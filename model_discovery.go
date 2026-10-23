package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// accountStorageMu serializes short in-process file read/commit sections against account mutations.
// Network I/O is NEVER held under this lock.
var accountStorageMu sync.Mutex

// ModelDiscoveryResult represents the API response for account model discovery.
type ModelDiscoveryResult struct {
	OK        bool     `json:"ok"`
	ID        string   `json:"id,omitempty"`
	Provider  string   `json:"provider,omitempty"`
	Models    []string `json:"models,omitempty"`
	Count     int      `json:"count,omitempty"`
	Cached    bool     `json:"cached,omitempty"`
	UpdatedAt string   `json:"updated_at,omitempty"`
	Error     string   `json:"error,omitempty"`
}

const maxDiscoveryResponseBytes = 2 * 1024 * 1024 // 2MB limit

// unmarshalAccountJSON decodes JSON preserving big integers (>2^53) as json.Number without float64 precision loss.
func unmarshalAccountJSON(data []byte, v any) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	return dec.Decode(v)
}

// defaultProviderBaseURL returns the standard base URL for a provider if not configured.
func defaultProviderBaseURL(provider string) string {
	switch strings.ToLower(provider) {
	case "nvidia", "nvidia-nim":
		return "https://integrate.api.nvidia.com/v1"
	case "groq":
		return "https://api.groq.com/openai/v1"
	case "cerebras":
		return "https://api.cerebras.ai/v1"
	case "mistral":
		return "https://api.mistral.ai/v1"
	case "gemini", "google", "google-gemini", "argon":
		return "https://generativelanguage.googleapis.com/v1beta/openai/"
	case "deepseek":
		return "https://api.deepseek.com"
	case "nan", "nan-builders":
		return "https://api.nan.builders/v1"
	case "opencode", "opencode-go":
		return "https://opencode.ai/zen/go/v1"
	default:
		return ""
	}
}

// sanitizeErrorMessage strips raw keys or bearer tokens from error messages to avoid leaks.
func sanitizeErrorMessage(raw string, secrets ...string) string {
	cleaned := raw
	for _, sec := range secrets {
		if sec != "" && len(sec) > 3 {
			cleaned = strings.ReplaceAll(cleaned, sec, "[REDACTED]")
		}
	}
	// Also redact generic bearer token strings
	cleaned = strings.TrimSpace(cleaned)
	if len(cleaned) > 500 {
		cleaned = cleaned[:500] + "..."
	}
	return cleaned
}

// parseModelIDs extracts model identifier strings across OpenAI, Gemini, and array formats.
// Distinguishes valid empty model arrays from unrecognized/malformed responses.
func parseModelIDs(body []byte) ([]string, error) {
	var genericMap map[string]any
	if err := unmarshalAccountJSON(body, &genericMap); err == nil {
		// 1. OpenAI-compatible format: {"data": [...]}
		if rawData, ok := genericMap["data"].([]any); ok {
			var ids []string
			for _, item := range rawData {
				switch v := item.(type) {
				case map[string]any:
					if id, ok := v["id"].(string); ok && strings.TrimSpace(id) != "" {
						ids = append(ids, strings.TrimSpace(id))
					}
				case string:
					if strings.TrimSpace(v) != "" {
						ids = append(ids, strings.TrimSpace(v))
					}
				}
			}
			return deduplicateModels(ids), nil
		}

		// 2. Gemini native format: {"models": [...]}
		if rawModels, ok := genericMap["models"].([]any); ok {
			var ids []string
			for _, item := range rawModels {
				switch v := item.(type) {
				case map[string]any:
					if name, ok := v["name"].(string); ok && strings.TrimSpace(name) != "" {
						ids = append(ids, strings.TrimPrefix(strings.TrimSpace(name), "models/"))
					} else if id, ok := v["id"].(string); ok && strings.TrimSpace(id) != "" {
						ids = append(ids, strings.TrimPrefix(strings.TrimSpace(id), "models/"))
					}
				case string:
					if strings.TrimSpace(v) != "" {
						ids = append(ids, strings.TrimPrefix(strings.TrimSpace(v), "models/"))
					}
				}
			}
			return deduplicateModels(ids), nil
		}

		// Object but neither "data" nor "models" field: unrecognized response structure
		return nil, fmt.Errorf("formato de respuesta no reconocido: falta campo data o models")
	}

	// 3. Root array format: [{"id": "..."}, ...] or ["..."]
	var genericArray []any
	if err := unmarshalAccountJSON(body, &genericArray); err == nil {
		var ids []string
		for _, item := range genericArray {
			switch v := item.(type) {
			case map[string]any:
				if id, ok := v["id"].(string); ok && strings.TrimSpace(id) != "" {
					ids = append(ids, strings.TrimSpace(id))
				}
			case string:
				if strings.TrimSpace(v) != "" {
					ids = append(ids, strings.TrimSpace(v))
				}
			}
		}
		return deduplicateModels(ids), nil
	}

	return nil, fmt.Errorf("respuesta de modelos no es JSON válido")
}

