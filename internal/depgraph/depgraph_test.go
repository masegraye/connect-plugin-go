package depgraph

import (
	"reflect"
	"sort"
	"testing"
)

func TestGraph_AddAndGetNode(t *testing.T) {
	g := New()

	node := &Node{
		RuntimeID: "logger-abc",
		SelfID:    "logger",
		Provides: []ServiceDeclaration{
			{Type: "logger", Version: "1.0.0"},
		},
	}

	g.Add(node)

	retrieved := g.GetNode("logger-abc")
	if retrieved == nil {
		t.Fatal("Expected to retrieve node")
	}
	if retrieved.RuntimeID != "logger-abc" {
		t.Errorf("Expected runtime_id logger-abc, got %s", retrieved.RuntimeID)
	}
}

func TestGraph_SimpleStartupOrder(t *testing.T) {
	g := New()

	// Add plugins: logger (no deps) → cache (requires logger) → app (requires cache)
	g.Add(&Node{
		RuntimeID: "logger-abc",
		Provides:  []ServiceDeclaration{{Type: "logger", Version: "1.0.0"}},
	})

	g.Add(&Node{
		RuntimeID: "cache-def",
		Provides:  []ServiceDeclaration{{Type: "cache", Version: "1.0.0"}},
		Requires: []ServiceDependency{
			{Type: "logger", RequiredForStartup: true},
		},
	})

	g.Add(&Node{
		RuntimeID: "app-ghi",
		Provides:  []ServiceDeclaration{{Type: "app", Version: "1.0.0"}},
		Requires: []ServiceDependency{
			{Type: "cache", RequiredForStartup: true},
		},
	})

	order, err := g.StartupOrder()
	if err != nil {
		t.Fatalf("StartupOrder failed: %v", err)
	}

	if len(order) != 3 {
		t.Fatalf("Expected 3 plugins in order, got %d", len(order))
	}

	// Logger should be first (no dependencies)
	if order[0] != "logger-abc" {
		t.Errorf("Expected logger first, got %s", order[0])
	}

	// Cache should be second (depends on logger)
	if order[1] != "cache-def" {
		t.Errorf("Expected cache second, got %s", order[1])
	}

	// App should be last (depends on cache)
	if order[2] != "app-ghi" {
		t.Errorf("Expected app last, got %s", order[2])
	}
}

func TestGraph_MultipleIndependentPlugins(t *testing.T) {
	g := New()

	// Add 3 plugins with no dependencies
	g.Add(&Node{
		RuntimeID: "logger-a",
		Provides:  []ServiceDeclaration{{Type: "logger", Version: "1.0.0"}},
	})

	g.Add(&Node{
		RuntimeID: "metrics-b",
		Provides:  []ServiceDeclaration{{Type: "metrics", Version: "1.0.0"}},
	})

	g.Add(&Node{
		RuntimeID: "cache-c",
		Provides:  []ServiceDeclaration{{Type: "cache", Version: "1.0.0"}},
	})

	order, err := g.StartupOrder()
	if err != nil {
		t.Fatalf("StartupOrder failed: %v", err)
	}

	if len(order) != 3 {
		t.Fatalf("Expected 3 plugins, got %d", len(order))
	}

	// All should be in alphabetical order (deterministic)
	expected := []string{"cache-c", "logger-a", "metrics-b"}
	if !reflect.DeepEqual(order, expected) {
		t.Errorf("Expected order %v, got %v", expected, order)
	}
}

