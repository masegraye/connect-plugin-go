package connectplugin

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"connectrpc.com/connect"
	connectpluginv1 "github.com/masegraye/connect-plugin-go/gen/plugin/v1"
)

// =============================================================================
// INPUT VALIDATION TESTS - R2.4
// =============================================================================

// TestValidateServiceType_Valid verifies valid service types are accepted.
func TestValidateServiceType_Valid(t *testing.T) {
	validTypes := []string{
		"cache",
		"logger",
		"auth",
		"Cache-Service",
		"my_service",
		"service-v2",
		"api.v1",
	}

	for _, svcType := range validTypes {
		if err := ValidateServiceType(svcType); err != nil {
			t.Errorf("ValidateServiceType(%q) should be valid: %v", svcType, err)
		}
	}
}

// TestValidateServiceType_Invalid verifies invalid service types are rejected.
func TestValidateServiceType_Invalid(t *testing.T) {
	tests := []struct {
		name      string
		svcType   string
		wantError string
	}{
		{"empty", "", "cannot be empty"},
		{"too_long", strings.Repeat("a", 129), "too long"},
		{"path_traversal_dotdot", "../secrets", "invalid characters"},
		{"path_traversal_slash", "service/admin", "invalid characters"},
		{"path_traversal_backslash", "service\\admin", "invalid characters"},
		{"null_byte", "service\x00admin", "invalid characters"},
		{"starts_with_number", "1service", "invalid characters"},
		{"special_chars", "service@admin", "invalid characters"},
		{"spaces", "my service", "invalid characters"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateServiceType(tt.svcType)
			if err == nil {
				t.Errorf("ValidateServiceType(%q) should return error", tt.svcType)
			}
			if !strings.Contains(err.Error(), tt.wantError) {
				t.Errorf("Error should contain %q, got: %v", tt.wantError, err)
			}
		})
	}
}

// TestValidateMetadata_Valid verifies valid metadata is accepted.
func TestValidateMetadata_Valid(t *testing.T) {
	tests := []struct {
		name     string
		metadata map[string]string
	}{
		{"empty", map[string]string{}},
		{"single_entry", map[string]string{"version": "1.0.0"}},
		{"multiple_entries", map[string]string{
			"version": "1.0.0",
			"author":  "test",
			"name":    "my-plugin",
		}},
		{"max_key_length", map[string]string{
			strings.Repeat("a", 256): "value",
		}},
		{"max_value_length", map[string]string{
			"key": strings.Repeat("x", 4096),
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := ValidateMetadata(tt.metadata); err != nil {
				t.Errorf("ValidateMetadata should be valid: %v", err)
			}
		})
	}
}

// TestValidateMetadata_Invalid verifies invalid metadata is rejected.
func TestValidateMetadata_Invalid(t *testing.T) {
	tests := []struct {
		name      string
		metadata  map[string]string
		wantError string
	}{
		{
			"too_many_entries",
			func() map[string]string {
				m := make(map[string]string)
				for i := 0; i < 101; i++ {
					m[fmt.Sprintf("key%d", i)] = "value"
				}
				return m
			}(),
			"too many metadata entries",
		},
		{
			"empty_key",
			map[string]string{"": "value"},
			"cannot be empty",
		},
		{
			"key_too_long",
			map[string]string{strings.Repeat("a", 257): "value"},
			"key too long",
		},
		{
			"value_too_long",
			map[string]string{"key": strings.Repeat("x", 4097)},
			"value for key",
		},
		{
			"invalid_key_chars",
			map[string]string{"key@invalid": "value"},
			"invalid characters",
		},
		{
			"null_byte_in_key",
			map[string]string{"key\x00admin": "value"},
			"invalid characters",
		},
		{
			"null_byte_in_value",
			map[string]string{"key": "value\x00admin"},
			"null bytes",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateMetadata(tt.metadata)
			if err == nil {
				t.Error("ValidateMetadata should return error")
			}
			if !strings.Contains(err.Error(), tt.wantError) {
				t.Errorf("Error should contain %q, got: %v", tt.wantError, err)
			}
		})
	}
}

// TestValidateSelfID_Valid verifies valid self IDs are accepted.
func TestValidateSelfID_Valid(t *testing.T) {
	validIDs := []string{
		"my-plugin",
		"cache",
		"logger_v2",
		"api.service",
		"Plugin-Name",
	}

	for _, id := range validIDs {
		if err := ValidateSelfID(id); err != nil {
			t.Errorf("ValidateSelfID(%q) should be valid: %v", id, err)
		}
	}
}

