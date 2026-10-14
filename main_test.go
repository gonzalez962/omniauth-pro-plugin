package main

import (
	"encoding/base64"
	"encoding/json"
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