func TestGraph_DiamondDependency(t *testing.T) {
	g := New()

	// Diamond: metrics ← logger, cache ← app
	//          (app depends on both logger and cache)
	g.Add(&Node{
		RuntimeID: "metrics-a",
		Provides:  []ServiceDeclaration{{Type: "metrics", Version: "1.0.0"}},
	})

	g.Add(&Node{
		RuntimeID: "logger-b",
		Provides:  []ServiceDeclaration{{Type: "logger", Version: "1.0.0"}},
		Requires: []ServiceDependency{
			{Type: "metrics", RequiredForStartup: true},
		},
	})

	g.Add(&Node{
		RuntimeID: "cache-c",
		Provides:  []ServiceDeclaration{{Type: "cache", Version: "1.0.0"}},
		Requires: []ServiceDependency{
			{Type: "metrics", RequiredForStartup: true},
		},
	})

	g.Add(&Node{
		RuntimeID: "app-d",
		Provides:  []ServiceDeclaration{{Type: "app", Version: "1.0.0"}},
		Requires: []ServiceDependency{
			{Type: "logger", RequiredForStartup: true},
			{Type: "cache", RequiredForStartup: true},
		},
	})

	order, err := g.StartupOrder()
	if err != nil {
		t.Fatalf("StartupOrder failed: %v", err)
	}

	if len(order) != 4 {
		t.Fatalf("Expected 4 plugins, got %d", len(order))
	}

	// Metrics must be first
	if order[0] != "metrics-a" {
		t.Errorf("Expected metrics first, got %s", order[0])
	}

	// App must be last
	if order[3] != "app-d" {
		t.Errorf("Expected app last, got %s", order[3])
	}

	// Logger and cache must be in the middle (either order)
	middle := []string{order[1], order[2]}
	sort.Strings(middle)
	expected := []string{"cache-c", "logger-b"}
	if !reflect.DeepEqual(middle, expected) {
		t.Errorf("Expected middle %v, got %v", expected, middle)
	}
}

func TestGraph_CycleDetection(t *testing.T) {
	g := New()

	// Create a cycle: A → B → C → A
	g.Add(&Node{
		RuntimeID: "a",
		Provides:  []ServiceDeclaration{{Type: "svc-a", Version: "1.0.0"}},
		Requires: []ServiceDependency{
			{Type: "svc-c", RequiredForStartup: true},
		},
	})

	g.Add(&Node{
		RuntimeID: "b",
		Provides:  []ServiceDeclaration{{Type: "svc-b", Version: "1.0.0"}},
		Requires: []ServiceDependency{
			{Type: "svc-a", RequiredForStartup: true},
		},
	})

	g.Add(&Node{
		RuntimeID: "c",
		Provides:  []ServiceDeclaration{{Type: "svc-c", Version: "1.0.0"}},
		Requires: []ServiceDependency{
			{Type: "svc-b", RequiredForStartup: true},
		},
	})

	_, err := g.StartupOrder()
	if err == nil {
		t.Error("Expected cycle detection error")
	}

	if err != nil && err.Error() != "dependency cycle detected (processed 0 of 3 plugins)" {
		t.Logf("Got error: %v", err)
	}
}

func TestGraph_MissingDependency(t *testing.T) {
	g := New()

	// Plugin requires a service that doesn't exist
	g.Add(&Node{
		RuntimeID: "app-xyz",
		Provides:  []ServiceDeclaration{{Type: "app", Version: "1.0.0"}},
		Requires: []ServiceDependency{
			{Type: "missing-service", RequiredForStartup: true},
		},
	})

	_, err := g.StartupOrder()
	if err == nil {
		t.Error("Expected error for missing dependency")
	}
}

func TestGraph_OptionalDependencyIgnored(t *testing.T) {
	g := New()

	// Plugin has optional dependency that doesn't exist
	g.Add(&Node{
		RuntimeID: "app-xyz",
		Provides:  []ServiceDeclaration{{Type: "app", Version: "1.0.0"}},
		Requires: []ServiceDependency{
			{Type: "cache", RequiredForStartup: false}, // Optional
		},
	})

	order, err := g.StartupOrder()
	if err != nil {
		t.Fatalf("StartupOrder failed: %v (optional deps should be ignored)", err)
	}

	if len(order) != 1 || order[0] != "app-xyz" {
		t.Errorf("Expected [app-xyz], got %v", order)
	}
}

