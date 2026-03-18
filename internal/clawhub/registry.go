package clawhub

import (
	"os"
	"sync"
)

var (
	registryCache string
	registryMu    sync.Mutex
)

// RegistryURL returns the ClawHub registry base URL.
// Uses CLAWHUB_REGISTRY env, or discovers from default site.
func RegistryURL() string {
	registryMu.Lock()
	defer registryMu.Unlock()
	if registryCache != "" {
		return registryCache
	}
	if u := os.Getenv("CLAWHUB_REGISTRY"); u != "" {
		registryCache = u
		return u
	}
	// Default without discovery to avoid extra HTTP call at init
	registryCache = DefaultRegistry
	return registryCache
}
