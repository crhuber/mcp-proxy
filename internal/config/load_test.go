package config

import (
	"os"
	"path/filepath"
	"testing"
)

const sampleYAML = `
endpoints:
  - name: billing
    upstream:
      base_url: "${TEST_BILLING_BASE_URL}"
      auth:
        type: bearer
        env: TEST_BILLING_TOKEN
    tools:
      - name: getInvoice
        description: "Fetch an invoice."
        parameters:
          type: object
          required: [invoiceId]
          properties:
            invoiceId:
              type: string
              in: path
              description: "id, e.g. literally the text ${NOT_AN_ENV_VAR} should stay literal here"
        http:
          method: GET
          path: "/v1/invoices/{invoiceId}"
`

func writeTempConfig(t *testing.T, contents string) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(p, []byte(contents), 0o644); err != nil {
		t.Fatalf("writing temp config: %v", err)
	}
	return p
}

func TestLoadInterpolatesBaseURLAndSkipsParameters(t *testing.T) {
	t.Setenv("TEST_BILLING_BASE_URL", "https://billing.example.com")
	t.Setenv("TEST_BILLING_TOKEN", "secret-token")

	path := writeTempConfig(t, sampleYAML)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Endpoints[0].Upstream.BaseURL != "https://billing.example.com" {
		t.Fatalf("base_url not interpolated: %q", cfg.Endpoints[0].Upstream.BaseURL)
	}
	props := cfg.Endpoints[0].Tools[0].Parameters["properties"].(map[string]any)
	desc := props["invoiceId"].(map[string]any)["description"].(string)
	if desc != `id, e.g. literally the text ${NOT_AN_ENV_VAR} should stay literal here` {
		t.Fatalf("parameters description should not be env-interpolated, got %q", desc)
	}
}

func TestLoadFailsOnMissingEnvVar(t *testing.T) {
	t.Setenv("TEST_BILLING_TOKEN", "secret-token")
	// Deliberately leave TEST_BILLING_BASE_URL unset.
	path := writeTempConfig(t, sampleYAML)
	if _, err := Load(path); err == nil {
		t.Fatal("expected error for missing env var referenced by ${...}")
	}
}

func TestLoadFailsOnMissingFile(t *testing.T) {
	if _, err := Load("/nonexistent/path/config.yaml"); err == nil {
		t.Fatal("expected error for missing config file")
	}
}