func TestGraph_GetImpact_NoProviderAlternatives(t *testing.T) {
	g := New()

	// Logger → Cache → App (linear chain)
	g.Add(&Node{
		RuntimeID: "logger-abc",
		Provides:  []ServiceDeclaration{{Type: "logger", Version: "1.0.0"}},
	})

	g.Add(&Node{
		RuntimeID: "cache-def",
		Provides:  []ServiceDeclaration{{Type: "cache", Version: "1.0.0"}},
		Requires: []ServiceDependency{
			{Type: "logger", RequiredForStartup: true},
		},
	})

	g.Add(&Node{
		RuntimeID: "app-ghi",
		Requires: []ServiceDependency{
			{Type: "cache", RequiredForStartup: true},
		},
	})

	// Removing logger should affect cache and app
	impact := g.GetImpact("logger-abc")

	if len(impact.AffectedServices) != 1 || impact.AffectedServices[0] != "logger" {
		t.Errorf("Expected affected services [logger], got %v", impact.AffectedServices)
	}

	expectedAffected := []string{"app-ghi", "cache-def"}
	sort.Strings(expectedAffected)
	actualAffected := impact.AffectedPlugins
	sort.Strings(actualAffected)

	if !reflect.DeepEqual(actualAffected, expectedAffected) {
		t.Errorf("Expected affected plugins %v, got %v", expectedAffected, actualAffected)
	}
}

func TestGraph_GetImpact_WithAlternativeProvider(t *testing.T) {
	g := New()

	// Two logger providers
	g.Add(&Node{
		RuntimeID: "logger-a",
		Provides:  []ServiceDeclaration{{Type: "logger", Version: "1.0.0"}},
	})

	g.Add(&Node{
		RuntimeID: "logger-b",
		Provides:  []ServiceDeclaration{{Type: "logger", Version: "1.0.0"}},
	})

	// Cache requires logger
	g.Add(&Node{
		RuntimeID: "cache-c",
		Provides:  []ServiceDeclaration{{Type: "cache", Version: "1.0.0"}},
		Requires: []ServiceDependency{
			{Type: "logger", RequiredForStartup: true},
		},
	})

	// Removing logger-a should NOT affect cache (logger-b still available)
	impact := g.GetImpact("logger-a")

	if len(impact.AffectedPlugins) != 0 {
		t.Errorf("Expected no affected plugins (alternative provider exists), got %v", impact.AffectedPlugins)
	}

	if len(impact.OptionalImpact) != 1 || impact.OptionalImpact[0] != "cache-c" {
		t.Errorf("Expected optional impact [cache-c], got %v", impact.OptionalImpact)
	}
}

func TestGraph_GetImpact_RemovingLastProvider(t *testing.T) {
	g := New()

	// Two logger providers
	g.Add(&Node{
		RuntimeID: "logger-a",
		Provides:  []ServiceDeclaration{{Type: "logger", Version: "1.0.0"}},
	})

	g.Add(&Node{
		RuntimeID: "logger-b",
		Provides:  []ServiceDeclaration{{Type: "logger", Version: "1.0.0"}},
	})

	// Cache requires logger
	g.Add(&Node{
		RuntimeID: "cache-c",
		Requires: []ServiceDependency{
			{Type: "logger", RequiredForStartup: true},
		},
	})

	// Remove logger-a first
	g.Remove("logger-a")

	// Now removing logger-b SHOULD affect cache (last provider)
	impact := g.GetImpact("logger-b")

	if len(impact.AffectedPlugins) != 1 || impact.AffectedPlugins[0] != "cache-c" {
		t.Errorf("Expected affected plugins [cache-c], got %v", impact.AffectedPlugins)
	}
}

func TestGraph_GetImpact_OptionalDependency(t *testing.T) {
	g := New()

	// Logger provider
	g.Add(&Node{
		RuntimeID: "logger-abc",
		Provides:  []ServiceDeclaration{{Type: "logger", Version: "1.0.0"}},
	})

	// Cache has optional logger dependency
	g.Add(&Node{
		RuntimeID: "cache-def",
		Provides:  []ServiceDeclaration{{Type: "cache", Version: "1.0.0"}},
		Requires: []ServiceDependency{
			{Type: "logger", RequiredForStartup: false}, // Optional
		},
	})

	// Removing logger should only have optional impact on cache
	impact := g.GetImpact("logger-abc")

	if len(impact.AffectedPlugins) != 0 {
		t.Errorf("Expected no required affected plugins, got %v", impact.AffectedPlugins)
	}

	if len(impact.OptionalImpact) != 1 || impact.OptionalImpact[0] != "cache-def" {
		t.Errorf("Expected optional impact [cache-def], got %v", impact.OptionalImpact)
	}
}

