// ui_models_test.js
// Unit and behavioral tests for OmniAuth Pro account model discovery UI (S1, S2)
// Uses Node.js built-in test runner (node:test, node:assert)

const test = require("node:test");
const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const vm = require("node:vm");

// Helper: load the client-side JavaScript from main.go
function loadClientScript() {
  const mainGoPath = path.join(__dirname, "main.go");
  const mainGoContent = fs.readFileSync(mainGoPath, "utf8");
  const scriptStart = mainGoContent.indexOf("<script>");
  const scriptEnd = mainGoContent.lastIndexOf("</script>");
  if (scriptStart === -1 || scriptEnd === -1) {
    throw new Error("Could not find <script> tags in main.go");
  }
  let script = mainGoContent.slice(scriptStart + "<script>".length, scriptEnd);
  // In main.go, backtick template literals in raw strings are written as ` + "`" + `
  // Normalize these back to actual backticks as compiled by Go
  script = script.replace(/`\s*\+\s*"`"\s*\+\s*`/g, "`");
  return script;
}

// Minimal DOM element mock for simulating the embedded UI
function createMockElement(id = "", tagName = "div") {
  const listeners = {};
  const classes = new Set();
  const attributes = {};
  let innerHTMLValue = "";
  let textContentValue = "";

  const elem = {
    id,
    tagName: tagName.toUpperCase(),
    style: {},
    value: "",
    disabled: false,
    attributes,
    classList: {
      add: (c) => classes.add(c),
      remove: (c) => classes.delete(c),
      toggle: (c, force) => {
        if (force === undefined) {
          classes.has(c) ? classes.delete(c) : classes.add(c);
        } else if (force) {
          classes.add(c);
        } else {
          classes.delete(c);
        }
      },
      contains: (c) => classes.has(c),
    },
    setAttribute: (k, v) => {
      attributes[k] = String(v);
    },
    getAttribute: (k) => attributes[k] || null,
    addEventListener: (evt, handler) => {
      if (!listeners[evt]) listeners[evt] = [];
      listeners[evt].push(handler);
    },
    dispatchEvent: async (evt) => {
      if (listeners[evt.type]) {
        for (const handler of listeners[evt.type]) {
          await handler(evt);
        }
      }
    },
    reset: () => {
      elem.value = "";
    },
    focus: () => {},
  };

  Object.defineProperty(elem, "innerHTML", {
    get: () => innerHTMLValue,
    set: (val) => {
      innerHTMLValue = String(val);
    },
  });

  Object.defineProperty(elem, "textContent", {
    get: () => textContentValue || innerHTMLValue,
    set: (val) => {
      textContentValue = String(val);
      innerHTMLValue = String(val);
    },
  });

  return elem;
}

// Set up simulated browser environment
function setupEnvironment(fetchMock) {
  const elements = {};
  function getOrCreate(id, tag = "div") {
    if (!elements[id]) {
      elements[id] = createMockElement(id, tag);
    }
    return elements[id];
  }

  // Pre-populate elements expected by main.go
  const requiredIds = [
    "accountsList",
    "authStatusText",
    "authStatusIndicator",
    "sessionBanner",
    "manualSecretKey",
    "alertBox",
    "addForm",
    "provider",
    "label",
    "apiKey",
    "prefix",
    "baseUrl",
    "finalCustomLogo",
    "filters",
  ];
  for (const id of requiredIds) {
    getOrCreate(id, id === "addForm" ? "form" : id.includes("Key") || id === "provider" || id === "label" || id === "prefix" || id === "baseUrl" || id === "finalCustomLogo" ? "input" : "div");
  }

  const document = {
    getElementById: (id) => getOrCreate(id),
    querySelectorAll: () => [],
    createElement: (tag) => createMockElement("", tag),
  };

  const window = {
    location: { host: "localhost:8000" },
    localStorage: {
      _store: { omniauth_manual_key: "test-management-key" },
      getItem: (k) => window.localStorage._store[k] || null,
      setItem: (k, v) => {
        window.localStorage._store[k] = String(v);
      },
      removeItem: (k) => {
        delete window.localStorage._store[k];
      },
    },
  };

  const navigator = { userAgent: "NodeTestAgent/1.0" };

  const context = {
    window,
    document,
    navigator,
    localStorage: window.localStorage,
    fetch: fetchMock || (async () => ({ ok: true, status: 200, json: async () => ({ ok: true, accounts: [] }) })),
    TextEncoder,
    TextDecoder,
    Uint8Array,
    btoa: (s) => Buffer.from(s, "binary").toString("base64"),
    atob: (s) => Buffer.from(s, "base64").toString("binary"),
    confirm: () => true,
    setTimeout: (fn) => setTimeout(fn, 0),
    console,
  };

  vm.createContext(context);
  return { context, elements, getOrCreate };
}

test("S1.1: Account card renders a model-fetch action and displays model identifiers", async (t) => {
  const scriptCode = loadClientScript();
  let fetchCallEndpoint = null;
  const mockModels = ["deepseek-chat", "deepseek-reasoner"];

  const fetchMock = async (url, opts = {}) => {
    fetchCallEndpoint = url;
    if (url.includes("/models")) {
      return {
        ok: true,
        status: 200,
        json: async () => ({
          ok: true,
          id: "deepseek-123.json",
          provider: "deepseek",
          models: mockModels,
          count: mockModels.length,
        }),
      };
    }
    // Default account list
    return {
      ok: true,
      status: 200,
      json: async () => ({
        ok: true,
        accounts: [
          {
            id: "deepseek-123.json",
            provider: "deepseek",
            label: "DeepSeek Official",
            key_mask: "sk-d...1234",
            prefix: "deepseek",
            created_at: "2025-01-01T00:00:00Z",
          },
        ],
      }),
    };
  };

  const { context, elements } = setupEnvironment(fetchMock);
  vm.runInContext(scriptCode, context);

  // 1. Initial accounts render
  await context.loadAccounts();
  const accountsHtml = elements["accountsList"].innerHTML;

  // The account card must offer a model-fetch action
  assert.ok(
    accountsHtml.includes("fetchModelsForAccount") || accountsHtml.includes("btn-models") || accountsHtml.includes("Obtener modelos"),
    "Account card must include a model-fetch action button"
  );

  // Verify fetchModelsForAccount function is defined
  assert.equal(typeof context.fetchModelsForAccount, "function", "fetchModelsForAccount must be a defined function");

  // 2. Fetch models for account
  await context.fetchModelsForAccount("deepseek-123.json");

  // Verify API was called referencing account ID
  assert.ok(fetchCallEndpoint && fetchCallEndpoint.includes("models"), "Must call models endpoint");
  assert.ok(fetchCallEndpoint.includes("deepseek-123.json"), "Must pass account ID to models fetch");

  // 3. Models must appear in the card
  const container = elements["models-box-deepseek-123.json"] || elements["accountsList"];
  const renderedContent = container.innerHTML;
  assert.ok(renderedContent.includes("deepseek-chat"), "Must display returned model deepseek-chat");
  assert.ok(renderedContent.includes("deepseek-reasoner"), "Must display returned model deepseek-reasoner");
});

test("S1.1: Safe text rendering prevents XSS in model identifiers", async (t) => {
  const scriptCode = loadClientScript();
  const maliciousModelId = '<script>alert("xss")</script><img src=x onerror=alert(1)>';

  const fetchMock = async (url) => {
    if (url.includes("/models")) {
      return {
        ok: true,
        status: 200,
        json: async () => ({
          ok: true,
          id: "custom-test.json",
          provider: "openai-compatible",
          models: [maliciousModelId],
        }),
      };
    }
    return {
      ok: true,
      status: 200,
      json: async () => ({
        ok: true,
        accounts: [
          {
            id: "custom-test.json",
            provider: "openai-compatible",
            label: "Custom",
            key_mask: "sk-...",
          },
        ],
      }),
    };
  };

  const { context, elements } = setupEnvironment(fetchMock);
  vm.runInContext(scriptCode, context);
  await context.loadAccounts();

  assert.equal(typeof context.fetchModelsForAccount, "function", "fetchModelsForAccount must be defined");
  await context.fetchModelsForAccount("custom-test.json");

  const container = elements["models-box-custom-test.json"] || elements["accountsList"];
  const html = container.innerHTML;

  // Unescaped malicious HTML must NOT appear raw in innerHTML
  assert.ok(!html.includes('<script>alert("xss")</script>'), "Raw script tags must not be rendered unescaped");
  assert.ok(!html.includes("<img src=x onerror=alert(1)>"), "Raw img tag with onerror must not be rendered unescaped");
});

test("S1.1 & D2: Discovery failure retains account card and displays retryable error", async (t) => {
  const scriptCode = loadClientScript();

  const fetchMock = async (url) => {
    if (url.includes("/models")) {
      return {
        ok: false,
        status: 502,
        statusText: "Bad Gateway",
        json: async () => ({
          ok: false,
          error: "Upstream provider timed out",
        }),
      };
    }
    return {
      ok: true,
      status: 200,
      json: async () => ({
        ok: true,
        accounts: [
          {
            id: "nvidia-123.json",
            provider: "nvidia",
            label: "NVIDIA Account",
            key_mask: "nvapi-...",
          },
        ],
      }),
    };
  };

  const { context, elements } = setupEnvironment(fetchMock);
  vm.runInContext(scriptCode, context);
  await context.loadAccounts();

  assert.equal(typeof context.fetchModelsForAccount, "function", "fetchModelsForAccount must be defined");
  await context.fetchModelsForAccount("nvidia-123.json");

  // Account card must still be retained
  const accountsHtml = elements["accountsList"].innerHTML;
  assert.ok(accountsHtml.includes("nvidia-123.json"), "Account card must be retained on discovery failure (D2)");

  // Container must show error message and retry option
  const container = elements["models-box-nvidia-123.json"] || elements["accountsList"];
  const text = container.innerHTML;
  assert.ok(text.toLowerCase().includes("error") || text.toLowerCase().includes("bad gateway") || text.includes("timed out"), "Must show error on discovery failure");
  assert.ok(text.includes("fetchModelsForAccount") || text.includes("Reintentar") || text.includes("retry"), "Must offer retry option on failure");
});

test("S2.1: Adding a key automatically initiates model discovery for the newly created account", async (t) => {
  const scriptCode = loadClientScript();
  const createdAccountId = "gemini-created-456.json";
  let discoveredAccountId = null;

  const fetchMock = async (url, opts = {}) => {
    if (opts.method === "POST" && !url.includes("refresh") && !url.includes("models")) {
      // Account creation response
      return {
        ok: true,
        status: 200,
        json: async () => ({
          ok: true,
          id: createdAccountId,
          provider: "gemini",
          message: "Cuenta creada con éxito",
        }),
      };
    }
    if (url.includes("/models")) {
      // Discovery endpoint
      const parsedUrl = new URL(url, "http://localhost");
      discoveredAccountId = parsedUrl.searchParams.get("id");
      return {
        ok: true,
        status: 200,
        json: async () => ({
          ok: true,
          id: createdAccountId,
          provider: "gemini",
          models: ["gemini-2.0-flash", "gemini-1.5-pro"],
        }),
      };
    }
    // Default account list
    return {
      ok: true,
      status: 200,
      json: async () => ({
        ok: true,
        accounts: [
          {
            id: createdAccountId,
            provider: "gemini",
            label: "Google Gemini",
            key_mask: "AIza...",
          },
        ],
      }),
    };
  };

  const { context, elements } = setupEnvironment(fetchMock);
  vm.runInContext(scriptCode, context);

  // Fill out the add form
  elements["provider"].value = "gemini";
  elements["label"].value = "Google Gemini";
  elements["apiKey"].value = "AIzaSyValidGeminiKey12345";

  // Submit the form
  const submitEvent = {
    type: "submit",
    preventDefault: () => {},
  };
  await elements["addForm"].dispatchEvent(submitEvent);

  // S2: Discovery must run for the created account without manual click
  assert.equal(
    discoveredAccountId,
    createdAccountId,
    "Adding a key must automatically initiate model discovery for newly saved account (S2.1)"
  );
});
