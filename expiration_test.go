package connectplugin

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"connectrpc.com/connect"
	connectpluginv1 "github.com/masegraye/connect-plugin-go/gen/plugin/v1"
)

// =============================================================================
// TOKEN EXPIRATION TESTS - R2.2
// =============================================================================

// TestTokenExpiration_RuntimeToken verifies runtime tokens expire correctly.
func TestTokenExpiration_RuntimeToken(t *testing.T) {
	h := NewHandshakeServer(&ServeConfig{})

	// Register token with 500ms TTL
	runtimeID := "test-runtime"
	token := "test-token-12345"
	h.mu.Lock()
	now := time.Now()
	h.tokens[runtimeID] = &tokenInfo{
		token:     token,
		issuedAt:  now,
		expiresAt: now.Add(500 * time.Millisecond),
	}
	h.mu.Unlock()

	// Should be valid immediately
	if !h.ValidateToken(runtimeID, token) {
		t.Error("Token should be valid immediately after issue")
	}

	// Wait for expiration
	time.Sleep(600 * time.Millisecond)

	// Should be invalid after expiration
	if h.ValidateToken(runtimeID, token) {
		t.Error("Token should be invalid after expiration")
	}

	// Verify lazy cleanup removed token
	h.mu.Lock()
	_, exists := h.tokens[runtimeID]
	h.mu.Unlock()
	if exists {
		t.Error("Expired token should be cleaned up by lazy cleanup")
	}
}

// TestTokenExpiration_CapabilityGrant verifies capability grants expire correctly.
func TestTokenExpiration_CapabilityGrant(t *testing.T) {
	broker := NewCapabilityBroker("http://localhost:8080")

	// Create expired grant (expired 1 hour ago)
	grantID := "test-grant"
	token := "test-token-12345"
	broker.mu.Lock()
	now := time.Now()
	broker.grants[grantID] = &grantInfo{
		grantID:        grantID,
		capabilityType: "logger",
		token:          token,
		handler:        nil, // Not needed for this test
		issuedAt:       now.Add(-2 * time.Hour),
		expiresAt:      now.Add(-1 * time.Hour), // Expired 1 hour ago
	}
	broker.mu.Unlock()

	// Create request with expired grant
	req := httptest.NewRequest("POST", "/capabilities/logger/"+grantID+"/Log", nil)
	req.Header.Set("Authorization", "Bearer "+token)

	w := httptest.NewRecorder()
	broker.handleCapabilityRequest(w, req)

	// Should return 401 Unauthorized
	if w.Code != http.StatusUnauthorized {
		t.Errorf("Expected 401 for expired grant, got %d", w.Code)
	}

	// Verify response mentions expiration
	if w.Body.String() != "grant expired\n" {
		t.Errorf("Expected 'grant expired' message, got: %s", w.Body.String())
	}

	// Verify grant was cleaned up
	broker.mu.Lock()
	_, exists := broker.grants[grantID]
	broker.mu.Unlock()
	if exists {
		t.Error("Expired grant should be cleaned up by lazy cleanup")
	}
}

// TestTokenExpiration_ValidTokenNotExpired verifies valid tokens are not rejected.
func TestTokenExpiration_ValidTokenNotExpired(t *testing.T) {
	h := NewHandshakeServer(&ServeConfig{})

	// Register token with 24-hour TTL (default)
	runtimeID := "test-runtime"
	token := "valid-token-12345"
	registerTestToken(h, runtimeID, token)

	// Should be valid
	if !h.ValidateToken(runtimeID, token) {
		t.Error("Valid token should not be rejected")
	}

	// Verify token still exists (not cleaned up)
	h.mu.Lock()
	_, exists := h.tokens[runtimeID]
	h.mu.Unlock()
	if !exists {
		t.Error("Valid token should not be cleaned up")
	}
}

// TestTokenExpiration_CustomTTL verifies custom TTL configuration works.
func TestTokenExpiration_CustomTTL(t *testing.T) {
	cfg := &ServeConfig{
		RuntimeTokenTTL: 100 * time.Millisecond, // Very short TTL for testing
		Plugins:         PluginSet{},
		Impls:           map[string]any{},
	}
	h := NewHandshakeServer(cfg)

	// Perform handshake to generate token with custom TTL
	req := connect.NewRequest(&connectpluginv1.HandshakeRequest{
		CoreProtocolVersion: 1,
		AppProtocolVersion:  1,
		MagicCookieKey:      DefaultMagicCookieKey,
		MagicCookieValue:    DefaultMagicCookieValue,
		SelfId:              "test-plugin",
	})

	resp, err := h.Handshake(context.Background(), req)
	if err != nil {
		t.Fatalf("Handshake failed: %v", err)
	}

	runtimeID := resp.Msg.RuntimeId
	runtimeToken := resp.Msg.RuntimeToken

	// Token should be valid immediately
	if !h.ValidateToken(runtimeID, runtimeToken) {
		t.Error("Token should be valid immediately")
	}

	// Wait for custom TTL to expire
	time.Sleep(150 * time.Millisecond)

	// Should be expired
	if h.ValidateToken(runtimeID, runtimeToken) {
		t.Error("Token should be expired after custom TTL")
	}
}

