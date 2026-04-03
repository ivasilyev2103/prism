package provider_test

import (
	"context"
	"fmt"

	"github.com/helldriver666/prism/internal/provider"
	"github.com/helldriver666/prism/internal/types"
)

// Compile-time checks.
var (
	_ provider.Provider = (*mockProvider)(nil)
	_ provider.Registry = (*mockRegistry)(nil)
)

type mockProvider struct {
	id                types.ProviderID
	supportedServices []types.ServiceType
	executeFn         func(ctx context.Context, req *types.SanitizedRequest) (*types.Response, error)
	estimateCostFn    func(req *types.ParsedRequest) (*types.CostEstimate, error)
	healthCheckFn     func(ctx context.Context) error
}

func (m *mockProvider) ID() types.ProviderID {
	return m.id
}

func (m *mockProvider) SupportedServices() []types.ServiceType {
	return m.supportedServices
}

func (m *mockProvider) Execute(ctx context.Context, req *types.SanitizedRequest) (*types.Response, error) {
	if m.executeFn != nil {
		return m.executeFn(ctx, req)
	}
	return &types.Response{
		ID:         "resp_mock",
		ProviderID: m.id,
	}, nil
}

func (m *mockProvider) EstimateCost(req *types.ParsedRequest) (*types.CostEstimate, error) {
	if m.estimateCostFn != nil {
		return m.estimateCostFn(req)
	}
	return &types.CostEstimate{
		EstimatedUSD: 0.001,
		BillingType:  types.BillingPerToken,
	}, nil
}

func (m *mockProvider) HealthCheck(ctx context.Context) error {
	if m.healthCheckFn != nil {
		return m.healthCheckFn(ctx)
	}
	return nil
}

type mockRegistry struct {
	providers map[types.ProviderID]provider.Provider
}

var _ = newMockRegistry // used in future phase tests

func newMockRegistry() *mockRegistry {
	return &mockRegistry{providers: make(map[types.ProviderID]provider.Provider)}
}

func (m *mockRegistry) Get(id types.ProviderID) (provider.Provider, error) {
	p, ok := m.providers[id]
	if !ok {
		return nil, fmt.Errorf("provider %s not registered", id)
	}
	return p, nil
}

func (m *mockRegistry) GetForService(serviceType types.ServiceType) []provider.Provider {
	var result []provider.Provider
	for _, p := range m.providers {
		for _, st := range p.SupportedServices() {
			if st == serviceType {
				result = append(result, p)
				break
			}
		}
	}
	return result
}

func (m *mockRegistry) Register(p provider.Provider) {
	if _, exists := m.providers[p.ID()]; exists {
		panic(fmt.Sprintf("provider %s already registered", p.ID()))
	}
	m.providers[p.ID()] = p
}

func (m *mockRegistry) All() []provider.Provider {
	result := make([]provider.Provider, 0, len(m.providers))
	for _, p := range m.providers {
		result = append(result, p)
	}
	return result
}
