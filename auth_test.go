package connectplugin

import (
	"context"
	"errors"
	"testing"

	"connectrpc.com/connect"
)

func TestAuthContext(t *testing.T) {
	ctx := context.Background()

	// No auth context initially
	auth := GetAuthContext(ctx)
	if auth != nil {
		t.Error("Expected nil auth context")
	}

	// Add auth context
	authCtx := &AuthContext{
		Identity: "user-123",
		Provider: "token",
		Claims: map[string]string{
			"role":  "admin",
			"scope": "read:write",
		},
	}

	ctx = WithAuthContext(ctx, authCtx)

	// Retrieve auth context
	retrieved := GetAuthContext(ctx)
	if retrieved == nil {
		t.Fatal("Expected auth context to be set")
	}

	if retrieved.Identity != "user-123" {
		t.Errorf("Expected identity user-123, got %s", retrieved.Identity)
	}

	if retrieved.Provider != "token" {
		t.Errorf("Expected provider token, got %s", retrieved.Provider)
	}

	if retrieved.Claims["role"] != "admin" {
		t.Errorf("Expected role admin, got %s", retrieved.Claims["role"])
	}
}

func TestRequireAuth_Authenticated(t *testing.T) {
	interceptor := RequireAuth()

	authCtx := &AuthContext{
		Identity: "user-456",
		Provider: "token",
	}
	ctx := WithAuthContext(context.Background(), authCtx)

	called := false
	wrapped := interceptor(func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
		called = true
		return &connect.Response[string]{}, nil
	})

	_, err := wrapped(ctx, &connect.Request[string]{})
	if err != nil {
		t.Fatalf("Expected success with auth context, got error: %v", err)
	}

	if !called {
		t.Error("Expected next function to be called")
	}
}

func TestRequireAuth_Unauthenticated(t *testing.T) {
	interceptor := RequireAuth()

	ctx := context.Background() // No auth context

	wrapped := interceptor(func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
		t.Error("Should not call next function without auth")
		return nil, nil
	})

	_, err := wrapped(ctx, &connect.Request[string]{})
	if err == nil {
		t.Fatal("Expected error without auth context")
	}

	if connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Errorf("Expected Unauthenticated error, got %v", connect.CodeOf(err))
	}
}

func TestTokenAuth_ClientInterceptor(t *testing.T) {
	auth := NewTokenAuth("secret-token-123", nil)

	interceptor := auth.ClientInterceptor()

	wrapped := interceptor(func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
		// Verify token was added to header
		authHeader := req.Header().Get("Authorization")
		expected := "Bearer secret-token-123"
		if authHeader != expected {
			t.Errorf("Expected Authorization header %q, got %q", expected, authHeader)
		}
		return &connect.Response[string]{}, nil
	})

	_, err := wrapped(context.Background(), &connect.Request[string]{})
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
}

func TestTokenAuth_ServerInterceptor_Valid(t *testing.T) {
	validateToken := func(token string) (string, map[string]string, error) {
		if token == "valid-token" {
			return "user-789", map[string]string{"role": "admin"}, nil
		}
		return "", nil, errors.New("invalid token")
	}

	auth := NewTokenAuth("", validateToken)
	interceptor := auth.ServerInterceptor()

	// Create request with valid token
	req := &connect.Request[string]{}
	req.Header().Set("Authorization", "Bearer valid-token")

	wrapped := interceptor(func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
		// Verify auth context was set
		authCtx := GetAuthContext(ctx)
		if authCtx == nil {
			t.Fatal("Expected auth context to be set")
		}

		if authCtx.Identity != "user-789" {
			t.Errorf("Expected identity user-789, got %s", authCtx.Identity)
		}

		if authCtx.Claims["role"] != "admin" {
			t.Errorf("Expected role admin, got %s", authCtx.Claims["role"])
		}

		if authCtx.Provider != "token" {
			t.Errorf("Expected provider token, got %s", authCtx.Provider)
		}

		return &connect.Response[string]{}, nil
	})

	_, err := wrapped(context.Background(), req)
	if err != nil {
		t.Fatalf("Expected success with valid token, got error: %v", err)
	}
}

