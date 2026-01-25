// Package depgraph provides dependency graph management for plugin ordering and impact analysis.
package depgraph

import (
	"fmt"
	"sort"
)

// Graph represents a dependency graph of plugins and their service dependencies.
type Graph struct {
	nodes map[string]*Node           // runtime_id → node
	edges map[string][]string        // runtime_id → service types required
	byType map[string][]string       // service_type → provider runtime_ids
}

// Node represents a plugin in the dependency graph.
type Node struct {
	RuntimeID string
	SelfID    string

	// Services this plugin provides
	Provides []ServiceDeclaration

	// Services this plugin requires
	Requires []ServiceDependency
}

// ServiceDeclaration describes a service a plugin provides.
type ServiceDeclaration struct {
	Type    string
	Version string
}

// ServiceDependency describes a service a plugin requires.
type ServiceDependency struct {
	Type                string
	MinVersion          string
	RequiredForStartup  bool
	WatchForChanges     bool
}

// New creates a new dependency graph.
func New() *Graph {
	return &Graph{
		nodes:  make(map[string]*Node),
		edges:  make(map[string][]string),
		byType: make(map[string][]string),
	}
}

// Add adds a plugin node to the graph.
func (g *Graph) Add(node *Node) {
	g.nodes[node.RuntimeID] = node

	// Build edges for required dependencies
	required := make([]string, 0)
	for _, dep := range node.Requires {
		if dep.RequiredForStartup {
			required = append(required, dep.Type)
		}
	}
	g.edges[node.RuntimeID] = required

	// Update byType index for services this plugin provides
	for _, svc := range node.Provides {
		g.byType[svc.Type] = append(g.byType[svc.Type], node.RuntimeID)
	}
}

// Remove removes a plugin node from the graph.
func (g *Graph) Remove(runtimeID string) {
	node, ok := g.nodes[runtimeID]
	if !ok {
		return
	}

	// Remove from nodes
	delete(g.nodes, runtimeID)

	// Remove edges
	delete(g.edges, runtimeID)

	// Remove from byType index
	for _, svc := range node.Provides {
		providers := g.byType[svc.Type]
		for i, pid := range providers {
			if pid == runtimeID {
				g.byType[svc.Type] = append(providers[:i], providers[i+1:]...)
				break
			}
		}
		if len(g.byType[svc.Type]) == 0 {
			delete(g.byType, svc.Type)
		}
	}
}

// StartupOrder returns the plugins in dependency-ordered startup sequence.
// Plugins with no dependencies come first, then plugins that depend on them, etc.
// Returns error if a dependency cycle is detected.
func (g *Graph) StartupOrder() ([]string, error) {
	// Kahn's algorithm for topological sort with cycle detection
	inDegree := make(map[string]int)
	adjList := make(map[string][]string) // service_type → plugins that depend on it

	// Initialize in-degrees
	for runtimeID := range g.nodes {
		inDegree[runtimeID] = 0
	}

	// Build adjacency list and compute in-degrees
	for runtimeID, requiredServices := range g.edges {
		for _, serviceType := range requiredServices {
			// Find providers of this service type
			providers := g.byType[serviceType]
			if len(providers) == 0 {
				return nil, fmt.Errorf("plugin %s requires service %q but no provider exists",
					runtimeID, serviceType)
			}

			// Add edges from providers to this plugin
			for _, providerID := range providers {
				adjList[providerID] = append(adjList[providerID], runtimeID)
				inDegree[runtimeID]++
			}
		}
	}

	// Start with nodes that have no dependencies
	queue := make([]string, 0)
	for runtimeID, degree := range inDegree {
		if degree == 0 {
			queue = append(queue, runtimeID)
		}
	}

	// Sort queue for deterministic output
	sort.Strings(queue)

	result := make([]string, 0, len(g.nodes))

	for len(queue) > 0 {
		// Pop from queue
		current := queue[0]
		queue = queue[1:]
		result = append(result, current)

		// Reduce in-degree for dependents
		for _, dependent := range adjList[current] {
			inDegree[dependent]--
			if inDegree[dependent] == 0 {
				queue = append(queue, dependent)
				sort.Strings(queue) // Keep deterministic
			}
		}
	}

	// If we didn't process all nodes, there's a cycle
	if len(result) != len(g.nodes) {
		return nil, fmt.Errorf("dependency cycle detected (processed %d of %d plugins)",
			len(result), len(g.nodes))
	}

	return result, nil
}

