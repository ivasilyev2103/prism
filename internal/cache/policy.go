package cache

import "github.com/helldriver666/prism/internal/types"

const defaultTTL = 3600 // 1 hour

// DefaultPolicies returns the default per-ServiceType cache policies.
func DefaultPolicies() []CachePolicy {
	return []CachePolicy{
		{ServiceType: types.ServiceChat, Enabled: true, TTL: 3600},
		{ServiceType: types.ServiceEmbedding, Enabled: true, TTL: 86400},
		{ServiceType: types.ServiceImage, Enabled: false},
		{ServiceType: types.ServiceTTS, Enabled: true, TTL: 86400},
		{ServiceType: types.Service3DModel, Enabled: false},
		{ServiceType: types.ServiceSTT, Enabled: false},
		{ServiceType: types.ServiceModeration, Enabled: false},
	}
}

// policyMap provides O(1) lookup of cache policy by service type.
type policyMap map[types.ServiceType]CachePolicy

func buildPolicyMap(policies []CachePolicy) policyMap {
	m := make(policyMap, len(policies))
	for _, p := range policies {
		m[p.ServiceType] = p
	}
	return m
}

// isEnabled returns whether caching is enabled for the given service type.
func (pm policyMap) isEnabled(svc types.ServiceType) bool {
	p, ok := pm[svc]
	return ok && p.Enabled
}

// ttl returns the TTL for the given service type, or defaultTTL.
func (pm policyMap) ttl(svc types.ServiceType) int {
	if p, ok := pm[svc]; ok && p.TTL > 0 {
		return p.TTL
	}
	return defaultTTL
}
