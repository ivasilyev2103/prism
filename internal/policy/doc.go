// Package policy is the request orchestrator for the Prism gateway.
//
// It combines all other modules via interfaces:
//   - Router: selects a provider based on YAML rules, service type, tags, privacy score
//   - BudgetChecker: verifies cost estimates against a 4-level budget hierarchy
//   - Failover: retry + fallback when a provider is unavailable
//   - Engine: full request flow (privacy → route → budget → cache → execute → restore → record)
//
// Engine accepts all dependencies through its constructor. No global state.
package policy
