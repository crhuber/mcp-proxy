package config

import "testing"

func validConfig() *Config {
	return &Config{
		Endpoints: []EndpointConfig{
			{
				Name: "billing",
				Upstream: UpstreamConfig{
					BaseURL: "https://billing.example.com",
					Auth:    AuthConfig{Type: "none"},
				},
				Tools: []ToolConfig{
					{
						Name:        "getInvoice",
						Description: "Fetch an invoice.",
						Parameters: map[string]any{
							"type": "object",
							"required": []any{"invoiceId"},
							"properties": map[string]any{
								"invoiceId": map[string]any{"type": "string", "in": "path", "description": "id"},
							},
						},
						HTTP: HTTPConfig{
							Method: "GET",
							Path:   "/v1/invoices/{invoiceId}",
						},
					},
				},
			},
		},
	}
}

func TestValidateAcceptsValidConfig(t *testing.T) {
	if err := Validate(validConfig()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateRejectsMissingDescription(t *testing.T) {
	cfg := validConfig()
	cfg.Endpoints[0].Tools[0].Description = ""
	err := Validate(cfg)
	if err == nil {
		t.Fatal("expected error for missing description")
	}
}

func TestValidateRejectsBadMethod(t *testing.T) {
	cfg := validConfig()
	cfg.Endpoints[0].Tools[0].HTTP.Method = "FROBNICATE"
	if err := Validate(cfg); err == nil {
		t.Fatal("expected error for bad method")
	}
}

func TestValidateRejectsPathWithoutLeadingSlash(t *testing.T) {
	cfg := validConfig()
	cfg.Endpoints[0].Tools[0].HTTP.Path = "v1/invoices"
	if err := Validate(cfg); err == nil {
		t.Fatal("expected error for path without leading slash")
	}
}

func TestValidateRejectsUnmatchedPathToken(t *testing.T) {
	cfg := validConfig()
	cfg.Endpoints[0].Tools[0].HTTP.Path = "/v1/invoices/{invoiceId}/{extra}"
	if err := Validate(cfg); err == nil {
		t.Fatal("expected error for unmatched path token")
	}
}

func TestValidateRejectsPathParamNotInPath(t *testing.T) {
	cfg := validConfig()
	props := cfg.Endpoints[0].Tools[0].Parameters["properties"].(map[string]any)
	props["extra"] = map[string]any{"type": "string", "in": "path", "description": "x"}
	if err := Validate(cfg); err == nil {
		t.Fatal("expected error for in:path param missing from path template")
	}
}

func TestValidateRejectsHTTPRequestBodyOnGet(t *testing.T) {
	cfg := validConfig()
	cfg.Endpoints[0].Tools[0].HTTP.Body = map[string]any{"x": "literal"}
	if err := Validate(cfg); err == nil {
		t.Fatal("expected error for httpRequestBody on GET")
	}
}

func TestValidateRejectsUndeclaredHTTPRequestBodyReference(t *testing.T) {
	cfg := validConfig()
	cfg.Endpoints[0].Tools[0].HTTP.Method = "POST"
	cfg.Endpoints[0].Tools[0].HTTP.Body = map[string]any{"x": "{doesNotExist}"}
	if err := Validate(cfg); err == nil {
		t.Fatal("expected error for undeclared httpRequestBody reference")
	}
}

func TestValidateRejectsUnusedParameter(t *testing.T) {
	cfg := validConfig()
	props := cfg.Endpoints[0].Tools[0].Parameters["properties"].(map[string]any)
	// Add a parameter with no "in" tag and no httpRequestBody to reference it.
	props["orphan"] = map[string]any{"type": "string", "description": "unused"}
	if err := Validate(cfg); err == nil {
		t.Fatal("expected error for unused parameter")
	}
}

func TestValidateAllowsUnusedParameterWhenReferencedInBody(t *testing.T) {
	cfg := validConfig()
	cfg.Endpoints[0].Tools[0].HTTP.Method = "POST"
	props := cfg.Endpoints[0].Tools[0].Parameters["properties"].(map[string]any)
	props["notes"] = map[string]any{"type": "string", "description": "notes"}
	cfg.Endpoints[0].Tools[0].HTTP.Body = map[string]any{"notes": "{notes}"}
	if err := Validate(cfg); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateRejectsUnknownResponseFunction(t *testing.T) {
	cfg := validConfig()
	cfg.Endpoints[0].Tools[0].HTTP.Response = &ResponseConfig{
		Select: map[string]any{"x": "{frobnicate(a, 1)}"},
	}
	if err := Validate(cfg); err == nil {
		t.Fatal("expected error for unknown response function")
	}
}

func TestValidateRejectsUnknownAuthType(t *testing.T) {
	cfg := validConfig()
	cfg.Endpoints[0].Upstream.Auth = AuthConfig{Type: "magic"}
	if err := Validate(cfg); err == nil {
		t.Fatal("expected error for unknown auth type")
	}
}

func TestValidateRejectsBearerAuthWithoutEnv(t *testing.T) {
	cfg := validConfig()
	cfg.Endpoints[0].Upstream.Auth = AuthConfig{Type: "bearer"}
	if err := Validate(cfg); err == nil {
		t.Fatal("expected error for bearer auth without env")
	}
}

func TestValidateRejectsMissingEnvVar(t *testing.T) {
	cfg := validConfig()
	cfg.Endpoints[0].Upstream.Auth = AuthConfig{Type: "bearer", Env: "MCP_PROXY_TEST_DEFINITELY_UNSET_VAR"}
	if err := Validate(cfg); err == nil {
		t.Fatal("expected error for referenced-but-unset env var")
	}
}

func TestValidateAcceptsBearerAuthWithSetEnv(t *testing.T) {
	t.Setenv("MCP_PROXY_TEST_TOKEN", "secret")
	cfg := validConfig()
	cfg.Endpoints[0].Upstream.Auth = AuthConfig{Type: "bearer", Env: "MCP_PROXY_TEST_TOKEN"}
	if err := Validate(cfg); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateRejectsDuplicateFinalToolName(t *testing.T) {
	cfg := validConfig()
	cfg.Endpoints = append(cfg.Endpoints, EndpointConfig{
		Name: "billing",
		Upstream: UpstreamConfig{
			BaseURL: "https://billing2.example.com",
			Auth:    AuthConfig{Type: "none"},
		},
		Tools: []ToolConfig{
			{
				Name:        "getInvoice",
				Description: "dup",
				HTTP: HTTPConfig{
					Method: "GET",
					Path:   "/x",
				},
			},
		},
	})
	if err := Validate(cfg); err == nil {
		t.Fatal("expected error for duplicate upstream name (also triggers final-name collision)")
	}
}

func TestValidateRejectsBadBaseURL(t *testing.T) {
	cfg := validConfig()
	cfg.Endpoints[0].Upstream.BaseURL = "not-a-url"
	if err := Validate(cfg); err == nil {
		t.Fatal("expected error for invalid base_url")
	}
}

func TestValidateRejectsBadTimeout(t *testing.T) {
	cfg := validConfig()
	cfg.Endpoints[0].Upstream.Timeout = "not-a-duration"
	if err := Validate(cfg); err == nil {
		t.Fatal("expected error for invalid timeout")
	}
}

func TestValidateRejectsArrayTypedPathParam(t *testing.T) {
	cfg := validConfig()
	props := cfg.Endpoints[0].Tools[0].Parameters["properties"].(map[string]any)
	props["invoiceId"] = map[string]any{"type": "array", "in": "path", "description": "bad"}
	if err := Validate(cfg); err == nil {
		t.Fatal("expected error for array-typed in:path property")
	}
}
