// Package proxyauth provides the bearer-token verifier that optionally
// protects the proxy's own MCP HTTP endpoint (independent of each upstream's
// own auth, which lives in internal/upstream).
package proxyauth

import (
	"context"
	"crypto/subtle"
	"net/http"

	"github.com/modelcontextprotocol/go-sdk/auth"
)

// NewStaticBearerVerifier returns an auth.TokenVerifier that accepts only
// one statically-configured token, compared in constant time.
//
// Pair this with auth.RequireBearerTokenOptions{AllowMissingExpiration:
// true}: a static shared-secret token has no expiration, and the SDK's
// middleware rejects any TokenInfo with a zero Expiration unless that option
// is set — forgetting it means every single request gets rejected with
// "token missing expiration".
func NewStaticBearerVerifier(token string) auth.TokenVerifier {
	expected := []byte(token)
	return func(ctx context.Context, presented string, req *http.Request) (*auth.TokenInfo, error) {
		if subtle.ConstantTimeCompare([]byte(presented), expected) != 1 {
			return nil, auth.ErrInvalidToken
		}
		return &auth.TokenInfo{}, nil
	}
}
