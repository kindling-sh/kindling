package ci

import (
	"fmt"
	"sync"
)

var (
	mu        sync.RWMutex
	providers = map[string]Provider{}
)

// Register makes a Provider available by its Name().
// It is typically called from an init() function.
func Register(p Provider) {
	mu.Lock()
	defer mu.Unlock()
	providers[p.Name()] = p
}

// Get returns the Provider with the given name, or an error if not found.
func Get(name string) (Provider, error) {
	mu.RLock()
	defer mu.RUnlock()
	p, ok := providers[name]
	if !ok {
		return nil, fmt.Errorf("unknown CI provider %q (available: %v)", name, Names())
	}
	return p, nil
}

// Default returns the default Provider ("github").
// Panics if no providers are registered.
func Default() Provider {
	p, err := Get("github")
	if err != nil {
		panic("ci: no default provider registered — import github.com/jeffvincent/kindling/pkg/ci for side-effect registration")
	}
	return p
}

// Names returns the sorted list of registered provider names.
func Names() []string {
	mu.RLock()
	defer mu.RUnlock()
	names := make([]string, 0, len(providers))
	for name := range providers {
		names = append(names, name)
	}
	return names
}
