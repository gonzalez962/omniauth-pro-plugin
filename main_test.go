package main

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPluginRegistration(t *testing.T) {
	raw, err := handlePluginMethod("plugin.register", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var env envelope
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatalf("failed to unmarshal envelope: %v", err)
	}
	if !env.OK {
		t.Fatalf("expected env.OK to be true")
	}

	var reg struct {
		SchemaVersion int             `json:"schema_version"`
		Metadata      map[string]any  `json:"metadata"`
		Capabilities  map[string]bool `json:"capabilities"`
	}
	if err := json.Unmarshal(env.Result, &reg); err != nil {
		t.Fatalf("failed to unmarshal registration: %v", err)
	}

	if reg.Metadata["Name"] != "OmniAuth Pro" {
		t.Errorf("expected Name 'OmniAuth Pro', got '%v'", reg.Metadata["Name"])
	}
	if !reg.Capabilities["management_api"] {
		t.Errorf("expected management_api capability to be true")
	}
}

func TestManagementRegisterAndAPI(t *testing.T) {
	tempAuth := t.TempDir()
	t.Setenv("CLIPROXY_AUTH_DIR", tempAuth)

	// Test management.register
	raw, err := handlePluginMethod("management.register", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var env envelope
	json.Unmarshal(raw, &env)
	var reg struct {
		Resources []struct {
			Path string `json:"Path"`
			Menu string `json:"Menu"`
		} `json:"resources"`
		Routes []struct {
			Method string `json:"Method"`
			Path   string `json:"Path"`
		} `json:"routes"`
	}
	json.Unmarshal(env.Result, &reg)
	if len(reg.Resources) == 0 || reg.Resources[0].Path != "/accounts" {
		t.Errorf("expected /accounts resource in management.register")
	}
	if len(reg.Resources) == 0 || reg.Resources[0].Menu != "🌐 OmniAuth Pro" {
		t.Errorf("expected Menu '🌐 OmniAuth Pro', got '%v'", reg.Resources[0].Menu)
	}

	foundHubRoute := false
	for _, r := range reg.Routes {
		if r.Path == "/hub/accounts" && r.Method == "GET" {
			foundHubRoute = true
			break
		}
	}
	if !foundHubRoute {
		t.Errorf("expected /hub/accounts GET route in management.register")
	}

	// Test GET UI HTML
	reqUI, _ := json.Marshal(ManagementRequestPayload{
		Method: "GET",
		Path:   "/accounts",
	})
	rawUI, err := handlePluginMethod("management.handle", reqUI)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var envUI envelope
	json.Unmarshal(rawUI, &envUI)
	var respUI ManagementResponsePayload
	json.Unmarshal(envUI.Result, &respUI)
	if respUI.StatusCode != 200 {
		t.Errorf("expected status 200, got %d", respUI.StatusCode)
	}

	// Test GET /v0/management/hub/accounts
	reqAPI, _ := json.Marshal(ManagementRequestPayload{
		Method: "GET",
		Path:   "/v0/management/hub/accounts",
	})
	rawAPI, err := handlePluginMethod("management.handle", reqAPI)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var envAPI envelope
	json.Unmarshal(rawAPI, &envAPI)
	var respAPI ManagementResponsePayload
	json.Unmarshal(envAPI.Result, &respAPI)
	if respAPI.StatusCode != 200 {
		t.Errorf("expected status 200, got %d", respAPI.StatusCode)
	}

	// Test POST /v0/management/hub/accounts for NVIDIA NIM
	reqPostNvidia, _ := json.Marshal(ManagementRequestPayload{
		Method: "POST",
		Path:   "/v0/management/hub/accounts",
		Body:   []byte(`{"provider":"nvidia","label":"NVIDIA NIM Wilox 1","api_key":"nvapi-testkey12345678"}`),
	})
	rawPostNvidia, _ := handlePluginMethod("management.handle", reqPostNvidia)
	var envPostNvidia envelope
	json.Unmarshal(rawPostNvidia, &envPostNvidia)
	var respPostNvidia ManagementResponsePayload
	json.Unmarshal(envPostNvidia.Result, &respPostNvidia)
	if respPostNvidia.StatusCode != 200 {
		t.Errorf("expected status 200 for nvidia key, got %d", respPostNvidia.StatusCode)
	}

	var createdNvidia struct {
		OK       bool   `json:"ok"`
		ID       string `json:"id"`
		Provider string `json:"provider"`
	}
	bodyNvidiaBytes, _ := base64.StdEncoding.DecodeString(respPostNvidia.Body)
	json.Unmarshal(bodyNvidiaBytes, &createdNvidia)
	if !createdNvidia.OK || !strings.HasPrefix(createdNvidia.ID, "nvidia-nim-") {
		t.Errorf("expected nvidia-nim ID prefix in response, got %+v", createdNvidia)
	}

	// Test POST /v0/management/hub/accounts valid OpenCode key
	reqPostValid, _ := json.Marshal(ManagementRequestPayload{
		Method: "POST",
		Path:   "/v0/management/hub/accounts",
		Body:   []byte(`{"provider":"opencode-go","label":"Cuenta Test Wilox","api_key":"oc_sk_1234567890abcdef"}`),
	})
	rawPostValid, _ := handlePluginMethod("management.handle", reqPostValid)
	var envPostValid envelope
	json.Unmarshal(rawPostValid, &envPostValid)
	var respPostValid ManagementResponsePayload
	json.Unmarshal(envPostValid.Result, &respPostValid)
	if respPostValid.StatusCode != 200 {
		t.Errorf("expected status 200 for valid key, got %d", respPostValid.StatusCode)
	}

	var createdRes struct {
		OK bool   `json:"ok"`
		ID string `json:"id"`
	}
	bodyBytes, _ := base64.StdEncoding.DecodeString(respPostValid.Body)
	json.Unmarshal(bodyBytes, &createdRes)
	if !createdRes.OK || createdRes.ID == "" {
		t.Errorf("expected account ID in response, got %+v", createdRes)
	}

	// Test POST /v0/management/hub/accounts for NaN Builders
	reqPostNaN, _ := json.Marshal(ManagementRequestPayload{
		Method: "POST",
		Path:   "/v0/management/hub/accounts",
		Body:   []byte(`{"provider":"nan","label":"NaN Builders Wilox","api_key":"nan_testkey_12345678"}`),
	})
	rawPostNaN, _ := handlePluginMethod("management.handle", reqPostNaN)
	var envPostNaN envelope
	json.Unmarshal(rawPostNaN, &envPostNaN)
	var respPostNaN ManagementResponsePayload
	json.Unmarshal(envPostNaN.Result, &respPostNaN)
	if respPostNaN.StatusCode != 200 {
		t.Errorf("expected status 200 for nan key, got %d", respPostNaN.StatusCode)
	}

	var createdNaN struct {
		OK bool   `json:"ok"`
		ID string `json:"id"`
	}
	bodyNaNBytes, _ := base64.StdEncoding.DecodeString(respPostNaN.Body)
	json.Unmarshal(bodyNaNBytes, &createdNaN)
	if !createdNaN.OK || !strings.HasPrefix(createdNaN.ID, "nan-") {
		t.Errorf("expected nan- ID prefix in response, got %+v", createdNaN)
	}

	// Clean up created NaN account via DELETE
	reqDeleteNaN, _ := json.Marshal(ManagementRequestPayload{
		Method: "DELETE",
		Path:   "/v0/management/hub/accounts",
		Body:   []byte(`{"id":"` + createdNaN.ID + `"}`),
	})
	rawDeleteNaN, _ := handlePluginMethod("management.handle", reqDeleteNaN)
	var envDeleteNaN envelope
	json.Unmarshal(rawDeleteNaN, &envDeleteNaN)
	var respDeleteNaN ManagementResponsePayload
	json.Unmarshal(envDeleteNaN.Result, &respDeleteNaN)
	if respDeleteNaN.StatusCode != 200 {
		t.Errorf("expected status 200 on delete nan, got %d", respDeleteNaN.StatusCode)
	}

	// Test POST /v0/management/hub/accounts for Google Gemini
	reqPostGemini, _ := json.Marshal(ManagementRequestPayload{
		Method: "POST",
		Path:   "/v0/management/hub/accounts",
		Body:   []byte(`{"provider":"gemini","label":"Google Gemini Wilox","api_key":"AIzaSyTestKey12345678"}`),
	})
	rawPostGemini, _ := handlePluginMethod("management.handle", reqPostGemini)
	var envPostGemini envelope
	json.Unmarshal(rawPostGemini, &envPostGemini)
	var respPostGemini ManagementResponsePayload
	json.Unmarshal(envPostGemini.Result, &respPostGemini)
	if respPostGemini.StatusCode != 200 {
		t.Errorf("expected status 200 for gemini key, got %d", respPostGemini.StatusCode)
	}

	var createdGemini struct {
		OK       bool   `json:"ok"`
		ID       string `json:"id"`
		Provider string `json:"provider"`
	}
	bodyGeminiBytes, _ := base64.StdEncoding.DecodeString(respPostGemini.Body)
	json.Unmarshal(bodyGeminiBytes, &createdGemini)
	if !createdGemini.OK || !strings.HasPrefix(createdGemini.ID, "gemini-") {
		t.Errorf("expected gemini- ID prefix in response, got %+v", createdGemini)
	}

	// Clean up created Gemini account via DELETE
	reqDeleteGemini, _ := json.Marshal(ManagementRequestPayload{
		Method: "DELETE",
		Path:   "/v0/management/hub/accounts",
		Body:   []byte(`{"id":"` + createdGemini.ID + `"}`),
	})
	rawDeleteGemini, _ := handlePluginMethod("management.handle", reqDeleteGemini)
	var envDeleteGemini envelope
	json.Unmarshal(rawDeleteGemini, &envDeleteGemini)
	var respDeleteGemini ManagementResponsePayload
	json.Unmarshal(envDeleteGemini.Result, &respDeleteGemini)
	if respDeleteGemini.StatusCode != 200 {
		t.Errorf("expected status 200 on delete gemini, got %d", respDeleteGemini.StatusCode)
	}

	// Test POST /v0/management/hub/accounts for DeepSeek Official
	reqPostDeepSeek, _ := json.Marshal(ManagementRequestPayload{
		Method: "POST",
		Path:   "/v0/management/hub/accounts",
		Body:   []byte(`{"provider":"deepseek","label":"DeepSeek Official Wilox","api_key":"sk-testkey1234567890abcdef"}`),
	})
	rawPostDeepSeek, _ := handlePluginMethod("management.handle", reqPostDeepSeek)
	var envPostDeepSeek envelope
	json.Unmarshal(rawPostDeepSeek, &envPostDeepSeek)
	var respPostDeepSeek ManagementResponsePayload
	json.Unmarshal(envPostDeepSeek.Result, &respPostDeepSeek)
	if respPostDeepSeek.StatusCode != 200 {
		t.Errorf("expected status 200 for deepseek key, got %d", respPostDeepSeek.StatusCode)
	}

	var createdDeepSeek struct {
		OK       bool   `json:"ok"`
		ID       string `json:"id"`
		Provider string `json:"provider"`
	}
	bodyDeepSeekBytes, _ := base64.StdEncoding.DecodeString(respPostDeepSeek.Body)
	json.Unmarshal(bodyDeepSeekBytes, &createdDeepSeek)
	if !createdDeepSeek.OK || !strings.HasPrefix(createdDeepSeek.ID, "deepseek-") {
		t.Errorf("expected deepseek- ID prefix in response, got %+v", createdDeepSeek)
	}

	// Clean up created DeepSeek account via DELETE
	reqDeleteDeepSeek, _ := json.Marshal(ManagementRequestPayload{
		Method: "DELETE",
		Path:   "/v0/management/hub/accounts",
		Body:   []byte(`{"id":"` + createdDeepSeek.ID + `"}`),
	})
	rawDeleteDeepSeek, _ := handlePluginMethod("management.handle", reqDeleteDeepSeek)
	var envDeleteDeepSeek envelope
	json.Unmarshal(rawDeleteDeepSeek, &envDeleteDeepSeek)
	var respDeleteDeepSeek ManagementResponsePayload
	json.Unmarshal(envDeleteDeepSeek.Result, &respDeleteDeepSeek)
	if respDeleteDeepSeek.StatusCode != 200 {
		t.Errorf("expected status 200 on delete deepseek, got %d", respDeleteDeepSeek.StatusCode)
	}

	// Clean up created test account via DELETE
	reqDelete, _ := json.Marshal(ManagementRequestPayload{
		Method: "DELETE",
		Path:   "/v0/management/opencode/accounts",
		Body:   []byte(`{"id":"` + createdRes.ID + `"}`),
	})
	rawDelete, _ := handlePluginMethod("management.handle", reqDelete)
	var envDelete envelope
	json.Unmarshal(rawDelete, &envDelete)
	var respDelete ManagementResponsePayload
	json.Unmarshal(envDelete.Result, &respDelete)
	if respDelete.StatusCode != 200 {
		t.Errorf("expected status 200 on delete, got %d", respDelete.StatusCode)
	}
}

func TestModelCatalog(t *testing.T) {
	raw, err := handlePluginMethod("model.register", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var env envelope
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatalf("failed to unmarshal envelope: %v", err)
	}
	if !env.OK {
		t.Fatalf("expected env.OK to be true")
	}

	var res modelRegistrationResult
	if err := json.Unmarshal(env.Result, &res); err != nil {
		t.Fatalf("failed to unmarshal modelRegistrationResult: %v", err)
	}

	if res.Provider != "opencode-go" {
		t.Errorf("expected Provider 'opencode-go', got '%s'", res.Provider)
	}
	if len(res.Models) != len(baseOpenCodeModelIDs) {
		t.Errorf("expected %d models, got %d", len(baseOpenCodeModelIDs), len(res.Models))
	}
}

func TestModelCatalogRespectsExcludedModels(t *testing.T) {
	req := []byte(`{"Host":{"ExcludedModels":{"opencode-go":["deepseek-v4-pro","glm-5.3"]}}}`)
	raw, err := handlePluginMethod("model.for_auth", req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var env envelope
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatalf("failed to unmarshal envelope: %v", err)
	}
	if !env.OK {
		t.Fatalf("expected env.OK to be true")
	}

	var res modelRegistrationResult
	if err := json.Unmarshal(env.Result, &res); err != nil {
		t.Fatalf("failed to unmarshal modelRegistrationResult: %v", err)
	}

	ids := make(map[string]bool, len(res.Models))
	for _, m := range res.Models {
		ids[m.ID] = true
	}
	for _, excluded := range []string{"deepseek-v4-pro", "glm-5.3"} {
		if ids[excluded] {
			t.Errorf("expected excluded model %q to be filtered out", excluded)
		}
	}
	if !ids["deepseek-v4-flash"] {
		t.Errorf("expected non-excluded model deepseek-v4-flash to remain")
	}
}

func TestModelCatalogExcludedWildcard(t *testing.T) {
	req := []byte(`{"Host":{"ExcludedModels":{"opencode-go":["deepseek-*"]}}}`)
	raw, err := handlePluginMethod("model.for_auth", req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var env envelope
	json.Unmarshal(raw, &env)
	var res modelRegistrationResult
	json.Unmarshal(env.Result, &res)

	for _, m := range res.Models {
		if strings.HasPrefix(strings.ToLower(m.ID), "deepseek-") {
			t.Errorf("expected wildcard-excluded model %q to be filtered out", m.ID)
		}
	}
	if len(res.Models) == 0 {
		t.Errorf("expected some models to remain after wildcard exclusion")
	}
}

func TestModelCatalogEmptyRequest(t *testing.T) {
	raw, err := handlePluginMethod("model.static", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var env envelope
	json.Unmarshal(raw, &env)
	var res modelRegistrationResult
	json.Unmarshal(env.Result, &res)
	if len(res.Models) != len(baseOpenCodeModelIDs) {
		t.Errorf("expected full catalog (%d) with empty request, got %d", len(baseOpenCodeModelIDs), len(res.Models))
	}
}

func TestAuthFlowMultiAccount(t *testing.T) {
	sampleKey := "oc_sk_a664a43e0e73_Wyq2BJXXjusBKvLZeLL8e7YdKStNy0T7"

	// Parse auth with custom label
	reqParseWithLabel := []byte(`{"api_key":"` + sampleKey + `","label":"OpenCode Cuenta Wilox 1"}`)
	rawParse1, err := handlePluginMethod("auth.parse", reqParseWithLabel)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var envParse1 envelope
	json.Unmarshal(rawParse1, &envParse1)
	var parsed1 map[string]any
	json.Unmarshal(envParse1.Result, &parsed1)
	if parsed1["label"] != "OpenCode Cuenta Wilox 1" {
		t.Errorf("expected custom label to be preserved, got '%v'", parsed1["label"])
	}

	// Parse auth without label
	reqParseNoLabel := []byte(`{"api_key":"` + sampleKey + `"}`)
	rawParse2, err := handlePluginMethod("auth.parse", reqParseNoLabel)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var envParse2 envelope
	json.Unmarshal(rawParse2, &envParse2)
	var parsed2 map[string]any
	json.Unmarshal(envParse2.Result, &parsed2)
	if !strings.HasPrefix(parsed2["label"].(string), "OpenCode Go (oc_sk_a6...") {
		t.Errorf("expected auto truncated label, got '%v'", parsed2["label"])
	}
}

func TestFrontendHTMLAndAssets(t *testing.T) {
	html := getAccountsHTML()

	// Verify required providers are available in <select id="provider">
	expectedProviders := []string{
		`value="deepseek"`,
		`value="gemini"`,
		`value="opencode-go"`,
		`value="nvidia"`,
		`value="nan"`,
		`value="groq"`,
		`value="cerebras"`,
		`value="mistral"`,
		`value="openai-compat"`,
	}
	for _, p := range expectedProviders {
		if !strings.Contains(html, p) {
			t.Errorf("expected HTML dropdown to contain %s", p)
		}
	}

	// Verify badges in CSS/HTML
	expectedBadges := []string{
		"badge-deepseek",
		"badge-gemini",
		"badge-nvidia",
		"badge-groq",
		"badge-cerebras",
		"badge-mistral",
		"badge-nan",
		"badge-opencode",
		"badge-custom",
	}
	for _, b := range expectedBadges {
		if !strings.Contains(html, b) {
			t.Errorf("expected HTML to contain badge class %s", b)
		}
	}

	// Verify filter buttons
	expectedFilters := []string{
		"setFilter('all')",
		"setFilter('deepseek')",
		"setFilter('opencode-go')",
		"setFilter('gemini')",
		"setFilter('nvidia')",
		"setFilter('nan')",
		"setFilter('groq')",
		"setFilter('cerebras')",
		"setFilter('otros')",
	}
	for _, f := range expectedFilters {
		if !strings.Contains(html, f) {
			t.Errorf("expected HTML to contain filter button %s", f)
		}
	}

	// Verify Custom Logo fields & tabs exist
	if !strings.Contains(html, `id="finalCustomLogo"`) {
		t.Errorf("expected HTML to contain finalCustomLogo input")
	}
	if !strings.Contains(html, `id="tabLogoUpload"`) || !strings.Contains(html, `id="tabLogoUrl"`) {
		t.Errorf("expected HTML to contain logo source selection tabs")
	}

	// Verify apiKey input has autocomplete="off"
	if !strings.Contains(html, `id="apiKey" class="form-control" placeholder="sk-..." autocomplete="off" required`) {
		t.Errorf("expected apiKey input to have autocomplete='off'")
	}

	// Verify DeepSeek and Gemini logos are non-empty valid Data URLs and embedded into HTML JavaScript
	if !strings.HasPrefix(deepseekLogoBase64, "data:image/svg+xml;base64,") {
		t.Errorf("expected deepseekLogoBase64 to be a valid data URI")
	}
	if len(deepseekLogoBase64) < 100 {
		t.Errorf("expected deepseekLogoBase64 to have non-trivial SVG content")
	}
	if len(geminiArgonLogoBase64) < 100 {
		t.Errorf("expected geminiArgonLogoBase64 to have non-trivial content")
	}
	if !strings.Contains(html, `const deepseekLogoBase64 = "`+deepseekLogoBase64+`";`) {
		t.Errorf("expected HTML to define deepseekLogoBase64 constant in JavaScript")
	}
	if !strings.Contains(html, `const geminiArgonLogoBase64 = "`+geminiArgonLogoBase64+`";`) {
		t.Errorf("expected HTML to define geminiArgonLogoBase64 constant in JavaScript")
	}
	if strings.Contains(html, "__DEEPSEEK_LOGO_BASE64__") || strings.Contains(html, "__GEMINI_ARGON_LOGO_BASE64__") {
		t.Errorf("expected HTML placeholders for logos to be fully replaced")
	}

	// Verify auth-files/refresh uses all=true in query/body
	if !strings.Contains(html, `/v0/management/auth-files/refresh?all=true`) {
		t.Errorf("expected HTML JavaScript to refresh auth files with all=true")
	}

	// Verify that 'otros' filter does not include deepseek or gemini
	if !strings.Contains(html, `!["opencode-go", "nvidia", "nan", "groq", "cerebras", "deepseek", "gemini"].includes(a.provider)`) {
		t.Errorf("expected 'otros' filter to exclude deepseek and gemini so they don't leak into 'otros'")
	}
}

func TestIsHubManagedFileSecurity(t *testing.T) {
	validFiles := []string{
		"deepseek-123456.json",
		"gemini-abcdef.json",
		"opencode-go-999.json",
		"nvidia-nim-123.json",
		"groq-456.json",
		"cerebras-789.json",
		"mistral-101.json",
		"nan-202.json",
		"openai-compat-303.json",
	}
	for _, f := range validFiles {
		if !isHubManagedFile(f) {
			t.Errorf("expected %q to be recognized as a valid hub managed file", f)
		}
	}

	// Path traversal / Malicious paths MUST be rejected
	maliciousFiles := []string{
		"../deepseek-123.json",
		"deepseek-123/../../secret.json",
		"subdir/deepseek-123.json",
		"deepseek-test/bar.json",
		"deepseek-test\\bar.json",
		"something.json",
		"config.yaml",
		"rotations.db",
		".cli-proxy-api.json",
		"deepseek-123.txt",
		"",
	}
	for _, f := range maliciousFiles {
		if isHubManagedFile(f) {
			t.Errorf("SECURITY RISK: expected %q to be rejected by isHubManagedFile", f)
		}
	}
}

func TestBackendAPIValidationAndErrors(t *testing.T) {
	tempAuth := t.TempDir()
	t.Setenv("CLIPROXY_AUTH_DIR", tempAuth)

	// 1. POST with key < 6 chars -> 400 Bad Request
	reqShortKey, _ := json.Marshal(ManagementRequestPayload{
		Method: "POST",
		Path:   "/v0/management/hub/accounts",
		Body:   []byte(`{"provider":"deepseek","api_key":"123"}`),
	})
	rawShort, _ := handlePluginMethod("management.handle", reqShortKey)
	var envShort envelope
	json.Unmarshal(rawShort, &envShort)
	var respShort ManagementResponsePayload
	json.Unmarshal(envShort.Result, &respShort)
	if respShort.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400 Bad Request for short key, got %d", respShort.StatusCode)
	}

	// 2. POST with empty body -> 400 Bad Request
	reqEmpty, _ := json.Marshal(ManagementRequestPayload{
		Method: "POST",
		Path:   "/v0/management/hub/accounts",
		Body:   []byte(`{}`),
	})
	rawEmpty, _ := handlePluginMethod("management.handle", reqEmpty)
	var envEmpty envelope
	json.Unmarshal(rawEmpty, &envEmpty)
	var respEmpty ManagementResponsePayload
	json.Unmarshal(envEmpty.Result, &respEmpty)
	if respEmpty.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400 Bad Request for empty body, got %d", respEmpty.StatusCode)
	}

	// 3. DELETE with empty ID -> 400 Bad Request
	reqDeleteEmpty, _ := json.Marshal(ManagementRequestPayload{
		Method: "DELETE",
		Path:   "/v0/management/hub/accounts",
		Body:   []byte(`{"id":""}`),
	})
	rawDelEmpty, _ := handlePluginMethod("management.handle", reqDeleteEmpty)
	var envDelEmpty envelope
	json.Unmarshal(rawDelEmpty, &envDelEmpty)
	var respDelEmpty ManagementResponsePayload
	json.Unmarshal(envDelEmpty.Result, &respDelEmpty)
	if respDelEmpty.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400 Bad Request for empty delete ID, got %d", respDelEmpty.StatusCode)
	}

	// 4. DELETE with path traversal ID -> 400 Bad Request
	reqDeleteTraversal, _ := json.Marshal(ManagementRequestPayload{
		Method: "DELETE",
		Path:   "/v0/management/hub/accounts",
		Body:   []byte(`{"id":"../deepseek-secret.json"}`),
	})
	rawDelTrav, _ := handlePluginMethod("management.handle", reqDeleteTraversal)
	var envDelTrav envelope
	json.Unmarshal(rawDelTrav, &envDelTrav)
	var respDelTrav ManagementResponsePayload
	json.Unmarshal(envDelTrav.Result, &respDelTrav)
	if respDelTrav.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400 Bad Request for path traversal delete ID, got %d", respDelTrav.StatusCode)
	}

	// 5. DELETE with non-hub ID -> 400 Bad Request
	reqDeleteNonHub, _ := json.Marshal(ManagementRequestPayload{
		Method: "DELETE",
		Path:   "/v0/management/hub/accounts",
		Body:   []byte(`{"id":"config.yaml"}`),
	})
	rawDelNonHub, _ := handlePluginMethod("management.handle", reqDeleteNonHub)
	var envDelNonHub envelope
	json.Unmarshal(rawDelNonHub, &envDelNonHub)
	var respDelNonHub ManagementResponsePayload
	json.Unmarshal(envDelNonHub.Result, &respDelNonHub)
	if respDelNonHub.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400 Bad Request for non-hub file, got %d", respDelNonHub.StatusCode)
	}

	// 6. POST openai-compat without base_url -> 400 Bad Request
	reqOpenAINoBase, _ := json.Marshal(ManagementRequestPayload{
		Method: "POST",
		Path:   "/v0/management/hub/accounts",
		Body:   []byte(`{"provider":"openai-compat","api_key":"valid-secret-key-12345"}`),
	})
	rawOpenAINoBase, _ := handlePluginMethod("management.handle", reqOpenAINoBase)
	var envOpenAINoBase envelope
	json.Unmarshal(rawOpenAINoBase, &envOpenAINoBase)
	var respOpenAINoBase ManagementResponsePayload
	json.Unmarshal(envOpenAINoBase.Result, &respOpenAINoBase)
	if respOpenAINoBase.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400 Bad Request for openai-compat without base_url, got %d", respOpenAINoBase.StatusCode)
	}
	bodyOpenAINoBase, _ := base64.StdEncoding.DecodeString(respOpenAINoBase.Body)
	if !strings.Contains(string(bodyOpenAINoBase), "Base URL") {
		t.Errorf("expected error message mentioning Base URL, got %s", string(bodyOpenAINoBase))
	}

	// 6. Invalid HTTP Method (PUT) -> 405 Method Not Allowed
	reqPut, _ := json.Marshal(ManagementRequestPayload{
		Method: "PUT",
		Path:   "/v0/management/hub/accounts",
		Body:   []byte(`{}`),
	})
	rawPut, _ := handlePluginMethod("management.handle", reqPut)
	var envPut envelope
	json.Unmarshal(rawPut, &envPut)
	var respPut ManagementResponsePayload
	json.Unmarshal(envPut.Result, &respPut)
	if respPut.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("expected 405 Method Not Allowed for PUT, got %d", respPut.StatusCode)
	}

	// 7. Unsupported plugin method
	rawUnsupported, _ := handlePluginMethod("unsupported.method", nil)
	var envUnsupported envelope
	json.Unmarshal(rawUnsupported, &envUnsupported)
	if envUnsupported.OK {
		t.Errorf("expected OK=false for unsupported plugin method")
	}
	if envUnsupported.Error == nil || envUnsupported.Error.Code != "unknown_method" {
		t.Errorf("expected unknown_method code, got %+v", envUnsupported.Error)
	}
}

