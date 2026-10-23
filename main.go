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
	ID         string `json:"id"`
	Provider   string `json:"provider"`
	Label      string `json:"label"`
	KeyMask    string `json:"key_mask"`
	Prefix     string `json:"prefix"`
	CustomLogo string `json:"custom_logo,omitempty"`
	Disabled   bool   `json:"disabled"`
	CreatedAt  string `json:"created_at"`
}

func isHubManagedFile(name string) bool {
	if filepath.Base(name) != name || strings.ContainsAny(name, "/\\") {
		return false
	}
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
		"deepseek-",
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
					customLogo, _ := item["custom_logo"].(string)
					createdAt, _ := item["created_at"].(string)

					keyMask := "***"
					if len(apiKey) > 16 {
						keyMask = fmt.Sprintf("%s...%s", apiKey[:8], apiKey[len(apiKey)-4:])
					} else if len(apiKey) > 6 {
						keyMask = fmt.Sprintf("%s...", apiKey[:4])
					}

					accounts = append(accounts, accountItem{
						ID:         f.Name(),
						Provider:   provider,
						Label:      label,
						KeyMask:    keyMask,
						Prefix:     prefix,
						CustomLogo: customLogo,
						Disabled:   disabled,
						CreatedAt:  createdAt,
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
			Provider   string `json:"provider"`
			Label      string `json:"label"`
			APIKey     string `json:"api_key"`
			BaseURL    string `json:"base_url"`
			Prefix     string `json:"prefix"`
			CustomLogo string `json:"custom_logo"`
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
		customLogo := strings.TrimSpace(input.CustomLogo)

		var filePrefix string
		accountData := map[string]any{
			"api_key":      key,
			"access_token": key,
			"disabled":     false,
			"created_at":   time.Now().UTC().Format(time.RFC3339),
		}
		if customLogo != "" {
			accountData["custom_logo"] = customLogo
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

		case "deepseek":
			filePrefix = "deepseek"
			if label == "" {
				label = "DeepSeek Official"
			}
			if prefix == "" {
				prefix = "deepseek"
			}
			if baseURL == "" {
				baseURL = "https://api.deepseek.com"
			}
			accountData["type"] = "openai-compatible"
			accountData["provider"] = "deepseek"
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
  <title>OmniAuth Pro - CPAMC Quantum</title>
  <meta name="viewport" content="width=device-width, initial-scale=1.0">
  <style>
    :root {
      --bg-base: #060913;
      --bg-gradient: radial-gradient(circle at 15% 15%, rgba(59, 130, 246, 0.18) 0%, transparent 45%),
                         radial-gradient(circle at 85% 85%, rgba(139, 92, 246, 0.20) 0%, transparent 50%),
                         radial-gradient(circle at 50% 50%, rgba(14, 165, 233, 0.08) 0%, transparent 60%),
                         #060913;
      --glass-panel: rgba(15, 23, 42, 0.65);
      --glass-panel-hover: rgba(30, 41, 59, 0.85);
      --glass-border: rgba(255, 255, 255, 0.08);
      --glass-border-glow: rgba(99, 102, 241, 0.45);
      --text-main: #f1f5f9;
      --text-muted: #94a3b8;
      --neon-cyan: #06b6d4;
      --neon-blue: #3b82f6;
      --neon-purple: #8b5cf6;
      --neon-pink: #ec4899;
      --neon-green: #10b981;
      --neon-amber: #f59e0b;
      --neon-red: #f43f5e;
      --font-stack: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, "Inter", sans-serif;
    }

    * { box-sizing: border-box; margin: 0; padding: 0; font-family: var(--font-stack); }

    body {
      background: var(--bg-gradient);
      background-attachment: fixed;
      color: var(--text-main);
      min-height: 100vh;
      padding: 28px 24px 60px 24px;
      overflow-x: hidden;
    }

    /* Ambient glow elements */
    .ambient-glow {
      position: fixed;
      width: 450px;
      height: 450px;
      border-radius: 50%;
      filter: blur(120px);
      pointer-events: none;
      z-index: 0;
      opacity: 0.35;
      animation: floatGlow 14s ease-in-out infinite alternate;
    }
    .glow-1 { top: -100px; left: -100px; background: linear-gradient(135deg, #3b82f6, #06b6d4); }
    .glow-2 { bottom: -100px; right: -100px; background: linear-gradient(135deg, #8b5cf6, #ec4899); }

    @keyframes floatGlow {
      0% { transform: translate(0, 0) scale(1); }
      100% { transform: translate(60px, 40px) scale(1.15); }
    }

    .container {
      position: relative;
      z-index: 1;
      max-width: 1400px;
      margin: 0 auto;
    }

    /* Header 2027 */
    .header {
      display: flex;
      justify-content: space-between;
      align-items: center;
      margin-bottom: 28px;
      padding-bottom: 20px;
      border-bottom: 1px solid var(--glass-border);
      position: relative;
    }
    .header-info h1 {
      font-size: 1.85rem;
      font-weight: 800;
      letter-spacing: -0.03em;
      background: linear-gradient(135deg, #ffffff 30%, #93c5fd 70%, #c084fc 100%);
      -webkit-background-clip: text;
      -webkit-text-fill-color: transparent;
      display: flex;
      align-items: center;
      gap: 12px;
    }
    .header-info p {
      color: var(--text-muted);
      font-size: 0.9rem;
      margin-top: 6px;
      max-width: 750px;
      line-height: 1.45;
    }
    .auth-status-badge {
      display: flex;
      align-items: center;
      gap: 8px;
      background: rgba(16, 185, 129, 0.12);
      border: 1px solid rgba(16, 185, 129, 0.3);
      padding: 6px 14px;
      border-radius: 9999px;
      font-size: 0.8rem;
      font-weight: 600;
      color: #34d399;
      box-shadow: 0 0 15px rgba(16, 185, 129, 0.2);
    }
    .auth-status-dot {
      width: 8px;
      height: 8px;
      border-radius: 50%;
      background: #10b981;
      box-shadow: 0 0 8px #10b981;
      animation: pulseDot 2s infinite;
    }
    @keyframes pulseDot {
      0%, 100% { opacity: 1; transform: scale(1); }
      50% { opacity: 0.5; transform: scale(0.85); }
    }

    /* Grid Layout */
    .grid {
      display: grid;
      grid-template-columns: 420px 1fr;
      gap: 28px;
      align-items: start;
    }
    @media (max-width: 1024px) {
      .grid { grid-template-columns: 1fr; }
    }

    /* Glassmorphism 3D Card */
    .card-3d {
      background: var(--glass-panel);
      backdrop-filter: blur(20px);
      -webkit-backdrop-filter: blur(20px);
      border: 1px solid var(--glass-border);
      border-radius: 20px;
      padding: 26px;
      box-shadow: 0 20px 40px -15px rgba(0, 0, 0, 0.6), inset 0 1px 1px rgba(255, 255, 255, 0.1);
      transition: all 0.35s cubic-bezier(0.16, 1, 0.3, 1);
      position: relative;
      overflow: hidden;
    }
    .card-3d::before {
      content: "";
      position: absolute;
      top: 0; left: 0; right: 0; height: 1px;
      background: linear-gradient(90deg, transparent, rgba(255, 255, 255, 0.25), transparent);
    }

    .card-title {
      font-size: 1.25rem;
      font-weight: 700;
      letter-spacing: -0.01em;
      margin-bottom: 20px;
      display: flex;
      align-items: center;
      justify-content: space-between;
    }

    /* Form Fields 2027 */
    .form-group {
      margin-bottom: 18px;
    }
    .form-group label {
      display: block;
      font-size: 0.8rem;
      font-weight: 600;
      text-transform: uppercase;
      letter-spacing: 0.05em;
      color: #94a3b8;
      margin-bottom: 8px;
    }
    .input-wrapper {
      position: relative;
    }
    .form-control {
      width: 100%;
      background: rgba(11, 17, 32, 0.75);
      border: 1px solid var(--glass-border);
      border-radius: 12px;
      padding: 12px 14px;
      color: #fff;
      font-size: 0.92rem;
      transition: all 0.25s ease;
      outline: none;
    }
    .form-control:focus {
      border-color: var(--neon-cyan);
      background: rgba(15, 23, 42, 0.95);
      box-shadow: 0 0 0 3px rgba(6, 182, 212, 0.2), 0 0 20px rgba(6, 182, 212, 0.2);
    }

    /* Interactive 3D Provider Preview */
    .provider-preview-card {
      display: flex;
      align-items: center;
      gap: 16px;
      padding: 14px 16px;
      background: linear-gradient(135deg, rgba(30, 41, 59, 0.6), rgba(15, 23, 42, 0.8));
      border: 1px solid rgba(255, 255, 255, 0.1);
      border-radius: 14px;
      margin-bottom: 20px;
      box-shadow: 0 8px 20px rgba(0,0,0,0.3);
      position: relative;
    }
    .preview-logo-box {
      width: 52px;
      height: 52px;
      border-radius: 12px;
      background: rgba(15, 23, 42, 0.9);
      border: 1px solid var(--glass-border);
      display: flex;
      align-items: center;
      justify-content: center;
      padding: 6px;
      flex-shrink: 0;
      box-shadow: 0 0 15px rgba(59, 130, 246, 0.25);
    }
    .preview-logo-box img {
      width: 100%;
      height: 100%;
      object-fit: contain;
    }
    .preview-title {
      font-weight: 700;
      font-size: 1rem;
      color: #fff;
    }
    .preview-desc {
      font-size: 0.78rem;
      color: var(--text-muted);
      margin-top: 3px;
      line-height: 1.35;
    }

    /* Custom Logo Upload Section */
    .logo-source-tabs {
      display: flex;
      gap: 8px;
      margin-bottom: 10px;
    }
    .logo-tab-btn {
      flex: 1;
      padding: 6px 10px;
      border-radius: 8px;
      background: rgba(15, 23, 42, 0.6);
      border: 1px solid var(--glass-border);
      color: var(--text-muted);
      font-size: 0.78rem;
      font-weight: 600;
      cursor: pointer;
      transition: all 0.2s;
    }
    .logo-tab-btn.active {
      background: rgba(59, 130, 246, 0.25);
      border-color: var(--neon-blue);
      color: #93c5fd;
    }
    .logo-upload-zone {
      border: 2px dashed rgba(255, 255, 255, 0.15);
      border-radius: 12px;
      padding: 14px;
      text-align: center;
      cursor: pointer;
      transition: all 0.25s;
      background: rgba(15, 23, 42, 0.4);
    }
    .logo-upload-zone:hover {
      border-color: var(--neon-cyan);
      background: rgba(6, 182, 212, 0.08);
    }
    .logo-upload-zone p {
      font-size: 0.8rem;
      color: var(--text-muted);
    }

    /* 3D Button */
    .btn-3d {
      width: 100%;
      background: linear-gradient(135deg, #3b82f6 0%, #8b5cf6 50%, #06b6d4 100%);
      background-size: 200% auto;
      color: #fff;
      border: none;
      border-radius: 14px;
      padding: 14px 20px;
      font-size: 0.95rem;
      font-weight: 700;
      letter-spacing: 0.02em;
      cursor: pointer;
      transition: all 0.3s cubic-bezier(0.16, 1, 0.3, 1);
      box-shadow: 0 10px 25px -5px rgba(59, 130, 246, 0.5), inset 0 1px 1px rgba(255,255,255,0.3);
      display: flex;
      align-items: center;
      justify-content: center;
      gap: 10px;
    }
    .btn-3d:hover {
      background-position: right center;
      transform: translateY(-2px) scale(1.01);
      box-shadow: 0 16px 32px -5px rgba(139, 92, 246, 0.6), inset 0 1px 1px rgba(255,255,255,0.4);
    }
    .btn-3d:active {
      transform: translateY(1px);
    }

    /* Filter Pills 2027 */
    .filter-pills-row {
      display: flex;
      gap: 8px;
      flex-wrap: wrap;
      margin-bottom: 22px;
    }
    .filter-pill {
      background: rgba(15, 23, 42, 0.7);
      border: 1px solid var(--glass-border);
      color: var(--text-muted);
      padding: 6px 14px;
      border-radius: 9999px;
      font-size: 0.82rem;
      font-weight: 600;
      cursor: pointer;
      transition: all 0.25s ease;
      backdrop-filter: blur(10px);
    }
    .filter-pill:hover {
      color: #fff;
      border-color: rgba(255, 255, 255, 0.25);
      transform: translateY(-1px);
    }
    .filter-pill.active {
      background: linear-gradient(135deg, rgba(59, 130, 246, 0.3), rgba(139, 92, 246, 0.3));
      border-color: var(--neon-cyan);
      color: #fff;
      box-shadow: 0 0 15px rgba(6, 182, 212, 0.35);
    }

    /* Account List Items 3D Tilt */
    .accounts-grid {
      display: flex;
      flex-direction: column;
      gap: 14px;
    }
    .account-card-3d {
      background: linear-gradient(135deg, rgba(30, 41, 59, 0.5) 0%, rgba(15, 23, 42, 0.7) 100%);
      border: 1px solid var(--glass-border);
      border-radius: 16px;
      padding: 16px 20px;
      display: flex;
      justify-content: space-between;
      align-items: center;
      transition: all 0.3s cubic-bezier(0.16, 1, 0.3, 1);
      position: relative;
      overflow: hidden;
    }
    .account-card-3d:hover {
      border-color: var(--glass-border-glow);
      transform: translateY(-3px) scale(1.008);
      box-shadow: 0 12px 30px rgba(0, 0, 0, 0.5), 0 0 20px rgba(99, 102, 241, 0.25);
    }
    .account-card-3d::after {
      content: "";
      position: absolute;
      top: -50%; left: -50%; width: 200%; height: 200%;
      background: radial-gradient(circle, rgba(255,255,255,0.06) 0%, transparent 60%);
      pointer-events: none;
      opacity: 0;
      transition: opacity 0.3s;
    }
    .account-card-3d:hover::after {
      opacity: 1;
    }

    .account-main {
      display: flex;
      align-items: center;
      gap: 16px;
      flex: 1;
    }
    .account-logo-frame {
      width: 52px;
      height: 52px;
      border-radius: 14px;
      background: rgba(11, 17, 32, 0.9);
      border: 1px solid var(--glass-border);
      display: flex;
      align-items: center;
      justify-content: center;
      padding: 6px;
      flex-shrink: 0;
      box-shadow: 0 4px 12px rgba(0,0,0,0.4);
    }
    .account-logo-frame img {
      width: 100%;
      height: 100%;
      object-fit: contain;
      border-radius: 8px;
    }
    .account-info h3 {
      font-size: 1.05rem;
      font-weight: 700;
      display: flex;
      align-items: center;
      gap: 10px;
      color: #fff;
    }
    .account-meta {
      display: flex;
      gap: 14px;
      font-size: 0.8rem;
      color: var(--text-muted);
      font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace;
      margin-top: 5px;
    }
    .meta-tag {
      background: rgba(255, 255, 255, 0.05);
      padding: 2px 8px;
      border-radius: 6px;
      border: 1px solid rgba(255, 255, 255, 0.05);
    }
    .models-hint {
      font-size: 0.78rem;
      color: #a5b4fc;
      margin-top: 6px;
      display: flex;
      align-items: center;
      gap: 6px;
    }

    /* Cyberpunk / Glass Badges */
    .badge {
      font-size: 0.72rem;
      padding: 3px 10px;
      border-radius: 8px;
      font-weight: 700;
      letter-spacing: 0.04em;
      text-transform: uppercase;
      box-shadow: 0 0 12px currentColor;
    }
    .badge-deepseek { background: rgba(77, 107, 254, 0.2); color: #708aff; border: 1px solid rgba(77, 107, 254, 0.5); }
    .badge-gemini { background: rgba(66, 133, 244, 0.2); color: #60a5fa; border: 1px solid rgba(66, 133, 244, 0.5); }
    .badge-opencode { background: rgba(16, 185, 129, 0.2); color: #34d399; border: 1px solid rgba(16, 185, 129, 0.5); }
    .badge-nvidia { background: rgba(118, 185, 0, 0.2); color: #84cc16; border: 1px solid rgba(118, 185, 0, 0.5); }
    .badge-groq { background: rgba(249, 115, 22, 0.2); color: #fb923c; border: 1px solid rgba(249, 115, 22, 0.5); }
    .badge-cerebras { background: rgba(139, 92, 246, 0.2); color: #a78bfa; border: 1px solid rgba(139, 92, 246, 0.5); }
    .badge-mistral { background: rgba(245, 158, 11, 0.2); color: #fbbf24; border: 1px solid rgba(245, 158, 11, 0.5); }
    .badge-nan { background: rgba(125, 57, 235, 0.25); color: #c084fc; border: 1px solid rgba(125, 57, 235, 0.5); }
    .badge-custom { background: rgba(59, 130, 246, 0.2); color: #60a5fa; border: 1px solid rgba(59, 130, 246, 0.5); }

    /* Action Buttons */
    .btn-delete {
      background: rgba(239, 68, 68, 0.15);
      border: 1px solid rgba(239, 68, 68, 0.3);
      color: #f87171;
      padding: 8px 14px;
      border-radius: 10px;
      font-size: 0.82rem;
      font-weight: 600;
      cursor: pointer;
      transition: all 0.2s ease;
      display: flex;
      align-items: center;
      gap: 6px;
    }
    .btn-delete:hover {
      background: var(--neon-red);
      color: #fff;
      box-shadow: 0 0 15px rgba(244, 63, 94, 0.5);
      transform: scale(1.05);
    }

    .btn-refresh {
      background: rgba(255, 255, 255, 0.08);
      border: 1px solid var(--glass-border);
      color: #fff;
      padding: 6px 14px;
      border-radius: 10px;
      font-size: 0.8rem;
      font-weight: 600;
      cursor: pointer;
      transition: all 0.2s;
    }
    .btn-refresh:hover {
      background: rgba(255, 255, 255, 0.16);
      border-color: rgba(255, 255, 255, 0.3);
    }

    /* Alerts */
    .alert-box {
      padding: 12px 16px;
      border-radius: 12px;
      font-size: 0.88rem;
      font-weight: 500;
      margin-bottom: 18px;
      display: none;
      animation: fadeIn 0.3s ease;
    }
    .alert-box.success {
      background: rgba(16, 185, 129, 0.15);
      color: #34d399;
      border: 1px solid rgba(16, 185, 129, 0.4);
      box-shadow: 0 0 20px rgba(16, 185, 129, 0.2);
    }
    .alert-box.error {
      background: rgba(244, 63, 94, 0.15);
      color: #fb7185;
      border: 1px solid rgba(244, 63, 94, 0.4);
      box-shadow: 0 0 20px rgba(244, 63, 94, 0.2);
    }

    /* Custom Key Input in Modal or Fallback Banner */
    .auth-helper-banner {
      background: linear-gradient(135deg, rgba(245, 158, 11, 0.12), rgba(234, 88, 12, 0.08));
      border: 1px solid rgba(245, 158, 11, 0.35);
      border-radius: 14px;
      padding: 18px;
      margin-bottom: 20px;
      box-shadow: 0 10px 25px rgba(245, 158, 11, 0.1);
    }
    .auth-helper-banner strong { color: #fcd34d; font-size: 0.95rem; }
    .auth-helper-banner p { color: #cbd5e1; font-size: 0.85rem; margin-top: 6px; line-height: 1.45; }
    .auth-helper-input-row {
      display: flex;
      gap: 10px;
      margin-top: 12px;
    }

    /* Skeleton Loading 2027 */
    .skeleton-card {
      height: 80px;
      border-radius: 16px;
      background: linear-gradient(90deg, rgba(30, 41, 59, 0.3) 25%, rgba(51, 65, 85, 0.5) 50%, rgba(30, 41, 59, 0.3) 75%);
      background-size: 200% 100%;
      animation: skeletonShimmer 1.5s infinite;
      border: 1px solid var(--glass-border);
    }
    @keyframes skeletonShimmer {
      0% { background-position: 200% 0; }
      100% { background-position: -200% 0; }
    }
    @keyframes fadeIn {
      from { opacity: 0; transform: translateY(-6px); }
      to { opacity: 1; transform: translateY(0); }
    }
  </style>
</head>
<body>
  <div class="ambient-glow glow-1"></div>
  <div class="ambient-glow glow-2"></div>

  <div class="container">
    <div class="header">
      <div class="header-info">
        <h1>🌐 OmniAuth Pro <span style="font-size:0.8rem; font-weight:600; padding:3px 10px; border-radius:9999px; background:linear-gradient(135deg, rgba(6,182,212,0.2), rgba(139,92,246,0.2)); border:1px solid rgba(6,182,212,0.4); color:#67e8f9;">v2.0 Quantum 2027</span></h1>
        <p>Hub multi-cuenta de alto rendimiento. Gestiona de forma unificada credenciales, logos personalizados, balanceo y aislamiento para DeepSeek, Google Gemini 4 Argon, NVIDIA NIM y clústeres compatibles.</p>
      </div>
      <div id="authStatusIndicator" class="auth-status-badge">
        <div class="auth-status-dot"></div>
        <span id="authStatusText">Conectando con CPAMC...</span>
      </div>
    </div>

    <!-- Fallback de sesión en caso de no estar en localStorage -->
    <div id="sessionBanner" class="auth-helper-banner" style="display:none;">
      <strong>🔑 Autenticación con CPAMC Management API</strong>
      <p>No se detectó el token en la sesión actual (ocurre si no activaste <em>"Remember password"</em> al iniciar sesión en el panel principal). Pega tu clave de administración para desbloquear la sincronización en vivo:</p>
      <div class="auth-helper-input-row">
        <input type="password" id="manualSecretKey" class="form-control" style="max-width:380px;" placeholder="Management Secret Key o Token..." />
        <button type="button" class="btn-refresh" onclick="saveManualKey()" style="background:var(--neon-blue); padding:10px 18px;">Vincular Clave</button>
      </div>
    </div>

    <div class="grid">
      <!-- Columna Izquierda: Formulario 3D -->
      <div class="card-3d">
        <div class="card-title">
          <span>✨ Conectar Nueva Cuenta</span>
          <span style="font-size:0.75rem; color:var(--neon-cyan);">Quantum Auth</span>
        </div>
        <div id="alertBox" class="alert-box"></div>

        <form id="addForm">
          <div class="form-group">
            <label>Proveedor IA</label>
            <select id="provider" class="form-control" onchange="onProviderChange()">
              <option value="deepseek">🐳 DeepSeek (Oficial - api.deepseek.com)</option>
              <option value="gemini">🔷 Google (Gemini 4 Argon / AI Studio)</option>
              <option value="opencode-go">🌐 OpenCode Go (Zen API)</option>
              <option value="nvidia">🟢 NVIDIA NIM (1,000 créditos / nvapi-...)</option>
              <option value="nan">🟣 NaN Builders (Inferencia UE / Cluster abierto)</option>
              <option value="groq">⚡ Groq Cloud (LPU ultra rápido)</option>
              <option value="cerebras">🧠 Cerebras AI (Inferencia ultra rápida)</option>
              <option value="mistral">🌪️ Mistral AI</option>
              <option value="openai-compat">⚙️ Personalizado (OpenAI-compatible)</option>
            </select>
          </div>

          <div id="providerPreview" class="provider-preview-card">
            <!-- Dinámico -->
          </div>

          <div class="form-group">
            <label>Etiqueta / Nombre de la Cuenta</label>
            <input type="text" id="label" class="form-control" placeholder="Ej: DeepSeek Cuenta 1 - Wilox" required />
          </div>

          <div class="form-group">
            <label id="apiKeyLabel">API Key de DeepSeek (sk-...)</label>
            <input type="password" id="apiKey" class="form-control" placeholder="sk-..." required />
          </div>

          <!-- Selector de Logo Personalizado -->
          <div class="form-group">
            <label>Logo de la Cuenta</label>
            <div class="logo-source-tabs">
              <button type="button" class="logo-tab-btn active" id="tabLogoDefault" onclick="setLogoTab('default')">Predeterminado</button>
              <button type="button" class="logo-tab-btn" id="tabLogoUrl" onclick="setLogoTab('url')">URL Externa</button>
              <button type="button" class="logo-tab-btn" id="tabLogoUpload" onclick="setLogoTab('upload')">Subir Imagen</button>
            </div>

            <div id="logoUrlField" style="display:none; margin-top:8px;">
              <input type="text" id="customLogoUrlInput" class="form-control" placeholder="https://ejemplo.com/logo.png" oninput="updateCustomLogoPreview(this.value)" />
            </div>

            <div id="logoUploadField" style="display:none; margin-top:8px;">
              <div class="logo-upload-zone" onclick="document.getElementById('logoFileInput').click()">
                <p>📁 Haz clic aquí para subir tu imagen PNG, SVG o WebP</p>
                <input type="file" id="logoFileInput" accept="image/*" style="display:none;" onchange="handleLogoFileSelect(event)" />
              </div>
            </div>
            <input type="hidden" id="finalCustomLogo" value="" />
          </div>

          <div class="form-group" id="prefixGroup" style="display:none;">
            <label>Prefijo para Modelos (Opcional)</label>
            <input type="text" id="prefix" class="form-control" placeholder="ej: myprov" />
          </div>

          <div class="form-group" id="baseUrlGroup" style="display:none;">
            <label>Base URL Personalizada (Opcional)</label>
            <input type="text" id="baseUrl" class="form-control" placeholder="https://api.ejemplo.com/v1" />
          </div>

          <button type="submit" class="btn-3d">
            <span>⚡ Conectar y Desplegar Tarjeta</span>
          </button>
        </form>
      </div>

      <!-- Columna Derecha: Cuentas Conectadas 3D -->
      <div class="card-3d">
        <div class="card-title">
          <span>📋 Cuentas Conectadas en CPAMC</span>
          <button class="btn-refresh" onclick="loadAccounts()">🔄 Sincronizar</button>
        </div>

        <div class="filter-pills-row" id="filters">
          <button class="filter-pill active" onclick="setFilter('all')">Todos</button>
          <button class="filter-pill" onclick="setFilter('deepseek')">DeepSeek</button>
          <button class="filter-pill" onclick="setFilter('gemini')">Google Gemini</button>
          <button class="filter-pill" onclick="setFilter('opencode-go')">OpenCode</button>
          <button class="filter-pill" onclick="setFilter('nvidia')">NVIDIA NIM</button>
          <button class="filter-pill" onclick="setFilter('nan')">NaN Builders</button>
          <button class="filter-pill" onclick="setFilter('groq')">Groq</button>
          <button class="filter-pill" onclick="setFilter('cerebras')">Cerebras</button>
          <button class="filter-pill" onclick="setFilter('otros')">Otros</button>
        </div>

        <div id="accountsList" class="accounts-grid">
          <div class="skeleton-card"></div>
          <div class="skeleton-card"></div>
        </div>
      </div>
    </div>
  </div>

  <script>
    const apiUrl = "/v0/management/hub/accounts";
    let allAccounts = [];
    let currentFilter = "all";
    let currentLogoTab = "default";

    function setLogoTab(tab) {
      currentLogoTab = tab;
      document.getElementById("tabLogoDefault").classList.toggle("active", tab === "default");
      document.getElementById("tabLogoUrl").classList.toggle("active", tab === "url");
      document.getElementById("tabLogoUpload").classList.toggle("active", tab === "upload");

      document.getElementById("logoUrlField").style.display = (tab === "url") ? "block" : "none";
      document.getElementById("logoUploadField").style.display = (tab === "upload") ? "block" : "none";

      if (tab === "default") {
        document.getElementById("finalCustomLogo").value = "";
        onProviderChange();
      }
    }

    function handleLogoFileSelect(e) {
      const file = e.target.files[0];
      if (!file) return;
      const reader = new FileReader();
      reader.onload = function(evt) {
        const base64 = evt.target.result;
        document.getElementById("finalCustomLogo").value = base64;
        updateCustomLogoPreview(base64);
      };
      reader.readAsDataURL(file);
    }

    function updateCustomLogoPreview(logoSrc) {
      document.getElementById("finalCustomLogo").value = logoSrc;
      const previewImg = document.querySelector("#providerPreview .preview-logo-box img");
      if (previewImg && logoSrc) {
        previewImg.src = logoSrc;
      }
    }

    function getProviderLogo(provider, customLogo) {
      if (customLogo && customLogo.trim() !== "") {
        return '<div class="account-logo-frame"><img src="' + customLogo + '" alt="Custom Logo" /></div>';
      }
      const p = (provider || "").toLowerCase();
      if (p.includes("deepseek")) {
        return '<div class="account-logo-frame"><img src="' + deepseekLogoBase64 + '" alt="DeepSeek" /></div>';
      }
      if (p.includes("gemini") || p.includes("google") || p.includes("argon")) {
        return '<div class="account-logo-frame"><img src="' + geminiArgonLogoBase64 + '" alt="Google (Gemini 4 Argon)" /></div>';
      }
      if (p.includes("nvidia")) return '<div class="account-logo-frame" style="color:var(--neon-green); font-size:1.6rem;">🟢</div>';
      if (p.includes("groq")) return '<div class="account-logo-frame" style="color:var(--neon-amber); font-size:1.6rem;">⚡</div>';
      if (p.includes("cerebras")) return '<div class="account-logo-frame" style="color:var(--neon-purple); font-size:1.6rem;">🧠</div>';
      if (p.includes("mistral")) return '<div class="account-logo-frame" style="color:#fbbf24; font-size:1.6rem;">🌪️</div>';
      if (p.includes("nan")) return '<div class="account-logo-frame" style="color:#c084fc; font-size:1.6rem;">🟣</div>';
      if (p.includes("opencode")) return '<div class="account-logo-frame" style="color:var(--neon-cyan); font-size:1.6rem;">🌐</div>';
      return '<div class="account-logo-frame" style="color:var(--neon-blue); font-size:1.6rem;">⚙️</div>';
    }

    function onProviderChange() {
      const p = document.getElementById("provider").value;
      const keyLabel = document.getElementById("apiKeyLabel");
      const keyInput = document.getElementById("apiKey");
      const prefixGroup = document.getElementById("prefixGroup");
      const baseUrlGroup = document.getElementById("baseUrlGroup");
      const preview = document.getElementById("providerPreview");
      const customLogo = document.getElementById("finalCustomLogo").value;

      let defaultLogo = deepseekLogoBase64;
      let title = "DeepSeek Oficial";
      let desc = "Acceso directo a api.deepseek.com para deepseek-chat (V3) y deepseek-reasoner (R1)";

      if (p === "gemini") {
        defaultLogo = geminiArgonLogoBase64;
        title = "Google (Gemini 4 Argon)";
        desc = "Inferencia multimodal de vanguardia y modelos 2.0/1.5 Pro en Google AI Studio";
        keyLabel.textContent = "API Key de Google (Gemini 4 Argon / AIzaSy...)";
        keyInput.placeholder = "AIzaSy...";
        prefixGroup.style.display = "none";
        baseUrlGroup.style.display = "none";
      } else if (p === "deepseek") {
        defaultLogo = deepseekLogoBase64;
        title = "DeepSeek Oficial";
        desc = "Acceso directo a api.deepseek.com para deepseek-chat (V3) y deepseek-reasoner (R1)";
        keyLabel.textContent = "API Key de DeepSeek (sk-...)";
        keyInput.placeholder = "sk-...";
        prefixGroup.style.display = "none";
        baseUrlGroup.style.display = "none";
      } else if (p === "nvidia") {
        title = "NVIDIA NIM";
        desc = "Catálogo de microservicios de inferencia empresarial con 1,000 créditos gratis";
        keyLabel.textContent = "NVIDIA API Key (nvapi-...)";
        keyInput.placeholder = "nvapi-...";
        prefixGroup.style.display = "none";
        baseUrlGroup.style.display = "none";
      } else if (p === "nan") {
        title = "NaN Builders";
        desc = "Inferencia ultrarrápida europea de código abierto (DeepSeek, GLM, MiMo, Qwen)";
        keyLabel.textContent = "API Key de NaN Builders";
        keyInput.placeholder = "API Key personal de nan.builders...";
        prefixGroup.style.display = "none";
        baseUrlGroup.style.display = "none";
      } else if (p === "groq") {
        title = "Groq Cloud";
        desc = "LPU de latencia ultra baja para Llama 3.3 70B y Mixtral";
        keyLabel.textContent = "Groq Cloud API Key (gsk_...)";
        keyInput.placeholder = "gsk_...";
        prefixGroup.style.display = "none";
        baseUrlGroup.style.display = "none";
      } else if (p === "cerebras") {
        title = "Cerebras AI";
        desc = "Inferencia Wafer-Scale masiva a ultra alta velocidad (>1500 tokens/s)";
        keyLabel.textContent = "Cerebras API Key (csk-...)";
        keyInput.placeholder = "csk-...";
        prefixGroup.style.display = "none";
        baseUrlGroup.style.display = "none";
      } else if (p === "mistral") {
        title = "Mistral AI";
        desc = "Modelos Mistral Large 2, Codestral y Pixtral de última generación";
        keyLabel.textContent = "Mistral API Key";
        keyInput.placeholder = "API Key de Mistral...";
        prefixGroup.style.display = "none";
        baseUrlGroup.style.display = "none";
      } else if (p === "openai-compat") {
        title = "OpenAI Compatible";
        desc = "Cualquier servidor o gateway local / remoto con formato OpenAI";
        keyLabel.textContent = "API Key o Token";
        keyInput.placeholder = "sk-...";
        prefixGroup.style.display = "block";
        baseUrlGroup.style.display = "block";
      } else {
        title = "OpenCode Go (Zen API)";
        desc = "Pool de modelos de programación e investigación de élite";
        keyLabel.textContent = "API Key de OpenCode (oc_sk_...)";
        keyInput.placeholder = "oc_sk_...";
        prefixGroup.style.display = "none";
        baseUrlGroup.style.display = "none";
      }

      const activeLogoSrc = customLogo || defaultLogo;
      preview.innerHTML = '<div class="preview-logo-box"><img src="' + activeLogoSrc + '" alt="Logo" /></div><div><div class="preview-title">' + title + '</div><div class="preview-desc">' + desc + '</div></div>';
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
      // 1. Check user manual override in localStorage
      const localManual = localStorage.getItem("omniauth_manual_key");
      if (localManual) return localManual;

      // 2. Check parent window storage (CPAMC standard)
      try {
        if (window.parent && window.parent.localStorage) {
          const raw = window.parent.localStorage.getItem("cli-proxy-auth");
          if (raw) {
            const parsed = JSON.parse(deobfuscateCPAMC(raw));
            if (parsed?.state?.managementKey) return parsed.state.managementKey;
          }
        }
      } catch (e) {}

      // 3. Check current window storage
      try {
        const raw = localStorage.getItem("cli-proxy-auth");
        if (raw) {
          const parsed = JSON.parse(deobfuscateCPAMC(raw));
          if (parsed?.state?.managementKey) return parsed.state.managementKey;
        }
      } catch (e) {}

      return "";
    }

    function saveManualKey() {
      const k = document.getElementById("manualSecretKey").value.trim();
      if (!k) return;
      localStorage.setItem("omniauth_manual_key", k);
      document.getElementById("sessionBanner").style.display = "none";
      loadAccounts();
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
      document.querySelectorAll("#filters .filter-pill").forEach(btn => {
        btn.classList.toggle("active", btn.getAttribute("onclick").includes("'" + f + "'"));
      });
      renderAccounts();
    }

    function getProviderBadge(provider) {
      const p = (provider || "opencode-go").toLowerCase();
      if (p.includes("deepseek")) return '<span class="badge badge-deepseek">DEEPSEEK</span>';
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
      const p = (provider || "").toLowerCase();
      if (p.includes("deepseek")) return "🐳 deepseek-chat (V3), deepseek-reasoner (R1)";
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
          list = list.filter(a => !["opencode-go", "nvidia", "nan", "groq", "cerebras", "deepseek", "gemini"].includes(a.provider));
        } else {
          list = list.filter(a => (a.provider || "").toLowerCase().includes(currentFilter));
        }
      }

      if (!list || list.length === 0) {
        listDiv.innerHTML = '<div style="padding:32px; text-align:center; color:var(--text-muted); font-size:0.95rem; border:1px dashed var(--glass-border); border-radius:16px;">🌱 No hay cuentas conectadas en esta categoría. Puedes añadir una usando el formulario lateral.</div>';
        return;
      }

      listDiv.innerHTML = list.map(acc => ` + "`" + `
        <div class="account-card-3d">
          <div class="account-main">
            ${getProviderLogo(acc.provider, acc.custom_logo)}
            <div class="account-info">
              <h3>${acc.label || 'Cuenta'} ${getProviderBadge(acc.provider)}</h3>
              <div class="account-meta">
                <span class="meta-tag">Key: ${acc.key_mask}</span>
                <span class="meta-tag">Prefijo: ${acc.prefix || 'defecto'}</span>
                <span class="meta-tag">Archivo: ${acc.id}</span>
              </div>
              <div class="models-hint">${getModelsHint(acc.provider)}</div>
            </div>
          </div>
          <div>
            <button class="btn-delete" onclick="deleteAccount('${acc.id}')">🗑️ Quitar</button>
          </div>
        </div>
      ` + "`" + `).join("");
    }

    async function loadAccounts() {
      const listDiv = document.getElementById("accountsList");
      const statusText = document.getElementById("authStatusText");
      const statusIndicator = document.getElementById("authStatusIndicator");

      try {
        const res = await apiFetch(apiUrl, { method: "GET" });
        if (res.status === 401) {
          statusText.textContent = "Sesión no vinculada (401)";
          statusIndicator.style.borderColor = "rgba(239, 68, 68, 0.4)";
          statusIndicator.style.background = "rgba(239, 68, 68, 0.15)";
          statusIndicator.style.color = "#f87171";
          document.getElementById("sessionBanner").style.display = "block";
          listDiv.innerHTML = '<div style="padding:24px; text-align:center; color:#f87171;">Introduce tu Management Key arriba para sincronizar las cuentas.</div>';
          return;
        }

        if (!res.ok) {
          throw new Error("HTTP " + res.status + " " + res.statusText);
        }

        const data = await res.json();
        allAccounts = data.accounts || [];

        statusText.textContent = "Conectado a CPAMC (" + allAccounts.length + " cuentas)";
        statusIndicator.style.borderColor = "rgba(16, 185, 129, 0.4)";
        statusIndicator.style.background = "rgba(16, 185, 129, 0.15)";
        statusIndicator.style.color = "#34d399";
        document.getElementById("sessionBanner").style.display = "none";

        renderAccounts();
      } catch (err) {
        statusText.textContent = "Modo Autónomo";
        statusIndicator.style.borderColor = "rgba(245, 158, 11, 0.4)";
        statusIndicator.style.background = "rgba(245, 158, 11, 0.15)";
        statusIndicator.style.color = "#fbbf24";
        document.getElementById("sessionBanner").style.display = "block";
      }
    }

    async function deleteAccount(id) {
      if (!confirm("¿Deseas quitar esta cuenta de CPAMC?")) return;
      try {
        const res = await apiFetch(apiUrl, {
          method: "DELETE",
          body: JSON.stringify({ id })
        });
        if (!res.ok) throw new Error("HTTP " + res.status);
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
      const customLogo = document.getElementById("finalCustomLogo").value;

      try {
        const res = await apiFetch(apiUrl, {
          method: "POST",
          body: JSON.stringify({
            provider,
            label,
            api_key: apiKey,
            prefix,
            base_url: baseUrl,
            custom_logo: customLogo
          })
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
          showAlert("¡Cuenta de " + provider + " conectada con éxito! Ya activa en CPAMC.", "success");
          document.getElementById("addForm").reset();
          document.getElementById("finalCustomLogo").value = "";
          setLogoTab('default');
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
      const el = document.getElementById("alertBox");
      el.textContent = msg;
      el.className = "alert-box " + type;
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
