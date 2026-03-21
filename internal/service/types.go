package service

import (
	"context"
	"fmt"
	"sync"
)

// Service defines the interface that all service types must implement.
type Service interface {
	// ServiceType returns the service type identifier (e.g., "web_design").
	ServiceType() string

	// ServiceID returns the unique instance ID.
	ServiceID() string

	// Start starts the service with the given context.
	Start(ctx context.Context) error

	// Stop stops the service gracefully.
	Stop() error

	// IsRunning returns whether the service is currently running.
	IsRunning() bool

	// ServiceInfo returns the current ServiceInfo with runtime state.
	ServiceInfo() *ServiceInfo
}

// ServiceFactory creates a Service instance from ServiceInfo.
type ServiceFactory func(info ServiceInfo) (Service, error)

// ServiceTypeRegistry manages registered service type factories.
type ServiceTypeRegistry struct {
	mu        sync.RWMutex
	factories map[string]ServiceFactory
}

var (
	globalTypeRegistry *ServiceTypeRegistry
	typeRegistryOnce   sync.Once
)

// GlobalTypeRegistry returns the singleton ServiceTypeRegistry.
func GlobalTypeRegistry() *ServiceTypeRegistry {
	typeRegistryOnce.Do(func() {
		globalTypeRegistry = &ServiceTypeRegistry{
			factories: make(map[string]ServiceFactory),
		}
	})
	return globalTypeRegistry
}

// Register registers a service type factory.
func (r *ServiceTypeRegistry) Register(svcType string, factory ServiceFactory) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.factories[svcType] = factory
}

// Create creates a Service instance from ServiceInfo using the registered factory.
func (r *ServiceTypeRegistry) Create(info ServiceInfo) (Service, error) {
	r.mu.RLock()
	factory, ok := r.factories[info.Type]
	r.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("unknown service type: %s", info.Type)
	}
	return factory(info)
}

// RegisteredTypes returns all registered type names.
func (r *ServiceTypeRegistry) RegisteredTypes() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	types := make([]string, 0, len(r.factories))
	for t := range r.factories {
		types = append(types, t)
	}
	return types
}

// HasType checks if a type is registered.
func (r *ServiceTypeRegistry) HasType(svcType string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	_, ok := r.factories[svcType]
	return ok
}
