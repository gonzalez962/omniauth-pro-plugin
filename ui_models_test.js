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
    "modelsModal",
    "modelsModalTitle",
    "modelsModalSubtitle",
    "modelsModalContent",
    "modelsModalRefreshBtn",
  ];
  for (const id of requiredIds) {
    const elem = getOrCreate(id, id === "addForm" ? "form" : id.includes("Key") || id === "provider" || id === "label" || id === "prefix" || id === "baseUrl" || id === "finalCustomLogo" ? "input" : "div");
    if (id === "modelsModal") {
      elem.style.display = "none";
    }
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

  // 3. Models must appear in the modal or card
  const container = (elements["modelsModalContent"] && elements["modelsModalContent"].innerHTML) ? elements["modelsModalContent"] : (elements["models-box-deepseek-123.json"] || elements["accountsList"]);
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

  const container = (elements["modelsModalContent"] && elements["modelsModalContent"].innerHTML) ? elements["modelsModalContent"] : (elements["models-box-custom-test.json"] || elements["accountsList"]);
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
  const container = (elements["modelsModalContent"] && elements["modelsModalContent"].innerHTML) ? elements["modelsModalContent"] : (elements["models-box-nvidia-123.json"] || elements["accountsList"]);
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

test("S3.1: Given an account, when opening its model list, then a modal identifies the account and displays its models; closing the modal returns to the unchanged cards", async (t) => {
  const scriptCode = loadClientScript();
  const mockModels = ["claude-3-7-sonnet", "claude-3-5-haiku"];

  const fetchMock = async (url) => {
    if (url.includes("/models")) {
      return {
        ok: true,
        status: 200,
        json: async () => ({
          ok: true,
          id: "anthropic-123.json",
          provider: "openai-compatible",
          models: mockModels,
          count: mockModels.length,
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
            id: "anthropic-123.json",
            provider: "openai-compatible",
            label: "Anthropic Work",
            key_mask: "sk-ant-...9876",
          },
        ],
      }),
    };
  };

  const { context, elements } = setupEnvironment(fetchMock);
  vm.runInContext(scriptCode, context);
  await context.loadAccounts();

  // Snapshot account cards before opening modal
  const initialCardsHtml = elements["accountsList"].innerHTML;

  // Verify modal is initially closed
  assert.equal(elements["modelsModal"].style.display, "none", "Modal must initially be hidden");

  // Open the models modal for the account
  if (typeof context.openModelsModal === "function") {
    await context.openModelsModal("anthropic-123.json");
  } else if (typeof context.fetchModelsForAccount === "function") {
    await context.fetchModelsForAccount("anthropic-123.json");
  } else {
    assert.fail("openModelsModal or fetchModelsForAccount must be defined");
  }

  // Modal must now be visible
  assert.notEqual(elements["modelsModal"].style.display, "none", "Modal must be visible after opening");

  // Modal title/header must identify the account
  const modalTitle = elements["modelsModalTitle"].textContent;
  assert.ok(
    modalTitle.includes("Anthropic Work") || modalTitle.includes("anthropic-123.json"),
    "Modal title must identify the account"
  );

  // Modal content must display the models
  const modalBody = elements["modelsModalContent"].innerHTML;
  assert.ok(modalBody.includes("claude-3-7-sonnet"), "Modal must display model claude-3-7-sonnet");
  assert.ok(modalBody.includes("claude-3-5-haiku"), "Modal must display model claude-3-5-haiku");

  // Closing the modal returns to the unchanged cards
  assert.equal(typeof context.closeModelsModal, "function", "closeModelsModal must be defined");
  context.closeModelsModal();

  assert.equal(elements["modelsModal"].style.display, "none", "Modal must be hidden after closing");
  assert.equal(elements["accountsList"].innerHTML, initialCardsHtml, "Cards must remain unchanged after closing modal");
});