func TestTokenAuth_ServerInterceptor_Invalid(t *testing.T) {
	validateToken := func(token string) (string, map[string]string, error) {
		if token == "valid-token" {
			return "user-789", nil, nil
		}
		return "", nil, errors.New("invalid token")
	}

	auth := NewTokenAuth("", validateToken)
	interceptor := auth.ServerInterceptor()

	// Create request with invalid token
	req := &connect.Request[string]{}
	req.Header().Set("Authorization", "Bearer invalid-token")

	wrapped := interceptor(func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
		t.Error("Should not call next function with invalid token")
		return nil, nil
	})

	_, err := wrapped(context.Background(), req)
	if err == nil {
		t.Fatal("Expected error with invalid token")
	}

	if connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Errorf("Expected Unauthenticated error, got %v", connect.CodeOf(err))
	}
}

func TestTokenAuth_ServerInterceptor_MissingHeader(t *testing.T) {
	auth := NewTokenAuth("", func(token string) (string, map[string]string, error) {
		return "user", nil, nil
	})

	interceptor := auth.ServerInterceptor()

	// Request without Authorization header
	req := &connect.Request[string]{}

	wrapped := interceptor(func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
		t.Error("Should not call next function without auth header")
		return nil, nil
	})

	_, err := wrapped(context.Background(), req)
	if err == nil {
		t.Fatal("Expected error without auth header")
	}

	if connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Errorf("Expected Unauthenticated error, got %v", connect.CodeOf(err))
	}
}

func TestTokenAuth_ServerInterceptor_WrongPrefix(t *testing.T) {
	auth := NewTokenAuth("", func(token string) (string, map[string]string, error) {
		return "user", nil, nil
	})

	interceptor := auth.ServerInterceptor()

	// Request with wrong prefix
	req := &connect.Request[string]{}
	req.Header().Set("Authorization", "Basic some-token")

	wrapped := interceptor(func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
		t.Error("Should not call next function with wrong prefix")
		return nil, nil
	})

	_, err := wrapped(context.Background(), req)
	if err == nil {
		t.Fatal("Expected error with wrong prefix")
	}

	if connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Errorf("Expected Unauthenticated error, got %v", connect.CodeOf(err))
	}
}

func TestAPIKeyAuth(t *testing.T) {
	validateKey := func(key string) (string, map[string]string, error) {
		if key == "valid-api-key" {
			return "service-account", map[string]string{"app": "test"}, nil
		}
		return "", nil, errors.New("invalid key")
	}

	auth := NewAPIKeyAuth("valid-api-key", validateKey)

	// Test client interceptor
	clientInterceptor := auth.ClientInterceptor()
	clientWrapped := clientInterceptor(func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
		// Verify API key was added with correct header (no Bearer prefix)
		apiKey := req.Header().Get("X-API-Key")
		if apiKey != "valid-api-key" {
			t.Errorf("Expected X-API-Key header with value, got %q", apiKey)
		}
		return &connect.Response[string]{}, nil
	})

	_, err := clientWrapped(context.Background(), &connect.Request[string]{})
	if err != nil {
		t.Fatalf("Client interceptor error: %v", err)
	}

	// Test server interceptor
	serverInterceptor := auth.ServerInterceptor()
	req := &connect.Request[string]{}
	req.Header().Set("X-API-Key", "valid-api-key")

	serverWrapped := serverInterceptor(func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
		authCtx := GetAuthContext(ctx)
		if authCtx == nil {
			t.Fatal("Expected auth context")
		}

		if authCtx.Identity != "service-account" {
			t.Errorf("Expected identity service-account, got %s", authCtx.Identity)
		}

		return &connect.Response[string]{}, nil
	})

	_, err = serverWrapped(context.Background(), req)
	if err != nil {
		t.Fatalf("Server interceptor error: %v", err)
	}
}

func TestComposeAuthClient_MultipleProviders(t *testing.T) {
	// Compose two auth providers
	token1 := NewTokenAuth("token-1", nil)
	token2 := NewAPIKeyAuth("api-key-2", nil)

	composed := ComposeAuthClient(token1, token2)

	wrapped := composed(func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
		// Both headers should be present
		if req.Header().Get("Authorization") != "Bearer token-1" {
			t.Errorf("Expected Authorization header from token1")
		}

		if req.Header().Get("X-API-Key") != "api-key-2" {
			t.Errorf("Expected X-API-Key header from token2")
		}

		return &connect.Response[string]{}, nil
	})

	_, err := wrapped(context.Background(), &connect.Request[string]{})
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
}

