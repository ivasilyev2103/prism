package policy

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/helldriver666/prism/internal/provider"
	"github.com/helldriver666/prism/internal/types"
)

// routeConfig is the top-level YAML structure.
type routeConfig struct {
	Routes []routeRule `yaml:"routes"`
}

type routeRule struct {
	Name string     `yaml:"name"`
	If   *condition `yaml:"if,omitempty"`
	Then action     `yaml:"then"`
}

type condition struct {
	ServiceType  string   `yaml:"service_type,omitempty"`
	PrivacyScore string   `yaml:"privacy_score,omitempty"` // e.g., ">0.7"
	Tags         []string `yaml:"tags,omitempty"`
	ProjectID    string   `yaml:"project_id,omitempty"`
}

type action struct {
	Provider  string `yaml:"provider"`
	Model     string `yaml:"model,omitempty"`
	Fallback  string `yaml:"fallback,omitempty"`
	MaxTokens int    `yaml:"max_tokens,omitempty"`
}

type router struct {
	rules    []routeRule
	registry provider.Registry
}

// NewRouter creates a Router from YAML configuration and a provider registry.
func NewRouter(configYAML []byte, registry provider.Registry) (Router, error) {
	var cfg routeConfig
	if err := yaml.Unmarshal(configYAML, &cfg); err != nil {
		return nil, fmt.Errorf("policy: parse routes.yaml: %w", err)
	}
	if len(cfg.Routes) == 0 {
		return nil, fmt.Errorf("policy: routes.yaml has no rules")
	}
	return &router{rules: cfg.Routes, registry: registry}, nil
}

func (r *router) Route(_ context.Context, req *types.SanitizedRequest) (*RoutingDecision, error) {
	for _, rule := range r.rules {
		if !matchRule(rule, req) {
			continue
		}

		providerID := types.ProviderID(rule.Then.Provider)

		// Check provider supports the requested service type.
		p, err := r.registry.Get(providerID)
		if err != nil {
			return nil, fmt.Errorf("policy: provider %q not registered: %w", providerID, err)
		}
		if !supportsService(p, req.ServiceType) {
			return nil, fmt.Errorf("policy: provider %q does not support service %q (rule %q)",
				providerID, req.ServiceType, rule.Name)
		}

		return &RoutingDecision{
			ProviderID:    providerID,
			ModelOverride: rule.Then.Model,
			RuleName:      rule.Name,
			FallbackID:    types.ProviderID(rule.Then.Fallback),
		}, nil
	}

	return nil, fmt.Errorf("policy: no matching route for service=%s project=%s", req.ServiceType, req.ProjectID)
}

// matchRule checks whether a request satisfies all conditions of a rule.
// A rule with no conditions (If == nil) is a catch-all.
func matchRule(rule routeRule, req *types.SanitizedRequest) bool {
	if rule.If == nil {
		return true // catch-all
	}
	c := rule.If

	if c.ServiceType != "" && c.ServiceType != string(req.ServiceType) {
		return false
	}
	if c.ProjectID != "" && c.ProjectID != req.ProjectID {
		return false
	}
	if c.PrivacyScore != "" && !matchPrivacyScore(c.PrivacyScore, req.PrivacyScore) {
		return false
	}
	if len(c.Tags) > 0 && !matchTags(c.Tags, req.Tags) {
		return false
	}
	return true
}

// matchPrivacyScore evaluates expressions like ">0.7", ">=0.5".
func matchPrivacyScore(expr string, score float64) bool {
	if len(expr) < 2 {
		return false
	}
	var op string
	var valStr string
	if strings.HasPrefix(expr, ">=") {
		op, valStr = ">=", expr[2:]
	} else if strings.HasPrefix(expr, "<=") {
		op, valStr = "<=", expr[2:]
	} else if strings.HasPrefix(expr, ">") {
		op, valStr = ">", expr[1:]
	} else if strings.HasPrefix(expr, "<") {
		op, valStr = "<", expr[1:]
	} else {
		return false
	}

	threshold, err := strconv.ParseFloat(valStr, 64)
	if err != nil {
		return false
	}

	switch op {
	case ">":
		return score > threshold
	case ">=":
		return score >= threshold
	case "<":
		return score < threshold
	case "<=":
		return score <= threshold
	default:
		return false
	}
}

// matchTags checks if any of the required tags are present in the request.
func matchTags(required, actual []string) bool {
	set := make(map[string]bool, len(actual))
	for _, t := range actual {
		set[t] = true
	}
	for _, t := range required {
		if set[t] {
			return true
		}
	}
	return false
}

func supportsService(p provider.Provider, svc types.ServiceType) bool {
	for _, s := range p.SupportedServices() {
		if s == svc {
			return true
		}
	}
	return false
}