test("S3.2: Given requests for different accounts, when responses arrive, then the visible modal MUST NOT show another account's models", async (t) => {
  const scriptCode = loadClientScript();

  let resolveAccountA;
  const slowPromiseA = new Promise((resolve) => {
    resolveAccountA = resolve;
  });

  const fetchMock = async (url) => {
    if (url.includes("account-a.json")) {
      await slowPromiseA;
      return {
        ok: true,
        status: 200,
        json: async () => ({
          ok: true,
          id: "account-a.json",
          provider: "provider-a",
          models: ["model-a-private-1", "model-a-private-2"],
        }),
      };
    }
    if (url.includes("account-b.json")) {
      return {
        ok: true,
        status: 200,
        json: async () => ({
          ok: true,
          id: "account-b.json",
          provider: "provider-b",
          models: ["model-b-fast-1"],
        }),
      };
    }
    return {
      ok: true,
      status: 200,
      json: async () => ({
        ok: true,
        accounts: [
          { id: "account-a.json", provider: "provider-a", label: "Account A" },
          { id: "account-b.json", provider: "provider-b", label: "Account B" },
        ],
      }),
    };
  };

  const { context, elements } = setupEnvironment(fetchMock);
  vm.runInContext(scriptCode, context);
  await context.loadAccounts();

  // 1. User starts fetching models for Account A (slow)
  const fetchAPromise = context.openModelsModal ? context.openModelsModal("account-a.json") : context.fetchModelsForAccount("account-a.json");

  // 2. User quickly switches and opens modal for Account B (fast)
  if (context.openModelsModal) {
    await context.openModelsModal("account-b.json");
  } else {
    await context.fetchModelsForAccount("account-b.json");
  }

  // Account B's modal content is now showing model-b-fast-1
  assert.ok(elements["modelsModalContent"].innerHTML.includes("model-b-fast-1"), "Modal must show Account B models");

  // 3. Now slow response for Account A arrives LATE
  resolveAccountA();
  await fetchAPromise;

  // S3.2: Visible modal for Account B MUST NOT show Account A's models!
  const modalContentAfterLateResponse = elements["modelsModalContent"].innerHTML;
  assert.ok(
    !modalContentAfterLateResponse.includes("model-a-private-1"),
    "Visible modal for Account B must NOT be overwritten by late Account A response (S3.2)"
  );
  assert.ok(
    modalContentAfterLateResponse.includes("model-b-fast-1"),
    "Visible modal must still display Account B models"
  );
});

test("S4.1 & S4.2: Opening saved models uses cache and offers refresh action", async (t) => {
  const scriptCode = loadClientScript();
  let requestedWithRefresh = null;

  const fetchMock = async (url) => {
    if (url.includes("/models")) {
      const parsed = new URL(url, "http://localhost");
      requestedWithRefresh = parsed.searchParams.get("refresh");
      if (requestedWithRefresh === "true") {
        return {
          ok: true,
          status: 200,
          json: async () => ({
            ok: true,
            id: "cached-acc.json",
            provider: "groq",
            models: ["refreshed-llama-3.3-70b"],
            cached: false,
          }),
        };
      }
      return {
        ok: true,
        status: 200,
        json: async () => ({
          ok: true,
          id: "cached-acc.json",
          provider: "groq",
          models: ["cached-llama-3.1-8b"],
          cached: true,
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
            id: "cached-acc.json",
            provider: "groq",
            label: "Groq Cached",
            key_mask: "gsk_...",
            model_cache: {
              models: ["cached-llama-3.1-8b"],
              updated_at: "2025-01-01T00:00:00Z",
            },
          },
        ],
      }),
    };
  };

  const { context, elements } = setupEnvironment(fetchMock);
  vm.runInContext(scriptCode, context);
  await context.loadAccounts();

  // Open modal (normal viewing uses cache without refresh=true)
  if (context.openModelsModal) {
    await context.openModelsModal("cached-acc.json");
  } else {
    await context.fetchModelsForAccount("cached-acc.json");
  }

  assert.equal(requestedWithRefresh, null, "Normal opening must not pass refresh=true");
  assert.ok(
    elements["modelsModalContent"].innerHTML.includes("cached-llama-3.1-8b"),
    "Must display cached models"
  );

  // Modal must offer refresh button
  assert.equal(typeof context.refreshModalModels, "function", "refreshModalModels must be defined");
  await context.refreshModalModels();

  // Refresh must pass refresh=true and update modal
  assert.equal(requestedWithRefresh, "true", "Explicit refresh must pass refresh=true");
  assert.ok(
    elements["modelsModalContent"].innerHTML.includes("refreshed-llama-3.3-70b"),
    "Modal must display updated models after refresh"
  );
});