func TestStorageAllProvidersAndKeyMasking(t *testing.T) {
	tempAuth := t.TempDir()
	t.Setenv("CLIPROXY_AUTH_DIR", tempAuth)

	type providerCase struct {
		provider     string
		apiKey       string
		label        string
		baseURL      string
		expectedPre  string
		expectedBase string
		expectedType string
	}

	cases := []providerCase{
		{
			provider:     "deepseek",
			apiKey:       "sk-deepseek-official-key-1234567890",
			label:        "DeepSeek Oficial",
			expectedPre:  "deepseek",
			expectedBase: "https://api.deepseek.com",
			expectedType: "openai-compatible",
		},
		{
			provider:     "gemini",
			apiKey:       "AIzaSy-gemini-argon-key-1234567890",
			label:        "Google Gemini 4 Argon",
			expectedPre:  "gemini",
			expectedBase: "https://generativelanguage.googleapis.com/v1beta/openai/",
			expectedType: "openai-compatible",
		},
		{
			provider:     "nvidia",
			apiKey:       "nvapi-nvidia-nim-key-1234567890",
			label:        "NVIDIA NIM",
			expectedPre:  "nim",
			expectedBase: "https://integrate.api.nvidia.com/v1",
			expectedType: "nvidia",
		},
		{
			provider:     "groq",
			apiKey:       "gsk_groq_cloud_key_1234567890",
			label:        "Groq Cloud",
			expectedPre:  "groq",
			expectedBase: "https://api.groq.com/openai/v1",
			expectedType: "openai-compatible",
		},
		{
			provider:     "cerebras",
			apiKey:       "csk-cerebras-key-1234567890",
			label:        "Cerebras AI",
			expectedPre:  "cere",
			expectedBase: "https://api.cerebras.ai/v1",
			expectedType: "openai-compatible",
		},
		{
			provider:     "mistral",
			apiKey:       "mistral_api_key_1234567890",
			label:        "Mistral AI",
			expectedPre:  "mistral",
			expectedBase: "https://api.mistral.ai/v1",
			expectedType: "openai-compatible",
		},
		{
			provider:     "nan",
			apiKey:       "nan_builders_key_1234567890",
			label:        "NaN Builders",
			expectedPre:  "nan",
			expectedBase: "https://api.nan.builders/v1",
			expectedType: "openai-compatible",
		},
		{
			provider:     "openai-compat",
			apiKey:       "custom_key_1234567890",
			label:        "Custom OpenAI Endpoint",
			baseURL:      "https://api.custom-gateway.com/v1",
			expectedPre:  "custom",
			expectedBase: "https://api.custom-gateway.com/v1",
			expectedType: "openai-compatible",
		},
		{
			provider:     "opencode-go",
			apiKey:       "oc_sk_opencode_go_key_1234567890",
			label:        "OpenCode Go Zen",
			expectedPre:  "ocgo",
			expectedBase: "https://opencode.ai/zen/go/v1",
			expectedType: "opencode-go",
		},
	}

	createdIDs := make([]string, 0, len(cases))

	for _, tc := range cases {
		pMap := map[string]any{
			"provider": tc.provider,
			"api_key":  tc.apiKey,
			"label":    tc.label,
		}
		if tc.baseURL != "" {
			pMap["base_url"] = tc.baseURL
		}
		payload, _ := json.Marshal(pMap)
		req, _ := json.Marshal(ManagementRequestPayload{
			Method: "POST",
			Path:   "/v0/management/hub/accounts",
			Body:   payload,
		})
		raw, err := handlePluginMethod("management.handle", req)
		if err != nil {
			t.Fatalf("unexpected error creating %s: %v", tc.provider, err)
		}
		var env envelope
		json.Unmarshal(raw, &env)
		var resp ManagementResponsePayload
		json.Unmarshal(env.Result, &resp)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("expected 200 creating %s, got %d", tc.provider, resp.StatusCode)
		}

		var created struct {
			OK bool   `json:"ok"`
			ID string `json:"id"`
		}
		bodyBytes, _ := base64.StdEncoding.DecodeString(resp.Body)
		json.Unmarshal(bodyBytes, &created)
		if !created.OK || created.ID == "" {
			t.Fatalf("expected valid ID for %s, got %+v", tc.provider, created)
		}
		createdIDs = append(createdIDs, created.ID)

		// Verify on-disk file
		diskPath := filepath.Join(tempAuth, created.ID)
		fileData, err := os.ReadFile(diskPath)
		if err != nil {
			t.Fatalf("expected file on disk %s, got err: %v", diskPath, err)
		}

		var stored map[string]any
		if err := json.Unmarshal(fileData, &stored); err != nil {
			t.Fatalf("stored file %s is not valid JSON: %v", diskPath, err)
		}

		if stored["type"] != tc.expectedType {
			t.Errorf("[%s] expected type %s, got %v", tc.provider, tc.expectedType, stored["type"])
		}
		if tc.expectedBase != "" && stored["base_url"] != tc.expectedBase {
			t.Errorf("[%s] expected base_url %s, got %v", tc.provider, tc.expectedBase, stored["base_url"])
		}
		if stored["prefix"] != tc.expectedPre {
			t.Errorf("[%s] expected prefix %s, got %v", tc.provider, tc.expectedPre, stored["prefix"])
		}
		if stored["api_key"] != tc.apiKey {
			t.Errorf("[%s] expected api_key to match input", tc.provider)
		}
		if stored["disabled"] != false {
			t.Errorf("[%s] expected disabled=false", tc.provider)
		}
	}

	// Add a non-managed file and a corrupt JSON file to verify tolerance
	os.WriteFile(filepath.Join(tempAuth, "random-notes.txt"), []byte("not a managed file"), 0644)
	os.WriteFile(filepath.Join(tempAuth, "deepseek-corrupt.json"), []byte("{invalid-json-content"), 0644)

	// Test GET all accounts
	reqList, _ := json.Marshal(ManagementRequestPayload{
		Method: "GET",
		Path:   "/v0/management/hub/accounts",
	})
	rawList, _ := handlePluginMethod("management.handle", reqList)
	var envList envelope
	json.Unmarshal(rawList, &envList)
	var respList ManagementResponsePayload
	json.Unmarshal(envList.Result, &respList)
	if respList.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 listing accounts, got %d", respList.StatusCode)
	}

	var listData struct {
		OK       bool          `json:"ok"`
		Accounts []accountItem `json:"accounts"`
	}
	bodyListBytes, _ := base64.StdEncoding.DecodeString(respList.Body)
	json.Unmarshal(bodyListBytes, &listData)

	if !listData.OK {
		t.Fatalf("expected ok=true in account listing")
	}
	if len(listData.Accounts) != len(cases) {
		t.Errorf("expected %d accounts listed, got %d", len(cases), len(listData.Accounts))
	}

	// Verify key masking on listed accounts
	for _, acc := range listData.Accounts {
		if !strings.Contains(acc.KeyMask, "...") && acc.KeyMask != "***" {
			t.Errorf("expected masked key for %s, got %s", acc.ID, acc.KeyMask)
		}
	}

	// Test POST account with custom logo
	customLogoData := "data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNk+M9QDwADhgGAWjR9awAAAABJRU5ErkJggg=="
	reqPostLogo, _ := json.Marshal(ManagementRequestPayload{
		Method: "POST",
		Path:   "/v0/management/hub/accounts",
		Body: []byte(`{
			"provider": "deepseek",
			"label": "DeepSeek Con Logo Custom",
			"api_key": "sk-custom-logo-test-12345",
			"custom_logo": "` + customLogoData + `"
		}`),
	})
	rawPostLogo, _ := handlePluginMethod("management.handle", reqPostLogo)
	var envPostLogo envelope
	json.Unmarshal(rawPostLogo, &envPostLogo)
	var respPostLogo ManagementResponsePayload
	json.Unmarshal(envPostLogo.Result, &respPostLogo)
	if respPostLogo.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 creating account with custom logo, got %d", respPostLogo.StatusCode)
	}

	var createdLogo struct {
		OK bool   `json:"ok"`
		ID string `json:"id"`
	}
	bodyLogoBytes, _ := base64.StdEncoding.DecodeString(respPostLogo.Body)
	json.Unmarshal(bodyLogoBytes, &createdLogo)

	// Verify on-disk file has custom_logo
	diskLogoPath := filepath.Join(tempAuth, createdLogo.ID)
	diskLogoContent, _ := os.ReadFile(diskLogoPath)
	var storedLogo map[string]any
	json.Unmarshal(diskLogoContent, &storedLogo)
	if storedLogo["custom_logo"] != customLogoData {
		t.Errorf("expected custom_logo stored on disk, got %v", storedLogo["custom_logo"])
	}
}
