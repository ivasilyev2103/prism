package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/helldriver666/prism/internal/audit"
	"github.com/helldriver666/prism/internal/cost"
	"github.com/helldriver666/prism/internal/ingress"
	"github.com/helldriver666/prism/internal/policy"
	"github.com/helldriver666/prism/internal/types"
)

// buildMux creates the HTTP multiplexer that handles both proxy and management requests.
func buildMux(
	engine *policy.Engine,
	ingressHandler ingress.Handler,
	costTracker cost.Tracker,
	auditLog audit.Logger,
) *http.ServeMux {
	mux := http.NewServeMux()

	// Management endpoints (under /prism/ prefix).
	mux.HandleFunc("/prism/health", handleHealth)
	mux.HandleFunc("/prism/cost/summary", handleCostSummary(costTracker))
	mux.HandleFunc("/prism/cost/quota", handleCostQuota(costTracker))
	mux.HandleFunc("/prism/audit/log", handleAuditLog(auditLog))

	// All other requests go through the proxy pipeline.
	mux.HandleFunc("/", handleProxy(engine, ingressHandler))

	return mux
}

// handleHealth returns the server health status.
func handleHealth(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{
		"status": "ok",
		"time":   time.Now().UTC().Format(time.RFC3339),
	})
}

// handleProxy is the main request handler that routes AI API requests through the pipeline.
func handleProxy(engine *policy.Engine, ingressHandler ingress.Handler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()

		// 1. Ingress: auth, rate limit, parse.
		parsed, err := ingressHandler.Handle(ctx, r)
		if err != nil {
			writeError(w, err)
			return
		}

		// 2. Engine: privacy → route → budget → cache → execute → restore → record.
		resp, err := engine.Process(ctx, parsed)
		if err != nil {
			writeError(w, err)
			return
		}

		// 3. Return response to client.
		writeResponse(w, resp)
	}
}

// handleCostSummary returns cost summary statistics.
func handleCostSummary(tracker cost.Tracker) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		projectID := q.Get("project_id")
		providerID := types.ProviderID(q.Get("provider_id"))

		// Default: last 30 days.
		to := time.Now()
		from := to.AddDate(0, 0, -30)

		if d := q.Get("days"); d != "" {
			if days, err := strconv.Atoi(d); err == nil {
				from = to.AddDate(0, 0, -days)
			}
		}

		summary, err := tracker.Summary(r.Context(), projectID, providerID, from, to)
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error": %q}`, err.Error()), http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(summary)
	}
}

// handleCostQuota returns quota usage for a provider.
func handleCostQuota(tracker cost.Tracker) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		providerID := types.ProviderID(r.URL.Query().Get("provider_id"))
		if providerID == "" {
			http.Error(w, `{"error": "provider_id is required"}`, http.StatusBadRequest)
			return
		}

		quota, err := tracker.QuotaUsage(r.Context(), providerID)
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error": %q}`, err.Error()), http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(quota)
	}
}

// handleAuditLog queries the audit log.
func handleAuditLog(logger audit.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		filter := &audit.Filter{
			ProjectID:   q.Get("project_id"),
			ProviderID:  types.ProviderID(q.Get("provider_id")),
			ServiceType: types.ServiceType(q.Get("service_type")),
			Status:      q.Get("status"),
		}

		if v := q.Get("limit"); v != "" {
			if n, err := strconv.Atoi(v); err == nil {
				filter.Limit = n
			}
		} else {
			filter.Limit = 100
		}
		if v := q.Get("offset"); v != "" {
			if n, err := strconv.Atoi(v); err == nil {
				filter.Offset = n
			}
		}

		// Default: last 24 hours.
		filter.To = time.Now()
		filter.From = filter.To.Add(-24 * time.Hour)

		if v := q.Get("hours"); v != "" {
			if h, err := strconv.Atoi(v); err == nil {
				filter.From = filter.To.Add(-time.Duration(h) * time.Hour)
			}
		}

		records, err := logger.Query(r.Context(), filter)
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error": %q}`, err.Error()), http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(records)
	}
}

// writeError maps domain errors to HTTP status codes.
func writeError(w http.ResponseWriter, err error) {
	w.Header().Set("Content-Type", "application/json")

	msg := err.Error()

	switch {
	case strings.Contains(msg, "missing X-Prism-Token"),
		strings.Contains(msg, "invalid token"):
		w.WriteHeader(http.StatusUnauthorized)
	case strings.Contains(msg, "rate limit"):
		w.WriteHeader(http.StatusTooManyRequests)
	case isBudgetError(err):
		w.WriteHeader(http.StatusTooManyRequests) // 429 for budget exceeded
	case strings.Contains(msg, "all providers failed"):
		w.WriteHeader(http.StatusServiceUnavailable)
	case strings.Contains(msg, "bad request"),
		strings.Contains(msg, "cannot determine service type"):
		w.WriteHeader(http.StatusBadRequest)
	default:
		w.WriteHeader(http.StatusInternalServerError)
	}

	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

// writeResponse sends the provider response back to the client.
func writeResponse(w http.ResponseWriter, resp *types.Response) {
	// If we have raw body from the provider, forward it as-is.
	if len(resp.RawBody) > 0 {
		ct := resp.ContentType
		if ct == "" {
			ct = "application/json"
		}
		w.Header().Set("Content-Type", ct)
		w.WriteHeader(http.StatusOK)
		w.Write(resp.RawBody)
		return
	}

	// If binary data (image, audio, 3D).
	if len(resp.BinaryData) > 0 {
		ct := resp.ContentType
		if ct == "" {
			ct = "application/octet-stream"
		}
		w.Header().Set("Content-Type", ct)
		w.WriteHeader(http.StatusOK)
		w.Write(resp.BinaryData)
		return
	}

	// Text response.
	w.Header().Set("Content-Type", "text/plain")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(resp.TextContent))
}

// isBudgetError checks if the error is a budget or quota exceeded error.
func isBudgetError(err error) bool {
	if err == nil {
		return false
	}
	var budgetErr *types.BudgetExceededError
	var quotaErr *types.QuotaExceededError

	// Use errors.As with the correct import.
	msg := err.Error()
	_ = budgetErr
	_ = quotaErr
	return strings.Contains(msg, "budget") || strings.Contains(msg, "quota")
}

// contextWithTimeout creates a context with a reasonable timeout for proxy requests.
func contextWithTimeout(parent context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(parent, 120*time.Second)
}
