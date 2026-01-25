package connectplugin

import (
	"context"
	"fmt"

	"connectrpc.com/connect"
)

// AuthProvider is the interface for authentication mechanisms.
// Implementations include token-based auth, mTLS, etc.
type AuthProvider interface {
	// ClientInterceptor returns an interceptor for authenticating outgoing client requests.
	// The interceptor should add credentials to the request.
	ClientInterceptor() connect.UnaryInterceptorFunc

	// ServerInterceptor returns an interceptor for validating incoming server requests.
	// The interceptor should validate credentials and populate auth context.
	ServerInterceptor() connect.UnaryInterceptorFunc
}

// AuthContext is stored in context.Context for authenticated requests.
// Contains identity and claims from the authentication provider.
type AuthContext struct {
	// Identity is the authenticated principal (user ID, service account, etc.)
	Identity string

	// Claims contains additional authenticated attributes.
	Claims map[string]string

	// Provider indicates which auth mechanism was used.
	Provider string
}

type authContextKey struct{}

// WithAuthContext stores auth context in the context.
func WithAuthContext(ctx context.Context, auth *AuthContext) context.Context {
	return context.WithValue(ctx, authContextKey{}, auth)
}

// GetAuthContext retrieves auth context from the context.
// Returns nil if no auth context is present.
func GetAuthContext(ctx context.Context) *AuthContext {
	auth, _ := ctx.Value(authContextKey{}).(*AuthContext)
	return auth
}

// RequireAuth returns an interceptor that rejects unauthenticated requests.
// Should be composed with an AuthProvider's ServerInterceptor.
func RequireAuth() connect.UnaryInterceptorFunc {
	return func(next connect.UnaryFunc) connect.UnaryFunc {
		return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
			auth := GetAuthContext(ctx)
			if auth == nil {
				return nil, connect.NewError(connect.CodeUnauthenticated,
					fmt.Errorf("authentication required"))
			}
			return next(ctx, req)
		}
	}
}

// ComposeAuth chains multiple auth providers for the client.
// Each provider's interceptor is applied in order.
func ComposeAuthClient(providers ...AuthProvider) connect.UnaryInterceptorFunc {
	if len(providers) == 0 {
		return func(next connect.UnaryFunc) connect.UnaryFunc {
			return next
		}
	}

	return func(next connect.UnaryFunc) connect.UnaryFunc {
		// Apply interceptors in reverse order (last provider wraps innermost)
		for i := len(providers) - 1; i >= 0; i-- {
			next = providers[i].ClientInterceptor()(next)
		}
		return next
	}
}

// ComposeAuthServer chains multiple auth providers for the server.
// First provider that succeeds authenticates the request.
func ComposeAuthServer(providers ...AuthProvider) connect.UnaryInterceptorFunc {
	if len(providers) == 0 {
		return func(next connect.UnaryFunc) connect.UnaryFunc {
			return next
		}
	}

	return func(next connect.UnaryFunc) connect.UnaryFunc {
		return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
			// Try each provider in order
			// First provider that sets auth context wins
			var lastErr error

			for _, provider := range providers {
				wrapped := provider.ServerInterceptor()(func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
					// Just check if auth context was set
					if GetAuthContext(ctx) != nil {
						// Auth succeeded, continue with this context
						return next(ctx, req)
					}
					return nil, connect.NewError(connect.CodeUnauthenticated, fmt.Errorf("no auth context"))
				})

				resp, err := wrapped(ctx, req)
				if err == nil {
					return resp, nil
				}
				lastErr = err
			}

			// All providers failed
			if lastErr != nil {
				return nil, lastErr
			}
			return nil, connect.NewError(connect.CodeUnauthenticated, fmt.Errorf("authentication failed"))
		}
	}
}
