package proxyauth

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/auth"
)

func protectedHandler(verifier auth.TokenVerifier, opts *auth.RequireBearerTokenOptions) http.Handler {
	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	return auth.RequireBearerToken(verifier, opts)(inner)
}

// TestStaticVerifierRequiresAllowMissingExpiration is a regression test for
// the go-sdk gotcha the plan called out: a static bearer token has no
// Expiration, so RequireBearerToken rejects EVERY request unless
// AllowMissingExpiration is set — even with the correct token.
func TestStaticVerifierRequiresAllowMissingExpiration(t *testing.T) {
	verifier := NewStaticBearerVerifier("correct-token")
	handler := protectedHandler(verifier, &auth.RequireBearerTokenOptions{ /* AllowMissingExpiration NOT set */ })

	req := httptest.NewRequest(http.MethodGet, "/mcp", nil)
	req.Header.Set("Authorization", "Bearer correct-token")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected the CORRECT token to still be rejected without AllowMissingExpiration (demonstrating the gotcha), got status %d", rec.Code)
	}
}

func TestStaticVerifierAcceptsCorrectTokenWithAllowMissingExpiration(t *testing.T) {
	verifier := NewStaticBearerVerifier("correct-token")
	handler := protectedHandler(verifier, &auth.RequireBearerTokenOptions{AllowMissingExpiration: true})

	req := httptest.NewRequest(http.MethodGet, "/mcp", nil)
	req.Header.Set("Authorization", "Bearer correct-token")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for the correct token with AllowMissingExpiration set, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestStaticVerifierRejectsWrongToken(t *testing.T) {
	verifier := NewStaticBearerVerifier("correct-token")
	handler := protectedHandler(verifier, &auth.RequireBearerTokenOptions{AllowMissingExpiration: true})

	req := httptest.NewRequest(http.MethodGet, "/mcp", nil)
	req.Header.Set("Authorization", "Bearer wrong-token")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for a wrong token, got %d", rec.Code)
	}
}

func TestStaticVerifierRejectsMissingToken(t *testing.T) {
	verifier := NewStaticBearerVerifier("correct-token")
	handler := protectedHandler(verifier, &auth.RequireBearerTokenOptions{AllowMissingExpiration: true})

	req := httptest.NewRequest(http.MethodGet, "/mcp", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 when no Authorization header is present, got %d", rec.Code)
	}
}
