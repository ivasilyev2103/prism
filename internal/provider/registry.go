package provider

import (
	"fmt"
	"sync"

	"github.com/helldriver666/prism/internal/types"
)

// registry is the concrete implementation of Registry.
type registry struct {
	mu        sync.RWMutex
	providers map[types.ProviderID]Provider
}

// NewRegistry creates a new empty provider registry.
func NewRegistry() Registry {
	return &registry{
		providers: make(map[types.ProviderID]Provider),
	}
}

func (r *registry) Get(id types.ProviderID) (Provider, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	p, ok := r.providers[id]
	if !ok {
		return nil, fmt.Errorf("provider %q not registered", id)
	}
	return p, nil
}

func (r *registry) GetForService(serviceType types.ServiceType) []Provider {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var result []Provider
	for _, p := range r.providers {
		for _, st := range p.SupportedServices() {
			if st == serviceType {
				result = append(result, p)
				break
			}
		}
	}
	return result
}

func (r *registry) Register(p Provider) {
	r.mu.Lock()
	defer r.mu.Unlock()

	id := p.ID()
	if _, exists := r.providers[id]; exists {
		panic(fmt.Sprintf("provider %q already registered", id))
	}
	r.providers[id] = p
}

func (r *registry) All() []Provider {
	r.mu.RLock()
	defer r.mu.RUnlock()

	result := make([]Provider, 0, len(r.providers))
	for _, p := range r.providers {
		result = append(result, p)
	}
	return result
}
