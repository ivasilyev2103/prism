package privacy_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/helldriver666/prism/internal/privacy"
)

func TestOllamaDetector_Execute_Success(t *testing.T) {
	entities := []struct {
		Type  string `json:"type"`
		Value string `json:"value"`
		Start int    `json:"start"`
		End   int    `json:"end"`
	}{
		{Type: "PERSON", Value: "John Smith", Start: 8, End: 18},
	}
	respJSON, _ := json.Marshal(entities)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/generate" {
			json.NewEncoder(w).Encode(map[string]string{
				"response": string(respJSON),
			})
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	det := privacy.NewOllamaDetector(srv.URL, "test-model")
	result, err := det.Detect(context.Background(), "Contact John Smith for info", privacy.ProfileModerate)
	if err != nil {
		t.Fatal(err)
	}
	if len(result) != 1 {
		t.Fatalf("expected 1 entity, got %d", len(result))
	}
	if result[0].Type != "PERSON" || result[0].Value != "John Smith" {
		t.Errorf("unexpected entity: %+v", result[0])
	}
}

func TestOllamaDetector_Execute_EmptyResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]string{
			"response": "[]",
		})
	}))
	defer srv.Close()

	det := privacy.NewOllamaDetector(srv.URL, "test-model")
	result, err := det.Detect(context.Background(), "No PII here", privacy.ProfileModerate)
	if err != nil {
		t.Fatal(err)
	}
	if len(result) != 0 {
		t.Errorf("expected 0 entities, got %d", len(result))
	}
}

func TestOllamaDetector_Execute_MalformedJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]string{
			"response": "this is not JSON at all",
		})
	}))
	defer srv.Close()

	det := privacy.NewOllamaDetector(srv.URL, "test-model")
	result, err := det.Detect(context.Background(), "test@example.com", privacy.ProfileModerate)
	// Malformed JSON from LLM is treated as no entities — not an error.
	if err != nil {
		t.Fatal("expected no error for malformed LLM response, got:", err)
	}
	if len(result) != 0 {
		t.Errorf("expected 0 entities for malformed response, got %d", len(result))
	}
}

func TestOllamaDetector_Execute_ServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("internal error"))
	}))
	defer srv.Close()

	det := privacy.NewOllamaDetector(srv.URL, "test-model")
	_, err := det.Detect(context.Background(), "test", privacy.ProfileModerate)
	if err == nil {
		t.Fatal("expected error for 500 response")
	}
}

func TestOllamaDetector_Execute_ServerUnavailable(t *testing.T) {
	det := privacy.NewOllamaDetector("http://127.0.0.1:1", "test-model")
	_, err := det.Detect(context.Background(), "test", privacy.ProfileModerate)
	if err == nil {
		t.Fatal("expected error for unavailable server")
	}
}

func TestOllamaDetector_HealthCheck_OK(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/tags" {
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	det := privacy.NewOllamaDetector(srv.URL, "test-model")
	if err := det.HealthCheck(context.Background()); err != nil {
		t.Fatal("expected healthy:", err)
	}
}

func TestOllamaDetector_HealthCheck_Unavailable(t *testing.T) {
	det := privacy.NewOllamaDetector("http://127.0.0.1:1", "test-model")
	if err := det.HealthCheck(context.Background()); err == nil {
		t.Fatal("expected health check failure")
	}
}

func TestOllamaDetector_ProfileOff(t *testing.T) {
	det := privacy.NewOllamaDetector("http://127.0.0.1:1", "test-model")
	result, err := det.Detect(context.Background(), "test@example.com", privacy.ProfileOff)
	if err != nil {
		t.Fatal(err)
	}
	if len(result) != 0 {
		t.Error("ProfileOff should return nil entities")
	}
}

func TestOllamaDetector_PositionValidation(t *testing.T) {
	// Entity with wrong positions — should be found by string search fallback.
	entities := []struct {
		Type  string `json:"type"`
		Value string `json:"value"`
		Start int    `json:"start"`
		End   int    `json:"end"`
	}{
		{Type: "PERSON", Value: "Alice", Start: 999, End: 1004}, // wrong positions
	}
	respJSON, _ := json.Marshal(entities)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]string{
			"response": string(respJSON),
		})
	}))
	defer srv.Close()

	det := privacy.NewOllamaDetector(srv.URL, "test-model")
	result, err := det.Detect(context.Background(), "Contact Alice now", privacy.ProfileModerate)
	if err != nil {
		t.Fatal(err)
	}
	if len(result) != 1 {
		t.Fatalf("expected 1 entity, got %d", len(result))
	}
	// Position should be corrected via string search.
	if result[0].Start != 8 || result[0].End != 13 {
		t.Errorf("expected corrected positions [8:13], got [%d:%d]", result[0].Start, result[0].End)
	}
}
