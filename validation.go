package connectplugin

import (
	"fmt"
	"regexp"
	"strings"
)

const (
	// MaxMetadataEntries is the maximum number of metadata entries.
	MaxMetadataEntries = 100

	// MaxMetadataKeyLen is the maximum length of a metadata key.
	MaxMetadataKeyLen = 256

	// MaxMetadataValueLen is the maximum length of a metadata value.
	MaxMetadataValueLen = 4096

	// MaxServiceTypeLen is the maximum length of a service type name.
	MaxServiceTypeLen = 128

	// MaxSelfIDLen is the maximum length of a self-declared ID.
	MaxSelfIDLen = 128

	// MaxVersionLen is the maximum length of a version string.
	MaxVersionLen = 64
)

var (
	// validKeyPattern matches valid metadata keys and service types.
	// Must start with letter, contain only alphanumeric, underscore, hyphen, dot.
	validKeyPattern = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9_.\-]*$`)

	// validVersionPattern matches semantic version strings.
	// Examples: "1.0.0", "2.1.3-beta", "3.0.0-rc.1"
	validVersionPattern = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+(-[a-zA-Z0-9.-]+)?$`)
)

// ValidateMetadata validates a metadata map.
// Returns an error if:
// - Too many entries (> MaxMetadataEntries)
// - Key is empty, too long, or contains invalid characters
// - Value is too long
func ValidateMetadata(metadata map[string]string) error {
	if len(metadata) > MaxMetadataEntries {
		return fmt.Errorf("too many metadata entries: %d (max: %d)", len(metadata), MaxMetadataEntries)
	}

	for key, value := range metadata {
		// Validate key
		if key == "" {
			return fmt.Errorf("metadata key cannot be empty")
		}
		if len(key) > MaxMetadataKeyLen {
			return fmt.Errorf("metadata key too long: %d bytes (max: %d)", len(key), MaxMetadataKeyLen)
		}
		if !validKeyPattern.MatchString(key) {
			return fmt.Errorf("metadata key %q contains invalid characters (must match: %s)", key, validKeyPattern.String())
		}

		// Validate value
		if len(value) > MaxMetadataValueLen {
			return fmt.Errorf("metadata value for key %q too long: %d bytes (max: %d)", key, len(value), MaxMetadataValueLen)
		}

		// Check for null bytes (potential injection)
		if strings.Contains(key, "\x00") || strings.Contains(value, "\x00") {
			return fmt.Errorf("metadata contains null bytes (potential injection)")
		}
	}

	return nil
}

// ValidateServiceType validates a service type name.
// Returns an error if:
// - Empty or too long
// - Contains invalid characters
// - Contains path traversal sequences
func ValidateServiceType(serviceType string) error {
	if serviceType == "" {
		return fmt.Errorf("service type cannot be empty")
	}

	if len(serviceType) > MaxServiceTypeLen {
		return fmt.Errorf("service type too long: %d bytes (max: %d)", len(serviceType), MaxServiceTypeLen)
	}

	if !validKeyPattern.MatchString(serviceType) {
		return fmt.Errorf("service type %q contains invalid characters (must match: %s)", serviceType, validKeyPattern.String())
	}

	// Check for path traversal attempts
	if strings.Contains(serviceType, "..") || strings.Contains(serviceType, "/") || strings.Contains(serviceType, "\\") {
		return fmt.Errorf("service type contains path traversal characters: %s", serviceType)
	}

	// Check for null bytes
	if strings.Contains(serviceType, "\x00") {
		return fmt.Errorf("service type contains null bytes")
	}

	return nil
}

// ValidateSelfID validates a plugin's self-declared ID.
// Returns an error if:
// - Empty or too long
// - Contains invalid characters
func ValidateSelfID(selfID string) error {
	if selfID == "" {
		return fmt.Errorf("self_id cannot be empty")
	}

	if len(selfID) > MaxSelfIDLen {
		return fmt.Errorf("self_id too long: %d bytes (max: %d)", len(selfID), MaxSelfIDLen)
	}

	if !validKeyPattern.MatchString(selfID) {
		return fmt.Errorf("self_id %q contains invalid characters (must match: %s)", selfID, validKeyPattern.String())
	}

	// Check for null bytes
	if strings.Contains(selfID, "\x00") {
		return fmt.Errorf("self_id contains null bytes")
	}

	return nil
}

// ValidateVersion validates a version string.
// Returns an error if:
// - Empty or too long
// - Invalid semantic version format
func ValidateVersion(version string) error {
	if version == "" {
		return fmt.Errorf("version cannot be empty")
	}

	if len(version) > MaxVersionLen {
		return fmt.Errorf("version too long: %d bytes (max: %d)", len(version), MaxVersionLen)
	}

	if !validVersionPattern.MatchString(version) {
		return fmt.Errorf("version %q must be semantic version (e.g., 1.0.0, 2.1.3-beta)", version)
	}

	// Check for null bytes
	if strings.Contains(version, "\x00") {
		return fmt.Errorf("version contains null bytes")
	}

	return nil
}

// ValidateEndpointPath validates an endpoint path.
// Returns an error if:
// - Empty or doesn't start with /
// - Too long
// - Contains invalid characters
func ValidateEndpointPath(path string) error {
	if path == "" {
		return fmt.Errorf("endpoint path cannot be empty")
	}

	if !strings.HasPrefix(path, "/") {
		return fmt.Errorf("endpoint path must start with /: %s", path)
	}

	if len(path) > 256 {
		return fmt.Errorf("endpoint path too long: %d bytes (max: 256)", len(path))
	}

	// Check for null bytes
	if strings.Contains(path, "\x00") {
		return fmt.Errorf("endpoint path contains null bytes")
	}

	return nil
}
