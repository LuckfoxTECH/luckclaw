package service

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"luckclaw/internal/paths"
)

type Registry struct {
	mu       sync.RWMutex
	services map[string]*ServiceInfo
	running  map[string]Service // running Service instances
}

type ServiceInfo struct {
	ID          string         `json:"id"`
	Type        string         `json:"type"`
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	Dir         string         `json:"dir,omitempty"`
	Host        string         `json:"host"`
	Port        int            `json:"port"`
	Running     bool           `json:"running"`
	AutoStart   bool           `json:"auto_start,omitempty"`
	CreatedAt   string         `json:"created_at"`
	Metadata    map[string]any `json:"metadata,omitempty"`
}

type RegistryData struct {
	Services map[string]ServiceInfo `json:"services"`
	Version  string                 `json:"version"`
}

var globalRegistry *Registry
var globalRegistryOnce sync.Once

func GlobalRegistry() *Registry {
	globalRegistryOnce.Do(func() {
		globalRegistry = &Registry{
			services: make(map[string]*ServiceInfo),
			running:  make(map[string]Service),
		}
	})
	return globalRegistry
}

func (r *Registry) Register(svc *ServiceInfo) {
	r.mu.Lock()
	svc.CreatedAt = time.Now().Format(time.RFC3339)
	r.services[svc.ID] = svc
	r.mu.Unlock()
	_ = r.Save()
}

func (r *Registry) Unregister(id string) {
	r.mu.Lock()
	delete(r.services, id)
	r.mu.Unlock()
	_ = r.Save()
}

func (r *Registry) Get(id string) (*ServiceInfo, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	svc, ok := r.services[id]
	return svc, ok
}

func (r *Registry) GetByType(serviceType string) []*ServiceInfo {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var result []*ServiceInfo
	for _, svc := range r.services {
		if svc.Type == serviceType {
			result = append(result, svc)
		}
	}
	return result
}

func (r *Registry) List() []*ServiceInfo {
	r.mu.RLock()
	defer r.mu.RUnlock()
	result := make([]*ServiceInfo, 0, len(r.services))
	for _, svc := range r.services {
		result = append(result, svc)
	}
	return result
}

func (r *Registry) ListByType(serviceType string) []*ServiceInfo {
	r.mu.RLock()
	defer r.mu.RUnlock()
	result := make([]*ServiceInfo, 0)
	for _, svc := range r.services {
		if svc.Type == serviceType {
			result = append(result, svc)
		}
	}
	return result
}

func (r *Registry) Update(id string, update func(*ServiceInfo)) bool {
	r.mu.Lock()
	svc, ok := r.services[id]
	if !ok {
		r.mu.Unlock()
		return false
	}
	update(svc)
	r.mu.Unlock()
	_ = r.Save()
	return true
}

func (r *Registry) Search(query string) []*ServiceInfo {
	r.mu.RLock()
	defer r.mu.RUnlock()
	query = strings.ToLower(strings.TrimSpace(query))
	if query == "" {
		return r.listAllLocked()
	}
	var result []*ServiceInfo
	for _, svc := range r.services {
		if r.matchService(svc, query) {
			result = append(result, svc)
		}
	}
	return result
}

func (r *Registry) matchService(svc *ServiceInfo, query string) bool {
	if strings.Contains(strings.ToLower(svc.ID), query) {
		return true
	}
	if strings.Contains(strings.ToLower(svc.Type), query) {
		return true
	}
	if strings.Contains(strings.ToLower(svc.Name), query) {
		return true
	}
	if strings.Contains(strings.ToLower(svc.Description), query) {
		return true
	}
	return false
}

func (r *Registry) listAllLocked() []*ServiceInfo {
	result := make([]*ServiceInfo, 0, len(r.services))
	for _, svc := range r.services {
		result = append(result, svc)
	}
	return result
}

func (r *Registry) Save() error {
	registryPath, err := paths.ServiceRegistryPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(registryPath), 0o755); err != nil {
		return err
	}

	r.mu.RLock()
	data := RegistryData{
		Services: make(map[string]ServiceInfo, len(r.services)),
		Version:  "1.0",
	}
	for id, svc := range r.services {
		s := *svc
		data.Services[id] = s
	}
	r.mu.RUnlock()

	b, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return err
	}
	tmp := registryPath + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, registryPath)
}

func (r *Registry) Load() error {
	registryPath, err := paths.ServiceRegistryPath()
	if err != nil {
		return err
	}

	b, err := os.ReadFile(registryPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	var data RegistryData
	if err := json.Unmarshal(b, &data); err != nil {
		return err
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	for id, svc := range data.Services {
		s := svc
		r.services[id] = &s
	}
	return nil
}

func (r *Registry) LoadFromDisk() error {
	return r.Load()
}

// StartService starts a service by ID using the registered factory.
func (r *Registry) StartService(ctx context.Context, id string) error {
	r.mu.Lock()
	info, ok := r.services[id]
	if !ok {
		r.mu.Unlock()
		return fmt.Errorf("service %s not found", id)
	}
	if svc, exists := r.running[id]; exists {
		if svc.IsRunning() {
			r.mu.Unlock()
			return nil // already running
		}
		delete(r.running, id)
	}
	r.mu.Unlock()

	svc, err := GlobalTypeRegistry().Create(*info)
	if err != nil {
		return fmt.Errorf("create service %s: %w", id, err)
	}

	if err := svc.Start(ctx); err != nil {
		return fmt.Errorf("start service %s: %w", id, err)
	}

	r.mu.Lock()
	r.running[id] = svc
	r.mu.Unlock()

	r.Update(id, func(info *ServiceInfo) {
		info.Running = true
		newInfo := svc.ServiceInfo()
		info.Port = newInfo.Port
	})
	return nil
}

// StopService stops a running service by ID.
func (r *Registry) StopService(id string) error {
	r.mu.Lock()
	svc, ok := r.running[id]
	if ok {
		delete(r.running, id)
	}
	r.mu.Unlock()

	if ok {
		if err := svc.Stop(); err != nil {
			return fmt.Errorf("stop service %s: %w", id, err)
		}
	}

	// Always reset running state in registry (handles stale state after restart)
	r.Update(id, func(info *ServiceInfo) {
		info.Running = false
	})
	return nil
}

// GetRunning returns the running Service instance by ID, if any.
func (r *Registry) GetRunning(id string) (Service, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	svc, ok := r.running[id]
	return svc, ok
}

// ListRunning returns all running Service instances.
func (r *Registry) ListRunning() []Service {
	r.mu.RLock()
	defer r.mu.RUnlock()
	result := make([]Service, 0, len(r.running))
	for _, svc := range r.running {
		result = append(result, svc)
	}
	return result
}

// RestoreAutoStart starts all services marked with AutoStart=true.
func (r *Registry) RestoreAutoStart(ctx context.Context) []error {
	var errs []error
	for _, info := range r.List() {
		if info.AutoStart {
			if err := r.StartService(ctx, info.ID); err != nil {
				errs = append(errs, fmt.Errorf("auto-start %s: %w", info.ID, err))
			}
		}
	}
	return errs
}