func TestComposeAuthServer_FirstProviderWins(t *testing.T) {
	// First provider validates successfully
	provider1 := NewTokenAuth("", func(token string) (string, map[string]string, error) {
		if token == "token-1" {
			return "user-from-provider1", nil, nil
		}
		return "", nil, errors.New("invalid")
	})

	// Second provider would also validate, but shouldn't be called
	provider2 := NewAPIKeyAuth("", func(key string) (string, map[string]string, error) {
		t.Error("Second provider should not be called when first succeeds")
		return "user-from-provider2", nil, nil
	})

	composed := ComposeAuthServer(provider1, provider2)

	req := &connect.Request[string]{}
	req.Header().Set("Authorization", "Bearer token-1")

	wrapped := composed(func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
		authCtx := GetAuthContext(ctx)
		if authCtx == nil {
			t.Fatal("Expected auth context")
		}

		if authCtx.Identity != "user-from-provider1" {
			t.Errorf("Expected identity from provider1, got %s", authCtx.Identity)
		}

		return &connect.Response[string]{}, nil
	})

	_, err := wrapped(context.Background(), req)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
}

func TestComposeAuthServer_FallbackToSecond(t *testing.T) {
	// First provider fails
	provider1 := NewTokenAuth("", func(token string) (string, map[string]string, error) {
		return "", nil, errors.New("token not valid")
	})

	// Second provider succeeds
	provider2 := NewAPIKeyAuth("", func(key string) (string, map[string]string, error) {
		if key == "valid-key" {
			return "user-from-provider2", nil, nil
		}
		return "", nil, errors.New("invalid key")
	})

	composed := ComposeAuthServer(provider1, provider2)

	req := &connect.Request[string]{}
	req.Header().Set("Authorization", "Bearer invalid-token")
	req.Header().Set("X-API-Key", "valid-key")

	wrapped := composed(func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
		authCtx := GetAuthContext(ctx)
		if authCtx == nil {
			t.Fatal("Expected auth context from provider2")
		}

		if authCtx.Identity != "user-from-provider2" {
			t.Errorf("Expected identity from provider2, got %s", authCtx.Identity)
		}

		return &connect.Response[string]{}, nil
	})

	_, err := wrapped(context.Background(), req)
	if err != nil {
		t.Fatalf("Expected success from provider2, got error: %v", err)
	}
}

func TestComposeAuthServer_AllProvidersFail(t *testing.T) {
	provider1 := NewTokenAuth("", func(token string) (string, map[string]string, error) {
		return "", nil, errors.New("invalid token")
	})

	provider2 := NewAPIKeyAuth("", func(key string) (string, map[string]string, error) {
		return "", nil, errors.New("invalid key")
	})

	composed := ComposeAuthServer(provider1, provider2)

	req := &connect.Request[string]{}
	req.Header().Set("Authorization", "Bearer bad-token")
	req.Header().Set("X-API-Key", "bad-key")

	wrapped := composed(func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
		t.Error("Should not call next function when all auth providers fail")
		return nil, nil
	})

	_, err := wrapped(context.Background(), req)
	if err == nil {
		t.Fatal("Expected error when all providers fail")
	}

	if connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Errorf("Expected Unauthenticated error, got %v", connect.CodeOf(err))
	}
}

func TestTokenAuth_CustomHeaderAndPrefix(t *testing.T) {
	auth := &TokenAuth{
		Token:  "my-custom-token",
		Header: "X-Custom-Auth",
		Prefix: "Token ",
	}

	interceptor := auth.ClientInterceptor()

	wrapped := interceptor(func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
		header := req.Header().Get("X-Custom-Auth")
		expected := "Token my-custom-token"
		if header != expected {
			t.Errorf("Expected custom header %q, got %q", expected, header)
		}
		return &connect.Response[string]{}, nil
	})

	_, err := wrapped(context.Background(), &connect.Request[string]{})
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
}

func TestComposeAuthClient_NoProviders(t *testing.T) {
	// Empty composition should be a no-op
	composed := ComposeAuthClient()

	called := false
	wrapped := composed(func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
		called = true
		return &connect.Response[string]{}, nil
	})

	_, err := wrapped(context.Background(), &connect.Request[string]{})
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	if !called {
		t.Error("Expected function to be called with no-op composition")
	}
}

func TestComposeAuthServer_NoProviders(t *testing.T) {
	// Empty composition should pass through
	composed := ComposeAuthServer()

	called := false
	wrapped := composed(func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
		called = true
		return &connect.Response[string]{}, nil
	})

	_, err := wrapped(context.Background(), &connect.Request[string]{})
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	if !called {
		t.Error("Expected function to be called with no-op composition")
	}
}
