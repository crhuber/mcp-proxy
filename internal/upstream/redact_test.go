package upstream

import (
	"strings"
	"testing"
)

func TestRedactorScrubsKnownSecrets(t *testing.T) {
	r := NewRedactor("s3cr3t", "hdr-secret")
	got := r.Redact(`error calling upstream: token s3cr3t rejected, also saw hdr-secret in header`)
	if strings.Contains(got, "s3cr3t") {
		t.Errorf("secret leaked into redacted output: %q", got)
	}
	if strings.Contains(got, "hdr-secret") {
		t.Errorf("secret leaked into redacted output: %q", got)
	}
	if !strings.Contains(got, "***REDACTED***") {
		t.Errorf("expected redaction marker in output: %q", got)
	}
}

func TestRedactorIgnoresEmptySecrets(t *testing.T) {
	r := NewRedactor("", "real-secret")
	got := r.Redact("some ordinary text")
	if got != "some ordinary text" {
		t.Errorf("empty secret should never match/redact arbitrary text, got %q", got)
	}
}

func TestNilRedactorIsNoOp(t *testing.T) {
	var r *Redactor
	got := r.Redact("some text with a secret in it")
	if got != "some text with a secret in it" {
		t.Errorf("nil Redactor should pass text through unchanged, got %q", got)
	}
}
