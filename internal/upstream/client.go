package upstream

import (
	"net/http"
	"time"
)

// NewClient returns the single *http.Client shared by every upstream call.
// It deliberately does not set Client.Timeout — the only per-call timeout
// mechanism is context.WithTimeout in the tool handler, so there is exactly
// one timeout source to reason about.
func NewClient() *http.Client {
	transport := &http.Transport{
		MaxIdleConns:        100,
		MaxIdleConnsPerHost: 20,
		IdleConnTimeout:     90 * time.Second,
	}
	return &http.Client{Transport: transport}
}
