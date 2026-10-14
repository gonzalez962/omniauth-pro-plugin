package main

/*
#include "cliproxy_plugin.h"

extern int cliproxyPluginCall(char*, uint8_t*, size_t, cliproxy_buffer*);
extern void cliproxyPluginFree(void*, size_t);
extern void cliproxyPluginShutdown(void);

static const cliproxy_host_api* stored_host;

static void store_host_api(const cliproxy_host_api* host) {
	stored_host = host;
}
*/
import "C"

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unsafe"
)

const abiVersion uint32 = 1

type envelope struct {
	OK     bool            `json:"ok"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *envelopeError  `json:"error,omitempty"`
}

type envelopeError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type ModelInfo struct {
	ID                         string   `json:"id"`
	Object                     string   `json:"object,omitempty"`
	Created                    int64    `json:"created,omitempty"`
	OwnedBy                    string   `json:"owned_by,omitempty"`
	Type                       string   `json:"type,omitempty"`
	DisplayName                string   `json:"display_name,omitempty"`
	Name                       string   `json:"name,omitempty"`
	Version                    string   `json:"version,omitempty"`
	Description                string   `json:"description,omitempty"`
	ContextLength              int64    `json:"context_length,omitempty"`
	MaxCompletionTokens        int64    `json:"max_completion_tokens,omitempty"`
	SupportedGenerationMethods []string `json:"supported_generation_methods,omitempty"`
}

type modelRegistrationResult struct {
	Provider string      `json:"provider"`
	Models   []ModelInfo `json:"models"`
}

// modelRequestHost is the subset of the host context sent alongside
// model.register / model.static / model.for_auth requests. Only the fields the
// plugin needs to respect exclusions are declared.
type modelRequestHost struct {
	ExcludedModels map[string][]string `json:"ExcludedModels"`
}

type modelRequest struct {
	Host modelRequestHost `json:"Host"`
}

// excludedModelsForProvider returns the exclusion patterns the host configured
// for the opencode-go provider, or nil when none exist.
func excludedModelsForProvider(request []byte) []string {
	if len(request) == 0 {
		return nil
	}
	var req modelRequest
	if err := json.Unmarshal(request, &req); err != nil {
		return nil
	}
	if req.Host.ExcludedModels == nil {
		return nil
	}
	return req.Host.ExcludedModels["opencode-go"]
}

// matchWildcard reports whether value matches pattern, mirroring the host's
// exclusion semantics: case-insensitive exact match, or '*' wildcard segments.
func matchWildcard(pattern, value string) bool {
	if pattern == "" {
		return false
	}
	pattern = strings.ToLower(pattern)
	value = strings.ToLower(value)
	if !strings.Contains(pattern, "*") {
		return pattern == value
	}
	parts := strings.Split(pattern, "*")
	if prefix := parts[0]; prefix != "" {
		if !strings.HasPrefix(value, prefix) {
			return false
		}
		value = value[len(prefix):]
	}
	if suffix := parts[len(parts)-1]; suffix != "" {
		if !strings.HasSuffix(value, suffix) {
			return false
		}
		value = value[:len(value)-len(suffix)]
	}
	for i := 1; i < len(parts)-1; i++ {
		segment := parts[i]
		if segment == "" {
			continue
		}
		idx := strings.Index(value, segment)
		if idx < 0 {
			return false
		}
		value = value[idx+len(segment):]
	}
	return true
}

func filterExcludedModels(models []ModelInfo, excluded []string) []ModelInfo {
	if len(models) == 0 || len(excluded) == 0 {
		return models
	}
	filtered := make([]ModelInfo, 0, len(models))
	for _, model := range models {
		blocked := false
		for _, pattern := range excluded {
			if matchWildcard(pattern, model.ID) {
				blocked = true
				break
			}
		}
		if !blocked {
			filtered = append(filtered, model)
		}
	}
	return filtered
}

type startLoginResponse struct {
	LoginURL           string `json:"login_url"`
	State              string `json:"state"`
	OAuthCallbackLabel string `json:"oauth_callback_label,omitempty"`
	Instructions       string `json:"instructions,omitempty"`
}

type pollLoginRequest struct {
	State string `json:"state"`
	Code  string `json:"code,omitempty"`
}

type pollLoginResponse struct {
	Status string `json:"status"`
	Error  string `json:"error,omitempty"`
}

type ManagementRequestPayload struct {
	Method  string              `json:"Method"`
	Path    string              `json:"Path"`
	Headers map[string][]string `json:"Headers"`
	Query   map[string][]string `json:"Query"`
	Body    []byte              `json:"Body"`
}

type ManagementResponsePayload struct {
	StatusCode int                 `json:"StatusCode"`
	Headers    map[string][]string `json:"Headers"`
	Body       string              `json:"Body"` // Base64 encoded payload
}

var (
	logMu sync.Mutex

	baseOpenCodeModelIDs = []string{
		"deepseek-v4-flash",
		"deepseek-v4-flash-vision-exp",
		"deepseek-flash",
		"deepseek-v4.1-flash",
		"deepseek-v4-pro",
		"glm-5.2",
		"glm-5.3",
		"grok-4.6",
		"grok-4.7",
		"muse-spark-1.2-contributor",
		"muse-spark-1.3-contributor",
		"glm-5.3-flash",
		"gpt-5.6-luna",
		"gpt-6-luna",
		"hy3",
		"hy4-preview",
		"kimi-k2.7-code",
		"kimi-k3",
		"mimo-v2.5",
		"mimo-v2.6-flash",
		"mimo-v2.5-pro",
		"mimo-v2.6-pro",
		"minimax-m2.7",
		"minimax-m3",
		"space-bunny-free",
		"longcat-2.0",
		"longcat-2.5-preview-free",
		"qwen3.8-max",
		"qwen3.8-flash",
		"qwen3.7-plus",
	}
)

func logToFile(msg string) {
	logMu.Lock()
	defer logMu.Unlock()
	f, err := os.OpenFile("/tmp/omniauth-pro-plugin.log", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return
	}
	defer f.Close()
	ts := time.Now().Format("2006-01-02 15:04:05.000")
	f.WriteString(fmt.Sprintf("[%s] %s\n", ts, msg))
}

func getOpenCodeModelCatalog() []ModelInfo {
	models := make([]ModelInfo, 0, len(baseOpenCodeModelIDs))
	for _, id := range baseOpenCodeModelIDs {
		models = append(models, ModelInfo{
			ID:                  id,
			Name:                id,
			DisplayName:         id,
			Type:                "opencode-go",
			OwnedBy:             "opencode-go",
			Object:              "model",
			Created:             1790587280,
			ContextLength:       1048576,
			MaxCompletionTokens: 64000,
			SupportedGenerationMethods: []string{
				"generateContent",
				"chat",
			},
		})
	}
	return models
}

func getAuthDir() string {
	if env := strings.TrimSpace(os.Getenv("CLIPROXY_AUTH_DIR")); env != "" {
		return env
	}
	candidates := []string{
		"/root/.cli-proxy-api",
		filepath.Join(os.Getenv("HOME"), ".cli-proxy-api"),
		".cli-proxy-api",
	}
	for _, c := range candidates {
		if fi, err := os.Stat(c); err == nil && fi.IsDir() {
			return c
		}
	}
	return candidates[0]
}

//export cliproxy_plugin_init
func cliproxy_plugin_init(host *C.cliproxy_host_api, plugin *C.cliproxy_plugin_api) C.int {
	logToFile("cliproxy_plugin_init called")
	if plugin == nil {
		return 1
	}
	C.store_host_api(host)
	plugin.abi_version = C.uint32_t(abiVersion)
	plugin.call = C.cliproxy_plugin_call_fn(C.cliproxyPluginCall)
	plugin.free_buffer = C.cliproxy_plugin_free_fn(C.cliproxyPluginFree)
	plugin.shutdown = C.cliproxy_plugin_shutdown_fn(C.cliproxyPluginShutdown)
	logToFile("cliproxy_plugin_init finished successfully")
	return 0
}

//export cliproxyPluginCall
func cliproxyPluginCall(method *C.char, request *C.uint8_t, requestLen C.size_t, response *C.cliproxy_buffer) (rc C.int) {
	if response != nil {
		response.ptr = nil
		response.len = 0
	}
	defer func() {
		if r := recover(); r != nil {
			logToFile(fmt.Sprintf("PANIC recovered in cliproxyPluginCall: %v", r))
			if response != nil && response.ptr != nil {
				C.free(response.ptr)
				response.ptr = nil
				response.len = 0
			}
			writeResponse(response, errorEnvelope("plugin_panic", fmt.Sprintf("recovered: %v", r)))
			rc = 1
		}
	}()

	if method == nil {
		writeResponse(response, errorEnvelope("invalid_method", "method required"))
		return 1
	}

	methodStr := C.GoString(method)
	var reqBytes []byte
	if request != nil && requestLen > 0 {
		reqBytes = C.GoBytes(unsafe.Pointer(request), C.int(requestLen))
	}

	logToFile(fmt.Sprintf("CALL method=%s reqLen=%d", methodStr, len(reqBytes)))

	raw, err := handlePluginMethod(methodStr, reqBytes)
	if err != nil {
		logToFile(fmt.Sprintf("ERROR method=%s: %v", methodStr, err))
		writeResponse(response, errorEnvelope("plugin_error", err.Error()))
		return 1
	}

	logToFile(fmt.Sprintf("REPLY method=%s bytes=%d", methodStr, len(raw)))
	writeResponse(response, raw)
	return 0
}

//export cliproxyPluginFree
func cliproxyPluginFree(ptr unsafe.Pointer, len C.size_t) {
	if ptr != nil {
		C.free(ptr)
	}
	_ = len
}

//export cliproxyPluginShutdown
func cliproxyPluginShutdown() {
	logToFile("cliproxyPluginShutdown called")
}

func handlePluginMethod(method string, request []byte) ([]byte, error) {
	switch method {
	case "plugin.register", "plugin.reconfigure":
		registration := map[string]any{
			"schema_version": 6,
			"metadata": map[string]any{
				"Name":             "OmniAuth Pro",
				"Version":          "1.0.0",
				"Author":           "Wilox <wilsonlavio9@gmail.com>",
				"Description":      "OmniAuth Pro: Centralized multi-account hub & auth provider for CPAMC",
				"GitHubRepository": "https://github.com/WiloxDev/omniauth-pro-plugin",
				"Logo":             "",
				"ConfigFields":     []any{},
			},
			"capabilities": map[string]any{
				"model_registrar": true,
				"model_provider":  true,
				"auth_provider":   true,
				"management_api":  true,
			},
		}
		raw, err := json.Marshal(registration)
		if err != nil {
			return nil, err
		}
		return okEnvelope(raw), nil

	case "management.register":
		regResponse := map[string]any{
			"resources": []map[string]any{
				{
					"Path":        "/accounts",
					"Menu":        "🌐 OmniAuth Pro",
					"Description": "Hub centralizado de múltiples cuentas para OpenCode Go, NVIDIA NIM, Groq, Cerebras y OpenAI-compatibles en CPAMC",
				},
			},
			"routes": []map[string]any{
				{"Method": "GET", "Path": "/hub/accounts"},
				{"Method": "POST", "Path": "/hub/accounts"},
				{"Method": "DELETE", "Path": "/hub/accounts"},
				{"Method": "GET", "Path": "/opencode/accounts"},
				{"Method": "POST", "Path": "/opencode/accounts"},
				{"Method": "DELETE", "Path": "/opencode/accounts"},
			},
		}
		raw, err := json.Marshal(regResponse)
		if err != nil {
			return nil, err
		}
		return okEnvelope(raw), nil

	case "management.handle":
		return handleManagementHTTP(request)

	case "model.register", "model.static", "model.for_auth":
		result := modelRegistrationResult{
			Provider: "opencode-go",
			Models:   filterExcludedModels(getOpenCodeModelCatalog(), excludedModelsForProvider(request)),
		}
		raw, err := json.Marshal(result)
		if err != nil {
			return nil, err
		}
		return okEnvelope(raw), nil

	case "auth.start_login", "start_login":
		resp := startLoginResponse{
			LoginURL:           "",
			State:              "waiting_for_key",
			OAuthCallbackLabel: "Enter OpenCode API Key or Account Key (oc_sk_...):",
			Instructions:       "1. Obtain your API key from OpenCode (https://opencode.ai) or your WiloxDev License Portal.\n2. Paste your key below to add and bind this account session to CPAMC.\n3. You can add multiple accounts by repeating this process in CPAMC.",
		}
		raw, err := json.Marshal(resp)
		if err != nil {
			return nil, err
		}
		return okEnvelope(raw), nil

	case "auth.poll_login", "poll_login":
		var req pollLoginRequest
		if len(request) > 0 {
			_ = json.Unmarshal(request, &req)
		}
		apiKey := strings.TrimSpace(req.Code)
		if apiKey == "" {
			raw, _ := json.Marshal(pollLoginResponse{Status: "pending"})
			return okEnvelope(raw), nil
		}

		if len(apiKey) < 10 {
			raw, _ := json.Marshal(pollLoginResponse{
				Status: "error",
				Error:  "Invalid key format: key must be a valid OpenCode or subscription key",
			})
			return okEnvelope(raw), nil
		}

		raw, _ := json.Marshal(pollLoginResponse{Status: "complete"})
		return okEnvelope(raw), nil

	case "auth.parse", "parse_auth":
		var parsed map[string]any
		if len(request) > 0 {
			_ = json.Unmarshal(request, &parsed)
		}

		label := "OpenCode Go"
		if rawLabel, ok := parsed["label"].(string); ok && strings.TrimSpace(rawLabel) != "" {
			label = strings.TrimSpace(rawLabel)
		} else if rawKey, ok := parsed["api_key"].(string); ok && len(rawKey) > 16 {
			label = fmt.Sprintf("OpenCode Go (%s...%s)", rawKey[:8], rawKey[len(rawKey)-4:])
		}

		res := map[string]any{
			"provider": "opencode-go",
			"status":   "active",
			"label":    label,
			"prefix":   "ocgo",
		}
		raw, _ := json.Marshal(res)
		return okEnvelope(raw), nil

	default:
		logToFile(fmt.Sprintf("unhandled method: %s", method))
		return errorEnvelope("unknown_method", "unknown method: "+method), nil
	}
}

func handleManagementHTTP(request []byte) ([]byte, error) {
	var req ManagementRequestPayload
	if len(request) > 0 {
		_ = json.Unmarshal(request, &req)
	}

	method := strings.ToUpper(strings.TrimSpace(req.Method))
	if method == "" {
		method = http.MethodGet
	}

	path := strings.TrimSpace(req.Path)
	if path == "" || path == "/" {
		path = "/accounts"
	}

	// API Endpoints
	if strings.HasSuffix(path, "/hub/accounts") || strings.HasSuffix(path, "/opencode/accounts") || strings.Contains(path, "/accounts/api") || strings.Contains(path, "/api/accounts") {
		return handleAccountsAPI(method, req)
	}

	// Render Management UI
	html := getAccountsHTML()
	resp := ManagementResponsePayload{
		StatusCode: http.StatusOK,
		Headers: map[string][]string{
			"Content-Type": {"text/html; charset=utf-8"},
		},
		Body: base64.StdEncoding.EncodeToString([]byte(html)),
	}
	raw, _ := json.Marshal(resp)
	return okEnvelope(raw), nil
}

type accountItem struct {
	ID        string `json:"id"`
	Provider  string `json:"provider"`
	Label     string `json:"label"`
	KeyMask   string `json:"key_mask"`
	Prefix    string `json:"prefix"`
	Disabled  bool   `json:"disabled"`
	CreatedAt string `json:"created_at"`
}

func isHubManagedFile(name string) bool {
	if !strings.HasSuffix(name, ".json") {
		return false
	}
	prefixes := []string{
		"opencode-go-",
		"nvidia-nim-",
		"groq-",
		"cerebras-",
		"mistral-",
		"gemini-",
		"google-",
		"nan-",
		"openai-compat-",
	}
	for _, p := range prefixes {
		if strings.HasPrefix(name, p) {
			return true
		}
	}
	return false
}

func handleAccountsAPI(method string, req ManagementRequestPayload) ([]byte, error) {
	authDir := getAuthDir()

	switch method {
	case http.MethodGet:
		// List accounts managed by Multi-Account Hub
		files, _ := os.ReadDir(authDir)
		accounts := []accountItem{}
		for _, f := range files {
			if !f.IsDir() && isHubManagedFile(f.Name()) {
				content, err := os.ReadFile(filepath.Join(authDir, f.Name()))
				if err != nil {
					continue
				}
				var item map[string]any
				if err := json.Unmarshal(content, &item); err == nil {
					label, _ := item["label"].(string)
					apiKey, _ := item["api_key"].(string)
					if apiKey == "" {
						apiKey, _ = item["access_token"].(string)
					}
					provider, _ := item["provider"].(string)
					if provider == "" {
						provider, _ = item["type"].(string)
					}
					disabled, _ := item["disabled"].(bool)
					prefix, _ := item["prefix"].(string)
					createdAt, _ := item["created_at"].(string)

					keyMask := "***"
					if len(apiKey) > 16 {
						keyMask = fmt.Sprintf("%s...%s", apiKey[:8], apiKey[len(apiKey)-4:])
					} else if len(apiKey) > 6 {
						keyMask = fmt.Sprintf("%s...", apiKey[:4])
					}

					accounts = append(accounts, accountItem{
						ID:        f.Name(),
						Provider:  provider,
						Label:     label,
						KeyMask:   keyMask,
						Prefix:    prefix,
						Disabled:  disabled,
						CreatedAt: createdAt,
					})
				}
			}
		}

		resBytes, _ := json.Marshal(map[string]any{
			"ok":       true,
			"accounts": accounts,
		})
		resp := ManagementResponsePayload{
			StatusCode: http.StatusOK,
			Headers: map[string][]string{
				"Content-Type": {"application/json; charset=utf-8"},
			},
			Body: base64.StdEncoding.EncodeToString(resBytes),
		}
		raw, _ := json.Marshal(resp)
		return okEnvelope(raw), nil

	case http.MethodPost:
		// Add new account
		var input struct {
			Provider string `json:"provider"`
			Label    string `json:"label"`
			APIKey   string `json:"api_key"`
			BaseURL  string `json:"base_url"`
			Prefix   string `json:"prefix"`
		}
		if len(req.Body) > 0 {
			_ = json.Unmarshal(req.Body, &input)
		}

		key := strings.TrimSpace(input.APIKey)
		if len(key) < 6 {
			resBytes, _ := json.Marshal(map[string]any{"ok": false, "error": "API Key inválida o muy corta"})
			resp := ManagementResponsePayload{
				StatusCode: http.StatusBadRequest,
				Headers:    map[string][]string{"Content-Type": {"application/json"}},
				Body:       base64.StdEncoding.EncodeToString(resBytes),
			}
			raw, _ := json.Marshal(resp)
			return okEnvelope(raw), nil
		}

		provider := strings.TrimSpace(strings.ToLower(input.Provider))
		if provider == "" {
			provider = "opencode-go"
		}

		label := strings.TrimSpace(input.Label)
		prefix := strings.TrimSpace(input.Prefix)
		baseURL := strings.TrimSpace(input.BaseURL)

		var filePrefix string
		accountData := map[string]any{
			"api_key":      key,
			"access_token": key,
			"disabled":     false,
			"created_at":   time.Now().UTC().Format(time.RFC3339),
		}

		switch provider {
		case "nvidia", "nvidia-nim":
			filePrefix = "nvidia-nim"
			if label == "" {
				label = "NVIDIA NIM Account"
			}
			if prefix == "" {
				prefix = "nim"
			}
			if baseURL == "" {
				baseURL = "https://integrate.api.nvidia.com/v1"
			}
			accountData["type"] = "nvidia"
			accountData["provider"] = "nvidia"
			accountData["label"] = label
			accountData["prefix"] = prefix
			accountData["base_url"] = baseURL

		case "groq":
			filePrefix = "groq"
			if label == "" {
				label = "Groq Account"
			}
			if prefix == "" {
				prefix = "groq"
			}
			if baseURL == "" {
				baseURL = "https://api.groq.com/openai/v1"
			}
			accountData["type"] = "openai-compatible"
			accountData["provider"] = "groq"
			accountData["label"] = label
			accountData["prefix"] = prefix
			accountData["base_url"] = baseURL

		case "cerebras":
			filePrefix = "cerebras"
			if label == "" {
				label = "Cerebras Account"
			}
			if prefix == "" {
				prefix = "cere"
			}
			if baseURL == "" {
				baseURL = "https://api.cerebras.ai/v1"
			}
			accountData["type"] = "openai-compatible"
			accountData["provider"] = "cerebras"
			accountData["label"] = label
			accountData["prefix"] = prefix
			accountData["base_url"] = baseURL

		case "mistral":
			filePrefix = "mistral"
			if label == "" {
				label = "Mistral AI Account"
			}
			if prefix == "" {
				prefix = "mistral"
			}
			if baseURL == "" {
				baseURL = "https://api.mistral.ai/v1"
			}
			accountData["type"] = "openai-compatible"
			accountData["provider"] = "mistral"
			accountData["label"] = label
			accountData["prefix"] = prefix
			accountData["base_url"] = baseURL

		case "gemini", "google", "google-gemini", "argon":
			filePrefix = "gemini"
			if label == "" {
				label = "Google (Gemini 4 Argon)"
			}
			if prefix == "" {
				prefix = "gemini"
			}
			if baseURL == "" {
				baseURL = "https://generativelanguage.googleapis.com/v1beta/openai/"
			}
			accountData["type"] = "openai-compatible"
			accountData["provider"] = "gemini"
			accountData["label"] = label
			accountData["prefix"] = prefix
			accountData["base_url"] = baseURL

		case "nan", "nan-builders":
			filePrefix = "nan"
			if label == "" {
				label = "NaN Builders Account"
			}
			if prefix == "" {
				prefix = "nan"
			}
			if baseURL == "" {
				baseURL = "https://api.nan.builders/v1"
			}
			accountData["type"] = "openai-compatible"
			accountData["provider"] = "nan"
			accountData["label"] = label
			accountData["prefix"] = prefix
			accountData["base_url"] = baseURL

		case "openai-compat", "custom":
			filePrefix = "openai-compat"
			if label == "" {
				label = "OpenAI Compatible Account"
			}
			if prefix == "" {
				prefix = "custom"
			}
			accountData["type"] = "openai-compatible"
			accountData["provider"] = "openai-compatible"
			accountData["label"] = label
			accountData["prefix"] = prefix
			if baseURL != "" {
				accountData["base_url"] = baseURL
			}

		default: // opencode-go
			filePrefix = "opencode-go"
			if label == "" {
				label = "OpenCode Go Account"
			}
			if prefix == "" {
				prefix = "ocgo"
			}
			if baseURL == "" {
				baseURL = "https://opencode.ai/zen/go/v1"
			}
			accountData["type"] = "opencode-go"
			accountData["provider"] = "opencode-go"
			accountData["label"] = label
			accountData["prefix"] = prefix
			accountData["base_url"] = baseURL
			accountData["fields"] = map[string]any{
				"provider_url": baseURL,
			}
		}

		// Calculate deterministic file ID
		hash := sha256.Sum256([]byte(key + provider))
		accountID := fmt.Sprintf("%s-%s.json", filePrefix, hex.EncodeToString(hash[:6]))

		encoded, _ := json.MarshalIndent(accountData, "", "  ")
		targetPath := filepath.Join(authDir, accountID)
		if err := os.WriteFile(targetPath, encoded, 0644); err != nil {
			resBytes, _ := json.Marshal(map[string]any{"ok": false, "error": "No se pudo guardar la cuenta: " + err.Error()})
			resp := ManagementResponsePayload{
				StatusCode: http.StatusInternalServerError,
				Headers:    map[string][]string{"Content-Type": {"application/json"}},
				Body:       base64.StdEncoding.EncodeToString(resBytes),
			}
			raw, _ := json.Marshal(resp)
			return okEnvelope(raw), nil
		}

		resBytes, _ := json.Marshal(map[string]any{
			"ok":       true,
			"id":       accountID,
			"provider": provider,
			"message":  "Cuenta de " + provider + " creada con éxito",
		})
		resp := ManagementResponsePayload{
			StatusCode: http.StatusOK,
			Headers:    map[string][]string{"Content-Type": {"application/json"}},
			Body:       base64.StdEncoding.EncodeToString(resBytes),
		}
		raw, _ := json.Marshal(resp)
		return okEnvelope(raw), nil

	case http.MethodDelete:
		// Delete account
		var input struct {
			ID string `json:"id"`
		}
		if len(req.Body) > 0 {
			_ = json.Unmarshal(req.Body, &input)
		}
		id := filepath.Clean(strings.TrimSpace(input.ID))
		if id == "" || !isHubManagedFile(id) {
			resBytes, _ := json.Marshal(map[string]any{"ok": false, "error": "ID de cuenta inválido"})
			resp := ManagementResponsePayload{
				StatusCode: http.StatusBadRequest,
				Headers:    map[string][]string{"Content-Type": {"application/json"}},
				Body:       base64.StdEncoding.EncodeToString(resBytes),
			}
			raw, _ := json.Marshal(resp)
			return okEnvelope(raw), nil
		}

		targetPath := filepath.Join(authDir, id)
		_ = os.Remove(targetPath)

		resBytes, _ := json.Marshal(map[string]any{"ok": true, "message": "Cuenta eliminada con éxito"})
		resp := ManagementResponsePayload{
			StatusCode: http.StatusOK,
			Headers:    map[string][]string{"Content-Type": {"application/json"}},
			Body:       base64.StdEncoding.EncodeToString(resBytes),
		}
		raw, _ := json.Marshal(resp)
		return okEnvelope(raw), nil
	}

	resp := ManagementResponsePayload{
		StatusCode: http.StatusMethodNotAllowed,
		Headers:    map[string][]string{"Content-Type": {"application/json"}},
		Body:       base64.StdEncoding.EncodeToString([]byte(`{"error":"method_not_allowed"}`)),
	}
	raw, _ := json.Marshal(resp)
	return okEnvelope(raw), nil
}

func getAccountsHTML() string {
	return `<!DOCTYPE html>
<html lang="es">
<head>
  <meta charset="UTF-8">
  <title>OmniAuth Pro - CPAMC</title>
  <meta name="viewport" content="width=device-width, initial-scale=1.0">
  <style>
    :root {
      --bg: #0f172a;
      --card-bg: #1e293b;
      --text: #f8fafc;
      --muted: #94a3b8;
      --primary: #3b82f6;
      --primary-hover: #2563eb;
      --danger: #ef4444;
      --danger-hover: #dc2626;
      --success: #10b981;
      --border: #334155;
      --gemini: #4285f4;
      --nvidia: #76b900;
      --groq: #f97316;
      --cerebras: #8b5cf6;
      --mistral: #f59e0b;
      --nan: #7D39EB;
    }
    * { box-sizing: border-box; margin: 0; padding: 0; font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, Helvetica, Arial, sans-serif; }
    body { background-color: var(--bg); color: var(--text); padding: 24px; }
    .header { margin-bottom: 24px; border-bottom: 1px solid var(--border); padding-bottom: 16px; }
    .header h1 { font-size: 1.5rem; font-weight: 700; display: flex; align-items: center; gap: 8px; }
    .header p { color: var(--muted); font-size: 0.875rem; margin-top: 4px; }
    .grid { display: grid; grid-template-columns: 1fr 2fr; gap: 24px; }
    @media (max-width: 860px) { .grid { grid-template-columns: 1fr; } }
    .card { background: var(--card-bg); border: 1px solid var(--border); border-radius: 8px; padding: 20px; }
    .card h2 { font-size: 1.1rem; margin-bottom: 16px; font-weight: 600; display: flex; align-items: center; justify-content: space-between; }
    .form-group { margin-bottom: 14px; }
    .form-group label { display: block; font-size: 0.85rem; color: var(--muted); margin-bottom: 6px; }
    .form-group input, .form-group select { width: 100%; background: #0b1120; border: 1px solid var(--border); border-radius: 6px; padding: 10px 12px; color: #fff; font-size: 0.9rem; }
    .form-group input:focus, .form-group select:focus { outline: none; border-color: var(--primary); }
    .btn { background: var(--primary); color: #fff; border: none; border-radius: 6px; padding: 10px 16px; font-weight: 500; font-size: 0.9rem; cursor: pointer; transition: 0.2s; width: 100%; }
    .btn:hover { background: var(--primary-hover); }
    .btn-danger { background: var(--danger); width: auto; padding: 6px 12px; font-size: 0.8rem; }
    .btn-danger:hover { background: var(--danger-hover); }
    .account-card { background: #0b1120; border: 1px solid var(--border); border-radius: 6px; padding: 14px; margin-bottom: 12px; display: flex; justify-content: space-between; align-items: center; gap: 14px; }
    .account-logo { width: 44px; height: 44px; border-radius: 8px; object-fit: contain; background: #1e293b; border: 1px solid var(--border); padding: 2px; flex-shrink: 0; }
    .account-logo-placeholder { width: 44px; height: 44px; border-radius: 8px; background: #1e293b; border: 1px solid var(--border); display: flex; align-items: center; justify-content: center; font-size: 1.3rem; flex-shrink: 0; }
    .account-main { display: flex; align-items: center; gap: 12px; flex: 1; }
    .provider-preview { display: flex; align-items: center; gap: 12px; padding: 10px; background: #0b1120; border: 1px solid var(--border); border-radius: 6px; margin-bottom: 14px; }
    .provider-preview img { width: 40px; height: 40px; border-radius: 6px; object-fit: contain; }
    .provider-preview-title { font-size: 0.9rem; font-weight: 600; }
    .provider-preview-desc { font-size: 0.75rem; color: var(--muted); }
    .account-info h3 { font-size: 0.95rem; font-weight: 600; display: flex; align-items: center; gap: 6px; }
    .account-info p { font-size: 0.8rem; color: var(--muted); font-family: monospace; margin-top: 4px; }
    .badge { font-size: 0.75rem; padding: 2px 6px; border-radius: 4px; font-weight: 600; }
    .badge-opencode { background: rgba(16, 185, 129, 0.2); color: var(--success); }
    .badge-gemini { background: rgba(66, 133, 244, 0.2); color: #60a5fa; border: 1px solid rgba(66, 133, 244, 0.4); }
    .badge-nvidia { background: rgba(118, 185, 0, 0.2); color: var(--nvidia); }
    .badge-groq { background: rgba(249, 115, 22, 0.2); color: var(--groq); }
    .badge-cerebras { background: rgba(139, 92, 246, 0.2); color: var(--cerebras); }
    .badge-mistral { background: rgba(245, 158, 11, 0.2); color: var(--mistral); }
    .badge-nan { background: rgba(125, 57, 235, 0.25); color: #c084fc; border: 1px solid rgba(125, 57, 235, 0.4); }
    .badge-custom { background: rgba(59, 130, 246, 0.2); color: var(--primary); }
    .models-hint { font-size: 0.75rem; color: var(--muted); margin-top: 6px; }
    .alert { padding: 10px; border-radius: 6px; font-size: 0.85rem; margin-bottom: 14px; display: none; }
    .alert-success { background: rgba(16, 185, 129, 0.2); color: var(--success); border: 1px solid var(--success); }
    .alert-error { background: rgba(239, 68, 68, 0.2); color: var(--danger); border: 1px solid var(--danger); }
    .filter-pills { display: flex; gap: 6px; margin-bottom: 12px; flex-wrap: wrap; }
    .filter-btn { background: #0b1120; border: 1px solid var(--border); color: var(--muted); font-size: 0.75rem; padding: 4px 10px; border-radius: 9999px; cursor: pointer; }
    .filter-btn.active { background: var(--primary); color: #fff; border-color: var(--primary); }
  </style>
</head>
<body>
  <div class="header">
    <h1>🌐 OmniAuth Pro</h1>
    <p>Hub centralizado de múltiples cuentas y credenciales para OpenCode Go, NVIDIA NIM, NaN Builders, Groq, Cerebras y proveedores OpenAI-compatibles en CPAMC.</p>
  </div>

  <div class="grid">
    <div class="card">
      <h2>➕ Conectar Nueva Cuenta</h2>
      <div id="alert" class="alert"></div>
      <form id="addForm">
        <div class="form-group">
          <label>Proveedor</label>
          <select id="provider" onchange="onProviderChange()">
            <option value="gemini">🔷 Google (Gemini 4 Argon)</option>
            <option value="opencode-go">🌐 OpenCode Go (Zen API)</option>
            <option value="nvidia">🟢 NVIDIA NIM (1,000 créditos / nvapi-...)</option>
            <option value="nan">🟣 NaN Builders (Inferencia UE / Cluster abierto)</option>
            <option value="groq">⚡ Groq Cloud (LPU ultra rápido)</option>
            <option value="cerebras">🧠 Cerebras AI (Inferencia ultra rápida)</option>
            <option value="mistral">🌪️ Mistral AI</option>
            <option value="openai-compat">⚙️ Personalizado (OpenAI-compatible)</option>
          </select>
        </div>
        <div id="providerPreview" class="provider-preview">
          <!-- Dinámico -->
        </div>
        <div class="form-group">
          <label>Nombre / Etiqueta de la Cuenta</label>
          <input type="text" id="label" placeholder="Ej: Cuenta 1 - Wilox" required />
        </div>
        <div class="form-group">
          <label id="apiKeyLabel">API Key de OpenCode (oc_sk_...)</label>
          <input type="password" id="apiKey" placeholder="oc_sk_..." required />
        </div>
        <div class="form-group" id="prefixGroup" style="display:none;">
          <label>Prefijo para Modelos (Opcional)</label>
          <input type="text" id="prefix" placeholder="ej: myprov" />
        </div>
        <div class="form-group" id="baseUrlGroup" style="display:none;">
          <label>Base URL Personalizada (Opcional)</label>
          <input type="text" id="baseUrl" placeholder="https://api.ejemplo.com/v1" />
        </div>
        <button type="submit" class="btn">Conectar y Crear Tarjeta en CPAMC</button>
      </form>
    </div>

    <div class="card">
      <h2>
        <span>📋 Cuentas Conectadas en CPAMC</span>
        <button class="filter-btn" onclick="loadAccounts()" style="font-size:0.8rem;">🔄 Refrescar</button>
      </h2>
      <div class="filter-pills" id="filters">
        <button class="filter-btn active" onclick="setFilter('all')">Todos</button>
        <button class="filter-btn" onclick="setFilter('opencode-go')">OpenCode</button>
        <button class="filter-btn" onclick="setFilter('gemini')">Google (Gemini 4 Argon)</button>
        <button class="filter-btn" onclick="setFilter('nvidia')">NVIDIA NIM</button>
        <button class="filter-btn" onclick="setFilter('nan')">NaN Builders</button>
        <button class="filter-btn" onclick="setFilter('groq')">Groq</button>
        <button class="filter-btn" onclick="setFilter('cerebras')">Cerebras</button>
        <button class="filter-btn" onclick="setFilter('otros')">Otros</button>
      </div>
      <div id="accountsList">Cargando cuentas...</div>
    </div>
  </div>

  <script>
    const apiUrl = "/v0/management/hub/accounts";
    let allAccounts = [];
    let currentFilter = "all";

    function getProviderLogo(provider) {
      const p = (provider || "").toLowerCase();
      if (p.includes("gemini") || p.includes("google") || p.includes("argon")) {
        return '<img src="' + geminiArgonLogoBase64 + '" class="account-logo" alt="Google (Gemini 4 Argon)" />';
      }
      if (p.includes("nvidia")) return '<div class="account-logo-placeholder" style="color:var(--nvidia);">🟢</div>';
      if (p.includes("groq")) return '<div class="account-logo-placeholder" style="color:var(--groq);">⚡</div>';
      if (p.includes("cerebras")) return '<div class="account-logo-placeholder" style="color:var(--cerebras);">🧠</div>';
      if (p.includes("mistral")) return '<div class="account-logo-placeholder" style="color:var(--mistral);">🌪️</div>';
      if (p.includes("nan")) return '<div class="account-logo-placeholder" style="color:var(--nan);">🟣</div>';
      if (p.includes("opencode")) return '<div class="account-logo-placeholder" style="color:var(--success);">🌐</div>';
      return '<div class="account-logo-placeholder" style="color:var(--primary);">⚙️</div>';
    }

    function onProviderChange() {
      const p = document.getElementById("provider").value;
      const keyLabel = document.getElementById("apiKeyLabel");
      const keyInput = document.getElementById("apiKey");
      const prefixGroup = document.getElementById("prefixGroup");
      const baseUrlGroup = document.getElementById("baseUrlGroup");
      const preview = document.getElementById("providerPreview");

      if (p === "gemini") {
        preview.innerHTML = '<img src="' + geminiArgonLogoBase64 + '" alt="Gemini 4 Argon Logo" /><div><div class="provider-preview-title">Google (Gemini 4 Argon)</div><div class="provider-preview-desc">Inferencia de última generación con soporte para Gemini 4 Argon y modelos 2.0/1.5</div></div>';
        preview.style.display = "flex";
        keyLabel.textContent = "API Key de Google (Gemini 4 Argon / AIzaSy...)";
        keyInput.placeholder = "AIzaSy...";
        prefixGroup.style.display = "none";
        baseUrlGroup.style.display = "none";
      } else if (p === "nvidia") {
        preview.innerHTML = '<div class="account-logo-placeholder" style="color:var(--nvidia);">🟢</div><div><div class="provider-preview-title">NVIDIA NIM</div><div class="provider-preview-desc">1,000 créditos API gratuitos con catálogo de inferencia empresarial</div></div>';
        preview.style.display = "flex";
        keyLabel.textContent = "NVIDIA API Key (nvapi-...)";
        keyInput.placeholder = "nvapi-...";
        prefixGroup.style.display = "none";
        baseUrlGroup.style.display = "none";
      } else if (p === "nan") {
        preview.innerHTML = '<div class="account-logo-placeholder" style="color:var(--nan);">🟣</div><div><div class="provider-preview-title">NaN Builders</div><div class="provider-preview-desc">Inferencia ultrarrápida en Europa (DeepSeek V4, GLM, MiMo, Qwen)</div></div>';
        preview.style.display = "flex";
        keyLabel.textContent = "API Key de NaN Builders";
        keyInput.placeholder = "API Key personal de nan.builders...";
        prefixGroup.style.display = "none";
        baseUrlGroup.style.display = "none";
      } else if (p === "groq") {
        preview.innerHTML = '<div class="account-logo-placeholder" style="color:var(--groq);">⚡</div><div><div class="provider-preview-title">Groq Cloud</div><div class="provider-preview-desc">LPU de latencia ultra baja para Llama 3.3 y Mixtral</div></div>';
        preview.style.display = "flex";
        keyLabel.textContent = "Groq Cloud API Key (gsk_...)";
        keyInput.placeholder = "gsk_...";
        prefixGroup.style.display = "none";
        baseUrlGroup.style.display = "none";
      } else if (p === "cerebras") {
        preview.innerHTML = '<div class="account-logo-placeholder" style="color:var(--cerebras);">🧠</div><div><div class="provider-preview-title">Cerebras AI</div><div class="provider-preview-desc">Inferencia Wafer-Scale ultra veloz (>1500 tokens/s)</div></div>';
        preview.style.display = "flex";
        keyLabel.textContent = "Cerebras API Key (csk-...)";
        keyInput.placeholder = "csk-...";
        prefixGroup.style.display = "none";
        baseUrlGroup.style.display = "none";
      } else if (p === "mistral") {
        preview.innerHTML = '<div class="account-logo-placeholder" style="color:var(--mistral);">🌪️</div><div><div class="provider-preview-title">Mistral AI</div><div class="provider-preview-desc">Modelos Mistral Large, Codestral y Pixtral</div></div>';
        preview.style.display = "flex";
        keyLabel.textContent = "Mistral API Key";
        keyInput.placeholder = "API Key de Mistral...";
        prefixGroup.style.display = "none";
        baseUrlGroup.style.display = "none";
      } else if (p === "openai-compat") {
        preview.innerHTML = '<div class="account-logo-placeholder" style="color:var(--primary);">⚙️</div><div><div class="provider-preview-title">OpenAI Compatible</div><div class="provider-preview-desc">Cualquier servidor o gateway local / remoto con formato OpenAI</div></div>';
        preview.style.display = "flex";
        keyLabel.textContent = "API Key o Token";
        keyInput.placeholder = "sk-...";
        prefixGroup.style.display = "block";
        baseUrlGroup.style.display = "block";
      } else {
        preview.innerHTML = '<div class="account-logo-placeholder" style="color:var(--success);">🌐</div><div><div class="provider-preview-title">OpenCode Go (Zen API)</div><div class="provider-preview-desc">Pool de modelos premium multi-proveedor</div></div>';
        preview.style.display = "flex";
        keyLabel.textContent = "API Key de OpenCode (oc_sk_...)";
        keyInput.placeholder = "oc_sk_...";
        prefixGroup.style.display = "none";
        baseUrlGroup.style.display = "none";
      }
    }

    function deobfuscateCPAMC(raw) {
      if (!raw || typeof raw !== "string") return "";
      const ENC_PREFIX = "enc::v1::";
      const SECRET_SALT = "cli-proxy-api-webui::secure-storage";
      if (!raw.startsWith(ENC_PREFIX)) return raw;

      try {
        const host = window.location.host;
        const ua = navigator.userAgent;
        const keyStr = SECRET_SALT + "|" + host + "|" + ua;
        const encoder = new TextEncoder();
        const keyBytes = encoder.encode(keyStr);

        const b64 = raw.slice(ENC_PREFIX.length);
        const binary = atob(b64);
        const dataBytes = new Uint8Array(binary.length);
        for (let i = 0; i < binary.length; i++) {
          dataBytes[i] = binary.charCodeAt(i);
        }

        const decrypted = new Uint8Array(dataBytes.length);
        for (let i = 0; i < dataBytes.length; i++) {
          decrypted[i] = dataBytes[i] ^ keyBytes[i % keyBytes.length];
        }
        return new TextDecoder().decode(decrypted);
      } catch (e) {
        return "";
      }
    }

    function getManagementKey() {
      try {
        if (window.parent && window.parent.localStorage) {
          const raw = window.parent.localStorage.getItem("cli-proxy-auth");
          if (raw) {
            const parsed = JSON.parse(deobfuscateCPAMC(raw));
            if (parsed?.state?.managementKey) return parsed.state.managementKey;
          }
        }
      } catch (e) {}

      try {
        const raw = localStorage.getItem("cli-proxy-auth");
        if (raw) {
          const parsed = JSON.parse(deobfuscateCPAMC(raw));
          if (parsed?.state?.managementKey) return parsed.state.managementKey;
        }
      } catch (e) {}

      return "";
    }

    async function apiFetch(endpoint, options = {}) {
      const key = getManagementKey();
      const headers = { "Content-Type": "application/json", ...(options.headers || {}) };
      if (key) {
        headers["Authorization"] = "Bearer " + key;
      }
      return fetch(endpoint, { ...options, headers });
    }

    function setFilter(f) {
      currentFilter = f;
      document.querySelectorAll("#filters .filter-btn").forEach(btn => {
        btn.classList.toggle("active", btn.getAttribute("onclick").includes(f));
      });
      renderAccounts();
    }

    function getProviderBadge(provider) {
      p = (provider || "opencode-go").toLowerCase();
      if (p.includes("gemini") || p.includes("google") || p.includes("argon")) return '<span class="badge badge-gemini">GOOGLE (GEMINI 4 ARGON)</span>';
      if (p.includes("nvidia")) return '<span class="badge badge-nvidia">NVIDIA NIM</span>';
      if (p.includes("groq")) return '<span class="badge badge-groq">GROQ</span>';
      if (p.includes("cerebras")) return '<span class="badge badge-cerebras">CEREBRAS</span>';
      if (p.includes("mistral")) return '<span class="badge badge-mistral">MISTRAL</span>';
      if (p.includes("nan")) return '<span class="badge badge-nan">NAN BUILDERS</span>';
      if (p.includes("opencode")) return '<span class="badge badge-opencode">OPENCODE GO</span>';
      return '<span class="badge badge-custom">OPENAI COMPAT</span>';
    }

    function getModelsHint(provider) {
      p = (provider || "").toLowerCase();
      if (p.includes("gemini") || p.includes("google") || p.includes("argon")) return "🔷 Google (Gemini 4 Argon), Gemini 2.0 Flash/Pro, Gemini 1.5 Pro, Experimental";
      if (p.includes("nvidia")) return "✨ Llama 3.3 70B, Kimi K2.5, Nemotron Ultra, DeepSeek R1, Qwen 2.5 Coder";
      if (p.includes("groq")) return "⚡ Llama 3.3 70B Versatile, Llama 3.1 8B Instant, Mixtral";
      if (p.includes("cerebras")) return "🧠 Llama 3.3 70B, Llama 3.1 8B a ultra alta velocidad (>1500 t/s)";
      if (p.includes("mistral")) return "🌪️ Mistral Large 2, Codestral, Pixtral, Ministral";
      if (p.includes("nan")) return "🟣 DeepSeek V4 Flash, GLM 5.3 Flash/Pro, MiMo V2.6 Flash, Qwen 3.8 Flash, Gemma 4";
      if (p.includes("opencode")) return "✨ DeepSeek V4 Flash/Pro, GLM 5.3, Grok 4.7, Qwen 3, Kimi K3";
      return "⚙️ Modelos personalizados del endpoint OpenAI Compatible";
    }

    function renderAccounts() {
      const listDiv = document.getElementById("accountsList");
      let list = allAccounts;
      if (currentFilter !== "all") {
        if (currentFilter === "otros") {
          list = list.filter(a => !["opencode-go", "nvidia", "nan", "groq", "cerebras"].includes(a.provider));
        } else {
          list = list.filter(a => (a.provider || "").toLowerCase().includes(currentFilter));
        }
      }

      if (!list || list.length === 0) {
        listDiv.innerHTML = '<p style="color:var(--muted); font-size:0.9rem;">No hay cuentas conectadas en esta categoría.</p>';
        return;
      }

      listDiv.innerHTML = list.map(acc => ` + "`" + `
        <div class="account-card">
          <div class="account-main">
            ${getProviderLogo(acc.provider)}
            <div class="account-info">
              <h3>${acc.label || 'Cuenta'} ${getProviderBadge(acc.provider)}</h3>
              <p>Key: ${acc.key_mask} | Prefijo: ${acc.prefix || 'defecto'} | Archivo: ${acc.id}</p>
              <div class="models-hint">${getModelsHint(acc.provider)}</div>
            </div>
          </div>
          <div>
            <button class="btn btn-danger" onclick="deleteAccount('${acc.id}')">🗑️ Quitar</button>
          </div>
        </div>
      ` + "`" + `).join("");
    }

    async function loadAccounts() {
      const listDiv = document.getElementById("accountsList");
      const key = getManagementKey();
      if (!key) {
        listDiv.innerHTML = ` + "`" + `
          <div style="background:rgba(245, 158, 11, 0.1); border:1px solid #f59e0b; border-radius:6px; padding:12px; font-size:0.85rem; color:#fde68a;">
            <strong>⚠️ Se requiere sesión recordada en CPAMC</strong>
            <p style="margin-top:6px; color:var(--muted);">Para que los plugins dentro del panel puedan comunicarse con la API de administración, asegúrate de iniciar sesión en CPAMC marcando la casilla <strong>"Remember password"</strong> (Recordar contraseña).</p>
          </div>
        ` + "`" + `;
        return;
      }

      try {
        const res = await apiFetch(apiUrl, { method: "GET" });
        if (res.status === 401) {
          listDiv.innerHTML = '<p style="color:var(--danger)">Sesión no autorizada (401). Vuelve a iniciar sesión en CPAMC con "Remember password".</p>';
          return;
        }
        if (!res.ok) {
          throw new Error("HTTP " + res.status + " " + res.statusText);
        }
        const data = await res.json();
        allAccounts = data.accounts || [];
        renderAccounts();
      } catch (err) {
        listDiv.innerHTML = '<p style="color:var(--danger)">Error al cargar cuentas: ' + err.message + '</p>';
      }
    }

    async function deleteAccount(id) {
      if (!confirm("¿Deseas quitar esta cuenta de CPAMC?")) return;
      try {
        const res = await apiFetch(apiUrl, {
          method: "DELETE",
          body: JSON.stringify({ id })
        });
        if (!res.ok) {
          throw new Error("HTTP " + res.status);
        }
        const data = await res.json();
        if (data.ok) {
          showAlert("Cuenta eliminada correctamente", "success");
          apiFetch("/v0/management/auth-files/refresh", { method: "POST" }).catch(() => {});
          loadAccounts();
        } else {
          showAlert(data.error || "Error al eliminar", "error");
        }
      } catch (e) {
        showAlert(e.message, "error");
      }
    }

    document.getElementById("addForm").addEventListener("submit", async (e) => {
      e.preventDefault();
      const provider = document.getElementById("provider").value;
      const label = document.getElementById("label").value;
      const apiKey = document.getElementById("apiKey").value;
      const prefix = document.getElementById("prefix").value;
      const baseUrl = document.getElementById("baseUrl").value;

      try {
        const res = await apiFetch(apiUrl, {
          method: "POST",
          body: JSON.stringify({ provider, label, api_key: apiKey, prefix, base_url: baseUrl })
        });
        if (!res.ok) {
          const errText = await res.text();
          let msg = "HTTP " + res.status;
          try {
            const errJson = JSON.parse(errText);
            if (errJson.error) msg = errJson.error;
          } catch (_) {}
          showAlert(msg, "error");
          return;
        }
        const data = await res.json();
        if (data.ok) {
          showAlert("¡Cuenta de " + provider + " conectada con éxito! Ya visible en Auth Files.", "success");
          document.getElementById("addForm").reset();
          onProviderChange();
          apiFetch("/v0/management/auth-files/refresh", { method: "POST" }).catch(() => {});
          loadAccounts();
        } else {
          showAlert(data.error || "No se pudo conectar la cuenta", "error");
        }
      } catch (e) {
        showAlert(e.message, "error");
      }
    });

    function showAlert(msg, type) {
      const el = document.getElementById("alert");
      el.textContent = msg;
      el.className = "alert alert-" + type;
      el.style.display = "block";
      setTimeout(() => { el.style.display = "none"; }, 4000);
    }

    onProviderChange();
    loadAccounts();
  </script>
</body>
</html>`
}

func okEnvelope(result []byte) []byte {
	raw, _ := json.Marshal(envelope{OK: true, Result: json.RawMessage(result)})
	return raw
}

func errorEnvelope(code, message string) []byte {
	raw, _ := json.Marshal(envelope{OK: false, Error: &envelopeError{Code: code, Message: message}})
	return raw
}

func writeResponse(response *C.cliproxy_buffer, raw []byte) {
	if response == nil || len(raw) == 0 {
		return
	}
	ptr := C.CBytes(raw)
	if ptr == nil {
		return
	}
	response.ptr = ptr
	response.len = C.size_t(len(raw))
}

func main() {
	// Empty main required for c-shared library
}