test("Regression Blocker 4: Card button is always restored when modal is closed or switched before response arrives (success and error paths)", async (t) => {
  const scriptCode = loadClientScript();

  let resolveAccountA_Success;
  const slowPromiseSuccess = new Promise((resolve) => {
    resolveAccountA_Success = resolve;
  });

  let rejectAccountA_Error;
  const slowPromiseError = new Promise((_, reject) => {
    rejectAccountA_Error = reject;
  });

  let mode = "success";

  const fetchMock = async (url) => {
    if (url.includes("account-a.json")) {
      if (mode === "success") {
        await slowPromiseSuccess;
        return {
          ok: true,
          status: 200,
          json: async () => ({
            ok: true,
            id: "account-a.json",
            provider: "provider-a",
            models: ["model-a-val"],
          }),
        };
      } else {
        await slowPromiseError;
        return {
          ok: false,
          status: 500,
          json: async () => ({ ok: false, error: "Server exploded" }),
        };
      }
    }
    return {
      ok: true,
      status: 200,
      json: async () => ({
        ok: true,
        accounts: [
          { id: "account-a.json", provider: "provider-a", label: "Account A" },
          { id: "account-b.json", provider: "provider-b", label: "Account B" },
        ],
      }),
    };
  };

  const { context, elements } = setupEnvironment(fetchMock);
  vm.runInContext(scriptCode, context);
  await context.loadAccounts();

  // Part 1: Modal closed or switched before SUCCESS response arrives
  const fetchSuccessPromise = context.fetchModelsForAccount("account-a.json");

  // Card button for account A must be disabled during flight
  const cardBtnA = elements["btn-models-account-a.json"];
  assert.equal(cardBtnA.disabled, true, "Card button must be disabled while fetch in flight");

  // User closes modal or switches to account B
  context.closeModelsModal();
  assert.equal(context.window.activeModalAccountId || elements["modelsModal"].style.display === "none", true);

  // Now the slow success response resolves
  resolveAccountA_Success();
  await fetchSuccessPromise;

  // Originating card button MUST be restored (not stuck disabled!)
  assert.equal(cardBtnA.disabled, false, "Card button MUST be restored to enabled after late success response");
  assert.ok(!cardBtnA.textContent.includes("⏳"), "Card button text must not remain in loading state");

  // Part 2: Modal closed or switched before ERROR response arrives
  mode = "error";
  const fetchErrorPromise = context.fetchModelsForAccount("account-a.json");
  assert.equal(cardBtnA.disabled, true, "Card button must be disabled while second fetch in flight");

  // User switches active modal
  if (context.openModelsModal) {
    context.openModelsModal("account-b.json");
  } else {
    context.closeModelsModal();
  }

  // Now the slow error response arrives
  rejectAccountA_Error(new Error("Network failed"));
  await fetchErrorPromise;

  // Originating card button MUST be restored (not stuck disabled on error!)
  assert.equal(cardBtnA.disabled, false, "Card button MUST be restored to enabled after late error response");
  assert.ok(!cardBtnA.textContent.includes("⏳"), "Card button text must not remain in loading state on error");
});