// TestValidateSelfID_Invalid verifies invalid self IDs are rejected.
func TestValidateSelfID_Invalid(t *testing.T) {
	tests := []struct {
		name      string
		selfID    string
		wantError string
	}{
		{"empty", "", "cannot be empty"},
		{"too_long", strings.Repeat("a", 129), "too long"},
		{"starts_with_number", "1plugin", "invalid characters"},
		{"null_byte", "plugin\x00", "invalid characters"},
		{"special_chars", "plugin@123", "invalid characters"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateSelfID(tt.selfID)
			if err == nil {
				t.Errorf("ValidateSelfID(%q) should return error", tt.selfID)
			}
			if !strings.Contains(err.Error(), tt.wantError) {
				t.Errorf("Error should contain %q, got: %v", tt.wantError, err)
			}
		})
	}
}

// TestValidateVersion_Valid verifies valid versions are accepted.
func TestValidateVersion_Valid(t *testing.T) {
	validVersions := []string{
		"1.0.0",
		"2.1.3",
		"0.0.1",
		"10.20.30",
		"1.0.0-beta",
		"2.1.3-rc.1",
		"1.0.0-alpha.2",
	}

	for _, version := range validVersions {
		if err := ValidateVersion(version); err != nil {
			t.Errorf("ValidateVersion(%q) should be valid: %v", version, err)
		}
	}
}

// TestValidateVersion_Invalid verifies invalid versions are rejected.
func TestValidateVersion_Invalid(t *testing.T) {
	tests := []struct {
		name      string
		version   string
		wantError string
	}{
		{"empty", "", "cannot be empty"},
		{"too_long", strings.Repeat("1.2.3-", 20), "too long"},
		{"no_patch", "1.0", "semantic version"},
		{"invalid_format", "v1.0.0", "semantic version"},
		{"null_byte", "1.0.0\x00", "semantic version"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateVersion(tt.version)
			if err == nil {
				t.Errorf("ValidateVersion(%q) should return error", tt.version)
			}
			if !strings.Contains(err.Error(), tt.wantError) {
				t.Errorf("Error should contain %q, got: %v", tt.wantError, err)
			}
		})
	}
}

// TestValidateEndpointPath_Valid verifies valid endpoint paths are accepted.
func TestValidateEndpointPath_Valid(t *testing.T) {
	validPaths := []string{
		"/",
		"/api",
		"/cache.v1.Cache/",
		"/logger.v1.Logger/Get",
	}

	for _, path := range validPaths {
		if err := ValidateEndpointPath(path); err != nil {
			t.Errorf("ValidateEndpointPath(%q) should be valid: %v", path, err)
		}
	}
}

// TestValidateEndpointPath_Invalid verifies invalid endpoint paths are rejected.
func TestValidateEndpointPath_Invalid(t *testing.T) {
	tests := []struct {
		name      string
		path      string
		wantError string
	}{
		{"empty", "", "cannot be empty"},
		{"no_leading_slash", "api", "must start with /"},
		{"too_long", "/" + strings.Repeat("a", 256), "too long"},
		{"null_byte", "/api\x00", "null bytes"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateEndpointPath(tt.path)
			if err == nil {
				t.Errorf("ValidateEndpointPath(%q) should return error", tt.path)
			}
			if !strings.Contains(err.Error(), tt.wantError) {
				t.Errorf("Error should contain %q, got: %v", tt.wantError, err)
			}
		})
	}
}

// TestInputValidation_Integration verifies validation is applied in RegisterService.
func TestInputValidation_Integration(t *testing.T) {
	lifecycle := NewLifecycleServer()
	registry := NewServiceRegistry(lifecycle)

	tests := []struct {
		name        string
		serviceType string
		version     string
		endpoint    string
		metadata    map[string]string
		wantError   bool
	}{
		{
			"valid",
			"cache",
			"1.0.0",
			"/cache.v1/",
			map[string]string{"author": "test"},
			false,
		},
		{
			"invalid_service_type",
			"../secrets",
			"1.0.0",
			"/path/",
			nil,
			true,
		},
		{
			"invalid_version",
			"cache",
			"not-a-version",
			"/path/",
			nil,
			true,
		},
		{
			"invalid_endpoint",
			"cache",
			"1.0.0",
			"no-slash",
			nil,
			true,
		},
		{
			"invalid_metadata",
			"cache",
			"1.0.0",
			"/path/",
			map[string]string{"": "empty-key"},
			true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := connect.NewRequest(&connectpluginv1.RegisterServiceRequest{
				ServiceType:  tt.serviceType,
				Version:      tt.version,
				EndpointPath: tt.endpoint,
				Metadata:     tt.metadata,
			})
			req.Header().Set("X-Plugin-Runtime-ID", "test-plugin")

			_, err := registry.RegisterService(context.Background(), req)

			if tt.wantError && err == nil {
				t.Error("Expected validation error")
			}
			if !tt.wantError && err != nil {
				t.Errorf("Expected success, got error: %v", err)
			}
		})
	}
}