func TestGraph_GetImpact_MultipleServicesFromOnePlugin(t *testing.T) {
	g := New()

	// Multi-service plugin provides both logger and metrics
	g.Add(&Node{
		RuntimeID: "multi-plugin",
		Provides: []ServiceDeclaration{
			{Type: "logger", Version: "1.0.0"},
			{Type: "metrics", Version: "1.0.0"},
		},
	})

	// Plugin A requires logger
	g.Add(&Node{
		RuntimeID: "plugin-a",
		Requires: []ServiceDependency{
			{Type: "logger", RequiredForStartup: true},
		},
	})

	// Plugin B requires metrics
	g.Add(&Node{
		RuntimeID: "plugin-b",
		Requires: []ServiceDependency{
			{Type: "metrics", RequiredForStartup: true},
		},
	})

	// Removing multi-plugin should affect both A and B
	impact := g.GetImpact("multi-plugin")

	expectedServices := []string{"logger", "metrics"}
	sort.Strings(impact.AffectedServices)
	if !reflect.DeepEqual(impact.AffectedServices, expectedServices) {
		t.Errorf("Expected affected services %v, got %v", expectedServices, impact.AffectedServices)
	}

	expectedPlugins := []string{"plugin-a", "plugin-b"}
	sort.Strings(impact.AffectedPlugins)
	if !reflect.DeepEqual(impact.AffectedPlugins, expectedPlugins) {
		t.Errorf("Expected affected plugins %v, got %v", expectedPlugins, impact.AffectedPlugins)
	}
}

func TestGraph_TransitiveDependencies(t *testing.T) {
	g := New()

	// Chain: metrics → logger → cache → app
	g.Add(&Node{
		RuntimeID: "metrics",
		Provides:  []ServiceDeclaration{{Type: "metrics", Version: "1.0.0"}},
	})

	g.Add(&Node{
		RuntimeID: "logger",
		Provides:  []ServiceDeclaration{{Type: "logger", Version: "1.0.0"}},
		Requires: []ServiceDependency{
			{Type: "metrics", RequiredForStartup: true},
		},
	})

	g.Add(&Node{
		RuntimeID: "cache",
		Provides:  []ServiceDeclaration{{Type: "cache", Version: "1.0.0"}},
		Requires: []ServiceDependency{
			{Type: "logger", RequiredForStartup: true},
		},
	})

	g.Add(&Node{
		RuntimeID: "app",
		Requires: []ServiceDependency{
			{Type: "cache", RequiredForStartup: true},
		},
	})

	// Removing metrics should transitively affect logger, cache, app
	impact := g.GetImpact("metrics")

	expectedAffected := []string{"app", "cache", "logger"}
	sort.Strings(impact.AffectedPlugins)
	if !reflect.DeepEqual(impact.AffectedPlugins, expectedAffected) {
		t.Errorf("Expected transitive affected plugins %v, got %v", expectedAffected, impact.AffectedPlugins)
	}
}

func TestGraph_Remove(t *testing.T) {
	g := New()

	g.Add(&Node{
		RuntimeID: "logger-abc",
		Provides:  []ServiceDeclaration{{Type: "logger", Version: "1.0.0"}},
	})

	// Verify added
	if !g.HasService("logger") {
		t.Error("Expected logger service to exist")
	}

	// Remove
	g.Remove("logger-abc")

	// Verify removed
	if g.HasService("logger") {
		t.Error("Expected logger service to be removed")
	}

	if g.GetNode("logger-abc") != nil {
		t.Error("Expected node to be removed")
	}
}

func TestGraph_GetProviders(t *testing.T) {
	g := New()

	g.Add(&Node{
		RuntimeID: "logger-a",
		Provides:  []ServiceDeclaration{{Type: "logger", Version: "1.0.0"}},
	})

	g.Add(&Node{
		RuntimeID: "logger-b",
		Provides:  []ServiceDeclaration{{Type: "logger", Version: "2.0.0"}},
	})

	providers := g.GetProviders("logger")
	sort.Strings(providers)

	expected := []string{"logger-a", "logger-b"}
	if !reflect.DeepEqual(providers, expected) {
		t.Errorf("Expected providers %v, got %v", expected, providers)
	}
}