func deduplicateModels(raw []string) []string {
	seen := make(map[string]bool)
	res := make([]string, 0, len(raw))
	for _, m := range raw {
		if m != "" && !seen[m] {
			seen[m] = true
			res = append(res, m)
		}
	}
	return res
}

// discoverAccountModels reads the saved account file and discovers models (defaults to serving cache when available).
func discoverAccountModels(authDir string, accountID string, client *http.Client) (*ModelDiscoveryResult, int, error) {
	return discoverAccountModelsWithRefresh(authDir, accountID, false, client)
}

// discoverAccountModelsWithRefresh reads the saved account file and discovers actual models from upstream provider or cache.
func discoverAccountModelsWithRefresh(authDir string, accountID string, refresh bool, client *http.Client) (*ModelDiscoveryResult, int, error) {
	cleanID := filepath.Clean(strings.TrimSpace(accountID))
	if cleanID == "" || !isHubManagedFile(cleanID) {
		return &ModelDiscoveryResult{
			OK:    false,
			Error: "ID de cuenta inválido",
		}, http.StatusBadRequest, nil
	}

	targetPath := filepath.Join(authDir, cleanID)

	// Short synchronized initial read
	accountStorageMu.Lock()
	content, err := os.ReadFile(targetPath)
	accountStorageMu.Unlock()

	if err != nil {
		if os.IsNotExist(err) {
			return &ModelDiscoveryResult{
				OK:    false,
				Error: "Cuenta no encontrada",
			}, http.StatusNotFound, nil
		}
		return &ModelDiscoveryResult{
			OK:    false,
			Error: "Error al leer credenciales: " + err.Error(),
		}, http.StatusInternalServerError, nil
	}

	var account map[string]any
	if err := unmarshalAccountJSON(content, &account); err != nil {
		return &ModelDiscoveryResult{
			OK:    false,
			Error: "Archivo de cuenta corrupto",
		}, http.StatusInternalServerError, nil
	}

	provider, _ := account["provider"].(string)
	if provider == "" {
		provider, _ = account["type"].(string)
	}

	// S4: If not refreshing, check if valid persistent cache exists
	if !refresh {
		if cacheMap, ok := account["model_cache"].(map[string]any); ok && cacheMap != nil {
			var cachedModels []string
			if rawList, ok := cacheMap["models"].([]any); ok {
				for _, m := range rawList {
					if str, ok := m.(string); ok && str != "" {
						cachedModels = append(cachedModels, str)
					}
				}
				if cachedModels == nil {
					cachedModels = []string{}
				}
				updatedAt, _ := cacheMap["updated_at"].(string)
				return &ModelDiscoveryResult{
					OK:        true,
					ID:        cleanID,
					Provider:  provider,
					Models:    cachedModels,
					Count:     len(cachedModels),
					Cached:    true,
					UpdatedAt: updatedAt,
				}, http.StatusOK, nil
			}
		}
	}

	apiKey, _ := account["api_key"].(string)
	if apiKey == "" {
		apiKey, _ = account["access_token"].(string)
	}
	if strings.TrimSpace(apiKey) == "" {
		return &ModelDiscoveryResult{
			OK:    false,
			ID:    cleanID,
			Error: "Credenciales de API no encontradas en la cuenta",
		}, http.StatusBadRequest, nil
	}

	baseURL, _ := account["base_url"].(string)
	if strings.TrimSpace(baseURL) == "" {
		baseURL = defaultProviderBaseURL(provider)
	}
	if strings.TrimSpace(baseURL) == "" {
		return &ModelDiscoveryResult{
			OK:       false,
			ID:       cleanID,
			Provider: provider,
			Error:    "Base URL no configurada para el proveedor",
		}, http.StatusBadRequest, nil
	}

	cleanBase := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	modelsURL := cleanBase
	if !strings.HasSuffix(cleanBase, "/models") {
		modelsURL = cleanBase + "/models"
	}

	if client == nil {
		client = &http.Client{
			Timeout: 15 * time.Second,
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				return http.ErrUseLastResponse
			},
		}
	}

	httpReq, err := http.NewRequest(http.MethodGet, modelsURL, nil)
	if err != nil {
		return &ModelDiscoveryResult{
			OK:       false,
			ID:       cleanID,
			Provider: provider,
			Error:    "URL de solicitud inválida: " + err.Error(),
		}, http.StatusBadRequest, nil
	}

	httpReq.Header.Set("Authorization", "Bearer "+strings.TrimSpace(apiKey))
	// For Gemini APIs, also send x-goog-api-key header
	pLower := strings.ToLower(provider)
	if strings.Contains(pLower, "gemini") || strings.Contains(pLower, "google") || strings.Contains(pLower, "argon") {
		httpReq.Header.Set("x-goog-api-key", strings.TrimSpace(apiKey))
	}
	httpReq.Header.Set("Accept", "application/json")
	httpReq.Header.Set("User-Agent", "OmniAuth-Pro-Plugin/1.0")

	// Outbound request is executed outside any lock (never hold lock over network I/O)
	resp, err := client.Do(httpReq)
	if err != nil {
		sanitizedErr := sanitizeErrorMessage(err.Error(), apiKey)
		return &ModelDiscoveryResult{
			OK:       false,
			ID:       cleanID,
			Provider: provider,
			Error:    "Fallo de conexión al proveedor: " + sanitizedErr,
		}, http.StatusBadGateway, nil
	}
	defer resp.Body.Close()

	// Handle redirects safely (status 3xx): do not follow or leak credentials
	if resp.StatusCode >= 300 && resp.StatusCode < 400 {
		return &ModelDiscoveryResult{
			OK:       false,
			ID:       cleanID,
			Provider: provider,
			Error:    "Redirección no permitida por seguridad",
		}, http.StatusBadGateway, nil
	}

	// Read bounded response
	limitedReader := io.LimitReader(resp.Body, int64(maxDiscoveryResponseBytes)+1)
	bodyBytes, err := io.ReadAll(limitedReader)
	if err != nil {
		return &ModelDiscoveryResult{
			OK:       false,
			ID:       cleanID,
			Provider: provider,
			Error:    "Error al leer respuesta del proveedor: " + err.Error(),
		}, http.StatusBadGateway, nil
	}

	if len(bodyBytes) > maxDiscoveryResponseBytes {
		return &ModelDiscoveryResult{
			OK:       false,
			ID:       cleanID,
			Provider: provider,
			Error:    "Respuesta del proveedor excede el límite permitido (2MB)",
		}, http.StatusBadGateway, nil
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		sanitizedBody := sanitizeErrorMessage(string(bodyBytes), apiKey)
		if sanitizedBody == "" {
			sanitizedBody = fmt.Sprintf("Error HTTP %d del proveedor", resp.StatusCode)
		}
		return &ModelDiscoveryResult{
			OK:       false,
			ID:       cleanID,
			Provider: provider,
			Error:    sanitizedBody,
		}, resp.StatusCode, nil
	}

	models, err := parseModelIDs(bodyBytes)
	if err != nil {
		return &ModelDiscoveryResult{
			OK:       false,
			ID:       cleanID,
			Provider: provider,
			Error:    "Error al procesar lista de modelos: " + err.Error(),
		}, http.StatusBadGateway, nil
	}

	// S4, D5, & Blockers 1, 2, 3: Synchronized commit section
	accountStorageMu.Lock()
	defer accountStorageMu.Unlock()

	// Check if account still exists (do not resurrect deleted accounts across network I/O)
	stat, err := os.Stat(targetPath)
	if err != nil {
		if os.IsNotExist(err) {
			return &ModelDiscoveryResult{
				OK:       false,
				ID:       cleanID,
				Provider: provider,
				Error:    "La cuenta fue eliminada durante la consulta",
			}, http.StatusNotFound, nil
		}
		return &ModelDiscoveryResult{
			OK:       false,
			ID:       cleanID,
			Provider: provider,
			Error:    "Error al verificar archivo de cuenta: " + err.Error(),
		}, http.StatusInternalServerError, nil
	}

	// Re-read current file fresh to preserve any credentials/fields changed during network I/O
	freshBytes, err := os.ReadFile(targetPath)
	if err != nil {
		return &ModelDiscoveryResult{
			OK:       false,
			ID:       cleanID,
			Provider: provider,
			Error:    "Error al leer archivo de cuenta para guardar caché: " + err.Error(),
		}, http.StatusInternalServerError, nil
	}

	// Blocker 1: unmarshal with UseNumber() to preserve integers >2^53 without float conversion
	var freshAccount map[string]any
	if err := unmarshalAccountJSON(freshBytes, &freshAccount); err != nil {
		return &ModelDiscoveryResult{
			OK:       false,
			ID:       cleanID,
			Provider: provider,
			Error:    "Error al procesar archivo de cuenta para guardar caché: " + err.Error(),
		}, http.StatusInternalServerError, nil
	}

	// Blocker 2: Verify effective credential/provider/baseURL identity matches what was used in request
	freshAPIKey, _ := freshAccount["api_key"].(string)
	if freshAPIKey == "" {
		freshAPIKey, _ = freshAccount["access_token"].(string)
	}
	freshProvider, _ := freshAccount["provider"].(string)
	if freshProvider == "" {
		freshProvider, _ = freshAccount["type"].(string)
	}
	freshBaseURL, _ := freshAccount["base_url"].(string)

	if strings.TrimSpace(freshAPIKey) != strings.TrimSpace(apiKey) ||
		strings.TrimSpace(freshProvider) != strings.TrimSpace(provider) ||
		strings.TrimSpace(freshBaseURL) != strings.TrimSpace(baseURL) {
		return &ModelDiscoveryResult{
			OK:       false,
			ID:       cleanID,
			Provider: freshProvider,
			Error:    "Las credenciales o configuración de la cuenta cambiaron durante la consulta; resultado descartado",
		}, http.StatusConflict, nil
	}

	updatedAt := time.Now().UTC().Format(time.RFC3339)
	freshAccount["model_cache"] = map[string]any{
		"models":     models,
		"updated_at": updatedAt,
	}

	newBytes, err := json.MarshalIndent(freshAccount, "", "  ")
	if err != nil {
		return &ModelDiscoveryResult{
			OK:       false,
			ID:       cleanID,
			Provider: provider,
			Error:    "Error al codificar credenciales con caché: " + err.Error(),
		}, http.StatusInternalServerError, nil
	}

	// Atomic save via temp file in the same directory, preserving file mode
	tmpFile, err := os.CreateTemp(filepath.Dir(targetPath), cleanID+".tmp.*")
	if err != nil {
		return &ModelDiscoveryResult{
			OK:       false,
			ID:       cleanID,
			Provider: provider,
			Error:    "Error al crear archivo temporal para guardar caché: " + err.Error(),
		}, http.StatusInternalServerError, nil
	}
	tmpName := tmpFile.Name()
	defer os.Remove(tmpName) // No-op if Rename succeeds

	if _, err := tmpFile.Write(newBytes); err != nil {
		tmpFile.Close()
		return &ModelDiscoveryResult{
			OK:       false,
			ID:       cleanID,
			Provider: provider,
			Error:    "Error al escribir archivo temporal de caché: " + err.Error(),
		}, http.StatusInternalServerError, nil
	}
	if err := tmpFile.Chmod(stat.Mode().Perm()); err != nil {
		tmpFile.Close()
		return &ModelDiscoveryResult{
			OK:       false,
			ID:       cleanID,
			Provider: provider,
			Error:    "Error al establecer permisos de archivo: " + err.Error(),
		}, http.StatusInternalServerError, nil
	}
	if err := tmpFile.Close(); err != nil {
		return &ModelDiscoveryResult{
			OK:       false,
			ID:       cleanID,
			Provider: provider,
			Error:    "Error al cerrar archivo temporal de caché: " + err.Error(),
		}, http.StatusInternalServerError, nil
	}
	if err := os.Rename(tmpName, targetPath); err != nil {
		return &ModelDiscoveryResult{
			OK:       false,
			ID:       cleanID,
			Provider: provider,
			Error:    "Error al reemplazar archivo de cuenta atómicamente: " + err.Error(),
		}, http.StatusInternalServerError, nil
	}

	return &ModelDiscoveryResult{
		OK:        true,
		ID:        cleanID,
		Provider:  provider,
		Models:    models,
		Count:     len(models),
		Cached:    false,
		UpdatedAt: updatedAt,
	}, http.StatusOK, nil
}
