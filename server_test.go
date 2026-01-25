package connectplugin

import (
	"testing"
)

func TestServeConfig_Validate(t *testing.T) {
	tests := []struct {
		name    string
		cfg     ServeConfig
		wantErr bool
	}{
		{
			name: "valid config",
			cfg: ServeConfig{
				Plugins: PluginSet{
					"test": &testPlugin{},
				},
				Impls: map[string]any{
					"test": &testImpl{},
				},
			},
			wantErr: false,
		},
		{
			name: "missing plugins",
			cfg: ServeConfig{
				Impls: map[string]any{
					"test": &testImpl{},
				},
			},
			wantErr: true,
		},
		{
			name: "missing impls",
			cfg: ServeConfig{
				Plugins: PluginSet{
					"test": &testPlugin{},
				},
			},
			wantErr: true,
		},
		{
			name: "plugin without impl",
			cfg: ServeConfig{
				Plugins: PluginSet{
					"test": &testPlugin{},
				},
				Impls: map[string]any{
					"other": &testImpl{},
				},
			},
			wantErr: true,
		},
		{
			name: "impl without plugin",
			cfg: ServeConfig{
				Plugins: PluginSet{
					"test": &testPlugin{},
				},
				Impls: map[string]any{
					"test":  &testImpl{},
					"extra": &testImpl{},
				},
			},
			wantErr: true,
		},
		{
			name: "invalid protocol version",
			cfg: ServeConfig{
				Plugins: PluginSet{
					"test": &testPlugin{},
				},
				Impls: map[string]any{
					"test": &testImpl{},
				},
				ProtocolVersion: -1,
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.cfg.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestPluginSet_Validate(t *testing.T) {
	tests := []struct {
		name    string
		plugins PluginSet
		wantErr bool
	}{
		{
			name: "valid plugin set",
			plugins: PluginSet{
				"test": &testPlugin{},
			},
			wantErr: false,
		},
		{
			name:    "empty plugin set",
			plugins: PluginSet{},
			wantErr: true,
		},
		{
			name: "path conflict",
			plugins: PluginSet{
				"test1": &testPlugin{},
				"test2": &testPlugin{}, // Same path
			},
			wantErr: true,
		},
		{
			name: "name mismatch",
			plugins: PluginSet{
				"wrongname": &testPlugin{}, // testPlugin returns "test" as name
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.plugins.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestPluginSet_Get(t *testing.T) {
	plugins := PluginSet{
		"test": &testPlugin{},
	}

	// Get existing plugin
	p, ok := plugins.Get("test")
	if !ok {
		t.Error("Get() returned false for existing plugin")
	}
	if p == nil {
		t.Error("Get() returned nil plugin")
	}

	// Get non-existent plugin
	p, ok = plugins.Get("nonexistent")
	if ok {
		t.Error("Get() returned true for non-existent plugin")
	}
	if p != nil {
		t.Error("Get() returned non-nil for non-existent plugin")
	}
}

func TestPluginSet_Keys(t *testing.T) {
	plugins := PluginSet{
		"c": &testPlugin{},
		"a": &testPlugin{},
		"b": &testPlugin{},
	}

	keys := plugins.Keys()

	// Should be sorted
	expected := []string{"a", "b", "c"}
	if len(keys) != len(expected) {
		t.Fatalf("Keys() length = %d, want %d", len(keys), len(expected))
	}

	for i, key := range keys {
		if key != expected[i] {
			t.Errorf("Keys()[%d] = %s, want %s", i, key, expected[i])
		}
	}
}

// testImpl is a test implementation for server tests
type testImpl struct{}