func TestGraph_HasService(t *testing.T) {
	g := New()

	if g.HasService("logger") {
		t.Error("Expected logger service to not exist initially")
	}

	g.Add(&Node{
		RuntimeID: "logger-abc",
		Provides:  []ServiceDeclaration{{Type: "logger", Version: "1.0.0"}},
	})

	if !g.HasService("logger") {
		t.Error("Expected logger service to exist after adding provider")
	}

	if g.HasService("nonexistent") {
		t.Error("Expected nonexistent service to not exist")
	}
}

func TestGraph_ComplexTopology(t *testing.T) {
	g := New()

	// Complex:
	// - metrics (no deps)
	// - logger-a (requires metrics)
	// - logger-b (requires metrics)
	// - cache (requires logger - either one)
	// - db (requires logger - either one)
	// - app (requires cache, db)

	g.Add(&Node{RuntimeID: "metrics", Provides: []ServiceDeclaration{{Type: "metrics", Version: "1.0.0"}}})
	g.Add(&Node{RuntimeID: "logger-a", Provides: []ServiceDeclaration{{Type: "logger", Version: "1.0.0"}},
		Requires: []ServiceDependency{{Type: "metrics", RequiredForStartup: true}}})
	g.Add(&Node{RuntimeID: "logger-b", Provides: []ServiceDeclaration{{Type: "logger", Version: "1.0.0"}},
		Requires: []ServiceDependency{{Type: "metrics", RequiredForStartup: true}}})
	g.Add(&Node{RuntimeID: "cache", Provides: []ServiceDeclaration{{Type: "cache", Version: "1.0.0"}},
		Requires: []ServiceDependency{{Type: "logger", RequiredForStartup: true}}})
	g.Add(&Node{RuntimeID: "db", Provides: []ServiceDeclaration{{Type: "db", Version: "1.0.0"}},
		Requires: []ServiceDependency{{Type: "logger", RequiredForStartup: true}}})
	g.Add(&Node{RuntimeID: "app", Provides: []ServiceDeclaration{{Type: "app", Version: "1.0.0"}},
		Requires: []ServiceDependency{
			{Type: "cache", RequiredForStartup: true},
			{Type: "db", RequiredForStartup: true},
		}})

	order, err := g.StartupOrder()
	if err != nil {
		t.Fatalf("StartupOrder failed: %v", err)
	}

	if len(order) != 6 {
		t.Fatalf("Expected 6 plugins, got %d", len(order))
	}

	// Verify metrics is first
	if order[0] != "metrics" {
		t.Errorf("Expected metrics first, got %s", order[0])
	}

	// Verify app is last
	if order[5] != "app" {
		t.Errorf("Expected app last, got %s", order[5])
	}

	// Verify proper ordering: position[dependency] < position[dependent]
	pos := make(map[string]int)
	for i, id := range order {
		pos[id] = i
	}

	// logger-a and logger-b must come after metrics
	if pos["logger-a"] <= pos["metrics"] {
		t.Error("logger-a should come after metrics")
	}
	if pos["logger-b"] <= pos["metrics"] {
		t.Error("logger-b should come after metrics")
	}

	// cache and db must come after at least one logger
	if pos["cache"] <= pos["logger-a"] && pos["cache"] <= pos["logger-b"] {
		t.Error("cache should come after at least one logger")
	}
	if pos["db"] <= pos["logger-a"] && pos["db"] <= pos["logger-b"] {
		t.Error("db should come after at least one logger")
	}

	// app must come after cache and db
	if pos["app"] <= pos["cache"] {
		t.Error("app should come after cache")
	}
	if pos["app"] <= pos["db"] {
		t.Error("app should come after db")
	}
}

func TestGraph_GetImpact_EmptyGraph(t *testing.T) {
	g := New()

	impact := g.GetImpact("nonexistent")

	if impact.TargetPlugin != "nonexistent" {
		t.Errorf("Expected target nonexistent, got %s", impact.TargetPlugin)
	}

	if len(impact.AffectedPlugins) != 0 {
		t.Error("Expected no affected plugins for nonexistent target")
	}
}
