package server

import (
	"net/http"

	"github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// BuildHandler wraps mcpServer in a stateless StreamableHTTPHandler mounted
// at /mcp, plus an unauthenticated /healthz for liveness probes. Stateless
// mode is correct here: the proxy never needs a server-initiated request
// back to the client (no sampling/elicitation use case), so there's no
// session-affinity requirement.
//
// If bearerVerifier is non-nil, /mcp is wrapped with bearer-token auth;
// /healthz is never protected, so orchestrators can probe liveness without a
// token.
func BuildHandler(mcpServer *mcp.Server, bearerVerifier auth.TokenVerifier) http.Handler {
	mcpHandler := mcp.NewStreamableHTTPHandler(
		func(*http.Request) *mcp.Server { return mcpServer },
		&mcp.StreamableHTTPOptions{
			Stateless:                    true,
			PropagateRequestCancellation: true,
		},
	)

	var mcpRoute http.Handler = mcpHandler
	if bearerVerifier != nil {
		// AllowMissingExpiration is required: a static shared-secret token
		// has no expiration, and without this the middleware rejects every
		// request regardless of whether the token is correct.
		mw := auth.RequireBearerToken(bearerVerifier, &auth.RequireBearerTokenOptions{AllowMissingExpiration: true})
		mcpRoute = mw(mcpHandler)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	mux.Handle("/mcp", mcpRoute)
	return mux
}