// GetImpact analyzes what will be affected if a plugin is removed.
func (g *Graph) GetImpact(runtimeID string) *ImpactAnalysis {
	node, ok := g.nodes[runtimeID]
	if !ok {
		return &ImpactAnalysis{
			TargetPlugin: runtimeID,
		}
	}

	impact := &ImpactAnalysis{
		TargetPlugin:     runtimeID,
		AffectedServices: make([]string, 0),
		AffectedPlugins:  make([]string, 0),
		OptionalImpact:   make([]string, 0),
	}

	// Services that will become unavailable
	for _, svc := range node.Provides {
		impact.AffectedServices = append(impact.AffectedServices, svc.Type)
	}

	// Find plugins that depend on these services
	visited := make(map[string]bool)
	g.findDependents(runtimeID, impact, visited)

	// Sort for deterministic output
	sort.Strings(impact.AffectedPlugins)
	sort.Strings(impact.OptionalImpact)
	sort.Strings(impact.AffectedServices)

	return impact
}

// findDependents recursively finds all plugins that depend on this plugin's services.
func (g *Graph) findDependents(runtimeID string, impact *ImpactAnalysis, visited map[string]bool) {
	if visited[runtimeID] {
		return
	}
	visited[runtimeID] = true

	node := g.nodes[runtimeID]
	if node == nil {
		return
	}

	// Find plugins that require services provided by this node
	for _, svc := range node.Provides {
		// Look for plugins that require this service type
		for otherID, otherNode := range g.nodes {
			if otherID == runtimeID {
				continue
			}

			// Check if otherNode requires this service
			for _, dep := range otherNode.Requires {
				if dep.Type == svc.Type {
					// Check if service will still be available from other providers
					otherProviders := g.getOtherProviders(svc.Type, runtimeID)

					if len(otherProviders) > 0 {
						// Service still available from other providers
						impact.OptionalImpact = appendUnique(impact.OptionalImpact, otherID)
					} else if dep.RequiredForStartup {
						// Service won't be available and it's required
						impact.AffectedPlugins = appendUnique(impact.AffectedPlugins, otherID)
						// Recursively find transitive dependents
						g.findDependents(otherID, impact, visited)
					} else {
						// Optional dependency
						impact.OptionalImpact = appendUnique(impact.OptionalImpact, otherID)
					}
				}
			}
		}
	}
}

// getOtherProviders returns providers of a service type excluding the given runtime_id.
func (g *Graph) getOtherProviders(serviceType, excludeRuntimeID string) []string {
	result := make([]string, 0)
	for _, providerID := range g.byType[serviceType] {
		if providerID != excludeRuntimeID {
			result = append(result, providerID)
		}
	}
	return result
}

// ImpactAnalysis describes what will be affected by removing a plugin.
type ImpactAnalysis struct {
	// The plugin being removed
	TargetPlugin string

	// Services that will become unavailable (if no other providers)
	AffectedServices []string

	// Plugins that will be affected (have required dependencies that will be unavailable)
	AffectedPlugins []string

	// Plugins that have optional dependencies or alternative providers available
	OptionalImpact []string
}

// appendUnique appends a string to a slice if it's not already present.
func appendUnique(slice []string, item string) []string {
	for _, existing := range slice {
		if existing == item {
			return slice
		}
	}
	return append(slice, item)
}

// GetNode returns a node by runtime ID.
func (g *Graph) GetNode(runtimeID string) *Node {
	return g.nodes[runtimeID]
}

// GetProviders returns all plugins that provide a given service type.
func (g *Graph) GetProviders(serviceType string) []string {
	providers := g.byType[serviceType]
	result := make([]string, len(providers))
	copy(result, providers)
	return result
}

// HasService returns true if at least one plugin provides the given service type.
func (g *Graph) HasService(serviceType string) bool {
	return len(g.byType[serviceType]) > 0
}
