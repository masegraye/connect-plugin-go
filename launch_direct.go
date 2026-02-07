package connectplugin

// DirectStrategy is a deprecated alias for InMemoryStrategy.
//
// Deprecated: Use InMemoryStrategy instead. DirectStrategy will be removed in a future release.
type DirectStrategy = InMemoryStrategy

// NewDirectStrategy is a deprecated alias for NewInMemoryStrategy.
//
// Deprecated: Use NewInMemoryStrategy instead.
func NewDirectStrategy(registry *ServiceRegistry) *InMemoryStrategy {
	return NewInMemoryStrategy(registry)
}
