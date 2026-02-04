package connectplugin

import (
	"context"
	"testing"

	"connectrpc.com/connect"
	connectpluginv1 "github.com/masegraye/connect-plugin-go/gen/plugin/v1"
)

// =============================================================================
// SERVICE REGISTRATION AUTHORIZATION TESTS - R2.3
// =============================================================================

// TestServiceAuthorization_AllowedService verifies authorized registration succeeds.
func TestServiceAuthorization_AllowedService(t *testing.T) {
	lifecycle := NewLifecycleServer()
	registry := NewServiceRegistry(lifecycle)

	// Set allowed services for runtime
	runtimeID := "plugin-123"
	registry.SetAllowedServices(runtimeID, []string{"cache", "logger"})

	// Attempt to register allowed service
	req := connect.NewRequest(&connectpluginv1.RegisterServiceRequest{
		ServiceType:  "cache",
		Version:      "1.0.0",
		EndpointPath: "/cache.v1.Cache/",
		Metadata:     map[string]string{},
	})
	req.Header().Set("X-Plugin-Runtime-ID", runtimeID)

	_, err := registry.RegisterService(context.Background(), req)
	if err != nil {
		t.Errorf("Authorized service registration should succeed: %v", err)
	}
}

// TestServiceAuthorization_UnauthorizedService verifies unauthorized registration is rejected.
func TestServiceAuthorization_UnauthorizedService(t *testing.T) {
	lifecycle := NewLifecycleServer()
	registry := NewServiceRegistry(lifecycle)

	// Set allowed services for runtime (only "cache")
	runtimeID := "plugin-123"
	registry.SetAllowedServices(runtimeID, []string{"cache"})

	// Attempt to register unauthorized service
	req := connect.NewRequest(&connectpluginv1.RegisterServiceRequest{
		ServiceType:  "secrets", // Not in allowed list!
		Version:      "1.0.0",
		EndpointPath: "/secrets.v1.Secrets/",
		Metadata:     map[string]string{},
	})
	req.Header().Set("X-Plugin-Runtime-ID", runtimeID)

	_, err := registry.RegisterService(context.Background(), req)
	if err == nil {
		t.Error("Unauthorized service registration should fail")
	}

	// Verify error code is PermissionDenied
	connectErr, ok := err.(*connect.Error)
	if !ok {
		t.Fatalf("Expected connect.Error, got %T", err)
	}
	if connectErr.Code() != connect.CodePermissionDenied {
		t.Errorf("Expected PermissionDenied, got %v", connectErr.Code())
	}

	// Verify error message mentions authorization
	errMsg := connectErr.Error()
	if !stringContains(errMsg, "not authorized") {
		t.Errorf("Error should mention authorization: %v", errMsg)
	}
}

// TestServiceAuthorization_NoRestrictionsAllowsAny verifies plugins without restrictions can register any service.
func TestServiceAuthorization_NoRestrictionsAllowsAny(t *testing.T) {
	lifecycle := NewLifecycleServer()
	registry := NewServiceRegistry(lifecycle)

	// Don't set any restrictions for this runtime
	runtimeID := "unrestricted-plugin"

	// Should allow any service type
	serviceTypes := []string{"cache", "logger", "secrets", "auth"}
	for _, svcType := range serviceTypes {
		req := connect.NewRequest(&connectpluginv1.RegisterServiceRequest{
			ServiceType:  svcType,
			Version:      "1.0.0",
			EndpointPath: "/" + svcType + ".v1." + svcType + "/",
			Metadata:     map[string]string{},
		})
		req.Header().Set("X-Plugin-Runtime-ID", runtimeID)

		_, err := registry.RegisterService(context.Background(), req)
		if err != nil {
			t.Errorf("Unrestricted plugin should be able to register %s: %v", svcType, err)
		}
	}
}

// TestServiceAuthorization_MultipleAllowedServices verifies multiple allowed services work.
func TestServiceAuthorization_MultipleAllowedServices(t *testing.T) {
	lifecycle := NewLifecycleServer()
	registry := NewServiceRegistry(lifecycle)

	runtimeID := "multi-service-plugin"
	registry.SetAllowedServices(runtimeID, []string{"cache", "logger", "storage"})

	// All three should be allowed
	allowedServices := []string{"cache", "logger", "storage"}
	for _, svcType := range allowedServices {
		req := connect.NewRequest(&connectpluginv1.RegisterServiceRequest{
			ServiceType:  svcType,
			Version:      "1.0.0",
			EndpointPath: "/" + svcType + ".v1/",
			Metadata:     map[string]string{},
		})
		req.Header().Set("X-Plugin-Runtime-ID", runtimeID)

		_, err := registry.RegisterService(context.Background(), req)
		if err != nil {
			t.Errorf("Service %s should be allowed: %v", svcType, err)
		}
	}

	// This should be denied
	req := connect.NewRequest(&connectpluginv1.RegisterServiceRequest{
		ServiceType:  "secrets",
		Version:      "1.0.0",
		EndpointPath: "/secrets.v1/",
		Metadata:     map[string]string{},
	})
	req.Header().Set("X-Plugin-Runtime-ID", runtimeID)

	_, err := registry.RegisterService(context.Background(), req)
	if err == nil {
		t.Error("Unauthorized service should be denied")
	}
}

// TestServiceAuthorization_EmptyAllowedList verifies empty allowed list denies all.
func TestServiceAuthorization_EmptyAllowedList(t *testing.T) {
	lifecycle := NewLifecycleServer()
	registry := NewServiceRegistry(lifecycle)

	runtimeID := "restricted-plugin"
	registry.SetAllowedServices(runtimeID, []string{}) // Empty list = deny all

	// Should deny registration
	req := connect.NewRequest(&connectpluginv1.RegisterServiceRequest{
		ServiceType:  "cache",
		Version:      "1.0.0",
		EndpointPath: "/cache.v1/",
		Metadata:     map[string]string{},
	})
	req.Header().Set("X-Plugin-Runtime-ID", runtimeID)

	_, err := registry.RegisterService(context.Background(), req)
	if err == nil {
		t.Error("Empty allowed list should deny all services")
	}

	connectErr := err.(*connect.Error)
	if connectErr.Code() != connect.CodePermissionDenied {
		t.Errorf("Expected PermissionDenied, got %v", connectErr.Code())
	}
}

// stringContains checks if a string contains a substring.
func stringContains(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
