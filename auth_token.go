package connectplugin

import (
	"context"
	"fmt"
	"strings"

	"connectrpc.com/connect"
)

// TokenAuth implements token-based authentication (Bearer tokens, API keys).
type TokenAuth struct {
	// Token to send in client requests (for client-side)
	Token string

	// ValidateToken validates incoming tokens (for server-side)
	// Returns identity and claims if valid, error if invalid.
	ValidateToken func(token string) (identity string, claims map[string]string, err error)

	// Header is the header name for the token.
	// Default: "Authorization"
	Header string

	// Prefix is the token prefix (e.g., "Bearer ").
	// Default: "Bearer "
	Prefix string
}

// NewTokenAuth creates a token auth provider.
// For client-side: provide token
// For server-side: provide validateToken function
func NewTokenAuth(token string, validateToken func(string) (string, map[string]string, error)) *TokenAuth {
	return &TokenAuth{
		Token:         token,
		ValidateToken: validateToken,
		Header:        "Authorization",
		Prefix:        "Bearer ",
	}
}

// ClientInterceptor returns an interceptor that adds the token to outgoing requests.
func (t *TokenAuth) ClientInterceptor() connect.UnaryInterceptorFunc {
	return func(next connect.UnaryFunc) connect.UnaryFunc {
		return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
			if t.Token != "" {
				req.Header().Set(t.Header, t.Prefix+t.Token)
			}
			return next(ctx, req)
		}
	}
}

// ServerInterceptor returns an interceptor that validates incoming tokens.
func (t *TokenAuth) ServerInterceptor() connect.UnaryInterceptorFunc {
	return func(next connect.UnaryFunc) connect.UnaryFunc {
		return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
			// Extract token from header
			authHeader := req.Header().Get(t.Header)
			if authHeader == "" {
				return nil, connect.NewError(connect.CodeUnauthenticated,
					fmt.Errorf("missing %s header", t.Header))
			}

			// Strip prefix
			token := authHeader
			if t.Prefix != "" {
				if !strings.HasPrefix(authHeader, t.Prefix) {
					return nil, connect.NewError(connect.CodeUnauthenticated,
						fmt.Errorf("invalid token format, expected %s prefix", t.Prefix))
				}
				token = strings.TrimPrefix(authHeader, t.Prefix)
			}

			// Validate token
			if t.ValidateToken == nil {
				return nil, connect.NewError(connect.CodeInternal,
					fmt.Errorf("token validator not configured"))
			}

			identity, claims, err := t.ValidateToken(token)
			if err != nil {
				return nil, connect.NewError(connect.CodeUnauthenticated,
					fmt.Errorf("invalid token: %w", err))
			}

			// Store auth context
			authCtx := &AuthContext{
				Identity: identity,
				Claims:   claims,
				Provider: "token",
			}
			ctx = WithAuthContext(ctx, authCtx)

			return next(ctx, req)
		}
	}
}

// APIKeyAuth is a simplified token auth for API keys (no "Bearer " prefix).
type APIKeyAuth struct {
	*TokenAuth
}

// NewAPIKeyAuth creates an API key auth provider.
// Uses X-API-Key header by default (no prefix).
func NewAPIKeyAuth(apiKey string, validateKey func(string) (string, map[string]string, error)) *APIKeyAuth {
	return &APIKeyAuth{
		TokenAuth: &TokenAuth{
			Token:         apiKey,
			ValidateToken: validateKey,
			Header:        "X-API-Key",
			Prefix:        "", // No prefix for API keys
		},
	}
}
