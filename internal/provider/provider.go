package provider

import (
	"context"

	"github.com/helldriver666/prism/internal/types"
)

// Provider is an adapter to a specific AI provider.
// All implementations are interchangeable (Liskov Substitution).
// Provider works as a proxy: receives SanitizedBody and proxies it to the provider API.
type Provider interface {
	// ID returns the provider identifier.
	ID() types.ProviderID

	// SupportedServices returns a list of supported service types.
	// Used by Router to check compatibility.
	SupportedServices() []types.ServiceType

	// Execute sends a request to the provider and returns a response.
	// Uses SanitizedBody (pass-through) to form the API request.
	// The context must support cancellation (timeout).
	Execute(ctx context.Context, req *types.SanitizedRequest) (*types.Response, error)

	// EstimateCost returns a preliminary cost estimate for the request.
	// Used by Budget Guard before sending.
	EstimateCost(req *types.ParsedRequest) (*types.CostEstimate, error)

	// HealthCheck verifies provider availability.
	HealthCheck(ctx context.Context) error
}

// Registry stores and provides access to registered providers.
type Registry interface {
	// Get returns a provider by ID. Error if not registered.
	Get(id types.ProviderID) (Provider, error)

	// GetForService returns providers supporting the specified service type.
	GetForService(serviceType types.ServiceType) []Provider

	// Register adds a provider. Panics on duplicate ID.
	Register(p Provider)

	// All returns all registered providers.
	All() []Provider
}
