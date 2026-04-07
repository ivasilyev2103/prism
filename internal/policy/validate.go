package policy

import (
	"fmt"

	"github.com/helldriver666/prism/internal/types"
)

// validServiceTypes is the set of known ServiceType values.
var validServiceTypes = map[string]bool{
	string(types.ServiceChat):       true,
	string(types.ServiceImage):      true,
	string(types.ServiceEmbedding):  true,
	string(types.ServiceTTS):        true,
	string(types.Service3DModel):    true,
	string(types.ServiceModeration): true,
	string(types.ServiceSTT):        true,
}

func (r *router) Validate() []error {
	var errs []error

	catchAllSeen := false
	for i, rule := range r.rules {
		// 1. Check for unreachable rules (after a catch-all).
		if catchAllSeen {
			errs = append(errs, fmt.Errorf("rule %q (#%d) is unreachable: follows catch-all rule", rule.Name, i+1))
		}
		if rule.If == nil {
			catchAllSeen = true
		}

		// 2. Check provider exists in registry.
		providerID := types.ProviderID(rule.Then.Provider)
		p, err := r.registry.Get(providerID)
		if err != nil {
			errs = append(errs, fmt.Errorf("rule %q: unknown provider %q", rule.Name, rule.Then.Provider))
		}

		// 3. Check fallback provider exists.
		if rule.Then.Fallback != "" {
			if _, err := r.registry.Get(types.ProviderID(rule.Then.Fallback)); err != nil {
				errs = append(errs, fmt.Errorf("rule %q: unknown fallback %q", rule.Name, rule.Then.Fallback))
			}
		}

		// 4. Check service_type is valid (if specified in condition).
		if rule.If != nil && rule.If.ServiceType != "" {
			if !validServiceTypes[rule.If.ServiceType] {
				errs = append(errs, fmt.Errorf("rule %q: unknown service_type %q", rule.Name, rule.If.ServiceType))
			}

			// 5. Check provider supports the service_type.
			if p != nil {
				svc := types.ServiceType(rule.If.ServiceType)
				if !supportsService(p, svc) {
					errs = append(errs, fmt.Errorf("rule %q: provider %q does not support service_type %q",
						rule.Name, rule.Then.Provider, rule.If.ServiceType))
				}
			}
		}
	}

	// 6. Check there is a default (catch-all) route.
	if !catchAllSeen {
		errs = append(errs, fmt.Errorf("no default (catch-all) route: add a rule without 'if' conditions"))
	}

	return errs
}
