package upstream

import "strings"

// Redactor scrubs known secret values out of any string before it leaves
// the process (tool-error messages, structured error fields, log lines). A
// single instance is built at boot from every resolved secret (the proxy's
// own bearer token plus every upstream's auth secret) and shared by every
// Upstream.
type Redactor struct {
	secrets []string
}

// NewRedactor builds a Redactor from the given secrets. Empty strings are
// ignored so an unset/none-auth upstream never accidentally causes every
// empty string to be "redacted".
func NewRedactor(secrets ...string) *Redactor {
	r := &Redactor{}
	for _, s := range secrets {
		if s != "" {
			r.secrets = append(r.secrets, s)
		}
	}
	return r
}

// Redact returns s with every known secret substring replaced. A nil
// Redactor is valid and simply performs no substitution, so callers never
// need a nil check.
func (r *Redactor) Redact(s string) string {
	if r == nil {
		return s
	}
	for _, secret := range r.secrets {
		s = strings.ReplaceAll(s, secret, "***REDACTED***")
	}
	return s
}