// TestTokenExpiration_ConcurrentValidation verifies thread-safety during expiration.
func TestTokenExpiration_ConcurrentValidation(t *testing.T) {
	h := NewHandshakeServer(&ServeConfig{})

	// Register tokens with short TTL
	numTokens := 50
	for i := 0; i < numTokens; i++ {
		runtimeID := fmt.Sprintf("runtime-%d", i)
		token := fmt.Sprintf("token-%d", i)
		h.mu.Lock()
		now := time.Now()
		h.tokens[runtimeID] = &tokenInfo{
			token:     token,
			issuedAt:  now,
			expiresAt: now.Add(200 * time.Millisecond),
		}
		h.mu.Unlock()
	}

	// Concurrently validate tokens (some will expire during validation)
	done := make(chan bool, numTokens)
	for i := 0; i < numTokens; i++ {
		go func(idx int) {
			runtimeID := fmt.Sprintf("runtime-%d", idx)
			token := fmt.Sprintf("token-%d", idx)

			// Validate multiple times
			for j := 0; j < 10; j++ {
				_ = h.ValidateToken(runtimeID, token)
				time.Sleep(30 * time.Millisecond)
			}
			done <- true
		}(i)
	}

	// Wait for all goroutines
	for i := 0; i < numTokens; i++ {
		<-done
	}

	// All tokens should be expired and cleaned up
	h.mu.Lock()
	remaining := len(h.tokens)
	h.mu.Unlock()

	if remaining > 0 {
		t.Logf("Note: %d tokens not yet cleaned up (lazy cleanup)", remaining)
	}
}

// TestTokenExpiration_MultipleExpiredCleanup verifies cleanup of multiple expired tokens.
func TestTokenExpiration_MultipleExpiredCleanup(t *testing.T) {
	h := NewHandshakeServer(&ServeConfig{})

	// Register 20 tokens, all already expired
	for i := 0; i < 20; i++ {
		runtimeID := fmt.Sprintf("runtime-%d", i)
		token := fmt.Sprintf("token-%d", i)
		h.mu.Lock()
		now := time.Now()
		h.tokens[runtimeID] = &tokenInfo{
			token:     token,
			issuedAt:  now.Add(-2 * time.Hour),
			expiresAt: now.Add(-1 * time.Hour), // All expired
		}
		h.mu.Unlock()
	}

	// Validate one token (triggers lazy cleanup for that token)
	if h.ValidateToken("runtime-0", "token-0") {
		t.Error("Expired token should not validate")
	}

	// Verify only runtime-0 was cleaned up (lazy cleanup is per-validation)
	h.mu.Lock()
	count := len(h.tokens)
	h.mu.Unlock()

	if count != 19 {
		t.Errorf("Expected 19 remaining tokens (lazy cleanup), got %d", count)
	}
}

// TestTokenExpiration_DefaultTTL verifies default TTL values are used.
func TestTokenExpiration_DefaultTTL(t *testing.T) {
	h := NewHandshakeServer(&ServeConfig{
		Plugins: PluginSet{},
		Impls:   map[string]any{},
	})

	// Perform handshake without custom TTL
	req := connect.NewRequest(&connectpluginv1.HandshakeRequest{
		CoreProtocolVersion: 1,
		AppProtocolVersion:  1,
		MagicCookieKey:      DefaultMagicCookieKey,
		MagicCookieValue:    DefaultMagicCookieValue,
		SelfId:              "test-plugin",
	})

	resp, err := h.Handshake(context.Background(), req)
	if err != nil {
		t.Fatalf("Handshake failed: %v", err)
	}

	// Check that token was created with default TTL
	h.mu.Lock()
	info := h.tokens[resp.Msg.RuntimeId]
	h.mu.Unlock()

	if info == nil {
		t.Fatal("Token should exist")
	}

	// Verify TTL is approximately 24 hours
	actualTTL := info.expiresAt.Sub(info.issuedAt)
	expectedTTL := DefaultRuntimeTokenTTL

	// Allow 1-second margin for execution time
	if actualTTL < expectedTTL-time.Second || actualTTL > expectedTTL+time.Second {
		t.Errorf("Expected TTL ~%s, got %s", expectedTTL, actualTTL)
	}
}
