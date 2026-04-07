package privacy_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/helldriver666/prism/internal/privacy"
	"github.com/helldriver666/prism/internal/types"
)

// --- Golden tests (loaded from testdata/privacy/*.json) ---

type goldenTest struct {
	Description                  string            `json:"description"`
	Profile                      string            `json:"profile"`
	InputParts                   []goldenTextPart   `json:"input_parts"`
	ExpectedOriginalNotInSanit   []string          `json:"expected_original_not_in_sanitized"`
	ExpectedEntities             []goldenEntity     `json:"expected_entities"`
	RestoreCheck                 bool              `json:"restore_check"`
	InjectionResponse            string            `json:"injection_response,omitempty"`
	InjectionNote                string            `json:"injection_note,omitempty"`
}

type goldenTextPart struct {
	Role    string `json:"role"`
	Content string `json:"content"`
	Index   int    `json:"index"`
}

type goldenEntity struct {
	Type     string  `json:"type"`
	ScoreMin float64 `json:"score_min"`
}

func TestGoldenPrivacy(t *testing.T) {
	files, err := filepath.Glob("../../testdata/privacy/*.json")
	if err != nil {
		t.Fatal(err)
	}
	if len(files) == 0 {
		t.Fatal("no golden test files found in testdata/privacy/")
	}

	det := privacy.NewRegexDetector()
	pipe := privacy.NewPipeline(det)

	for _, f := range files {
		name := filepath.Base(f)
		t.Run(name, func(t *testing.T) {
			data, err := os.ReadFile(f)
			if err != nil {
				t.Fatal(err)
			}
			var gt goldenTest
			if err := json.Unmarshal(data, &gt); err != nil {
				t.Fatal(err)
			}

			profile := privacy.Profile(gt.Profile)
			parts := make([]types.TextPart, len(gt.InputParts))
			for i, gp := range gt.InputParts {
				parts[i] = types.TextPart{Role: gp.Role, Content: gp.Content, Index: gp.Index}
			}

			// Build a raw body from parts for testing.
			rawBody := buildRawBody(parts)

			result, err := pipe.Sanitize(context.Background(), parts, rawBody, profile, nil)
			if err != nil {
				t.Fatal(err)
			}

			// Check that expected PII is NOT in sanitized output.
			for _, original := range gt.ExpectedOriginalNotInSanit {
				for _, sp := range result.SanitizedParts {
					if strings.Contains(sp.Content, original) {
						t.Errorf("expected %q to be replaced in SanitizedParts, but found in: %s", original, sp.Content)
					}
				}
				if strings.Contains(string(result.SanitizedBody), jsonEscape(original)) {
					t.Errorf("expected %q to be replaced in SanitizedBody", original)
				}
			}

			// Check entity types are detected.
			foundTypes := make(map[string]float64)
			for _, e := range result.EntitiesFound {
				if existing, ok := foundTypes[e.Type]; !ok || e.Score > existing {
					foundTypes[e.Type] = e.Score
				}
			}
			for _, ge := range gt.ExpectedEntities {
				score, found := foundTypes[ge.Type]
				if !found {
					t.Errorf("expected entity type %s not found", ge.Type)
					continue
				}
				if score < ge.ScoreMin {
					t.Errorf("entity %s score %.2f < expected min %.2f", ge.Type, score, ge.ScoreMin)
				}
			}

			// Check restoration if applicable.
			if gt.RestoreCheck && len(gt.ExpectedOriginalNotInSanit) > 0 {
				// Build a fake response containing the placeholders.
				var placeholders []string
				for _, sp := range result.SanitizedParts {
					placeholders = append(placeholders, sp.Content)
				}
				fakeResponse := strings.Join(placeholders, " ")
				restored := result.RestoreFunc(fakeResponse)

				for _, original := range gt.ExpectedOriginalNotInSanit {
					if !strings.Contains(restored, original) {
						t.Errorf("RestoreFunc should have restored %q in response", original)
					}
				}
			}
		})
	}
}

func TestGolden_PromptInjection(t *testing.T) {
	det := privacy.NewRegexDetector()
	pipe := privacy.NewPipeline(det)

	parts := []types.TextPart{
		{Role: "user", Content: "My email is alice@example.com", Index: 0},
	}

	result, err := pipe.Sanitize(context.Background(), parts, nil, privacy.ProfileModerate, nil)
	if err != nil {
		t.Fatal(err)
	}

	// Extract the reqID from the placeholder.
	placeholder := result.SanitizedParts[0].Content
	// placeholder looks like: "My email is [EMAIL_<reqID>_1]"

	// Simulate a model response with a foreign placeholder.
	injection := fmt.Sprintf("The user is [PERSON_deadbeef_1] and their email is %s",
		extractPlaceholder(placeholder, "EMAIL"))
	restored := result.RestoreFunc(injection)

	// Our placeholder should be restored.
	if !strings.Contains(restored, "alice@example.com") {
		t.Error("expected our EMAIL placeholder to be restored")
	}
	// Foreign placeholder should remain.
	if !strings.Contains(restored, "[PERSON_deadbeef_1]") {
		t.Error("foreign placeholder should NOT be restored")
	}
}

func TestPipeline_DetectorError_PropagatesError(t *testing.T) {
	failing := &mockDetector{
		detectFn: func(_ context.Context, _ string, _ privacy.Profile) ([]privacy.Entity, error) {
			return nil, fmt.Errorf("detector down")
		},
	}
	pipe := privacy.NewPipeline(failing)

	parts := []types.TextPart{
		{Role: "user", Content: "test@example.com", Index: 0},
	}

	_, err := pipe.Sanitize(context.Background(), parts, nil, privacy.ProfileModerate, nil)
	if err == nil {
		t.Fatal("expected error from failing detector")
	}
}

func TestPipeline_PrivacyScore_Calculated(t *testing.T) {
	det := privacy.NewRegexDetector()
	pipe := privacy.NewPipeline(det)

	parts := []types.TextPart{
		{Role: "user", Content: "test@example.com", Index: 0},
	}

	result, err := pipe.Sanitize(context.Background(), parts, nil, privacy.ProfileModerate, nil)
	if err != nil {
		t.Fatal(err)
	}

	if result.PrivacyScore <= 0 {
		t.Error("expected positive privacy score for text with PII")
	}
	if result.PrivacyScore > 1.0 {
		t.Error("privacy score should be <= 1.0")
	}
}

func TestPipeline_ContextHelpers(t *testing.T) {
	ctx := context.Background()

	// No provider set.
	_, ok := privacy.ProviderIDFromContext(ctx)
	if ok {
		t.Error("expected no provider in empty context")
	}

	// Set provider.
	ctx = privacy.WithProviderID(ctx, types.ProviderClaude)
	id, ok := privacy.ProviderIDFromContext(ctx)
	if !ok || id != types.ProviderClaude {
		t.Errorf("expected ProviderClaude, got %v", id)
	}
}

func TestIsLocalProvider(t *testing.T) {
	if !privacy.IsLocalProvider(types.ProviderOllama) {
		t.Error("Ollama should be local")
	}
	if privacy.IsLocalProvider(types.ProviderClaude) {
		t.Error("Claude should not be local")
	}
	if privacy.IsLocalProvider(types.ProviderOpenAI) {
		t.Error("OpenAI should not be local")
	}
}

// --- helpers ---

func buildRawBody(parts []types.TextPart) json.RawMessage {
	type msg struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	}
	var msgs []msg
	for _, p := range parts {
		msgs = append(msgs, msg{Role: p.Role, Content: p.Content})
	}
	body := struct {
		Messages []msg `json:"messages"`
	}{Messages: msgs}
	data, _ := json.Marshal(body)
	return data
}

func jsonEscape(s string) string {
	b, _ := json.Marshal(s)
	return string(b[1 : len(b)-1])
}

func extractPlaceholder(text, entityType string) string {
	start := strings.Index(text, "["+entityType+"_")
	if start < 0 {
		return ""
	}
	end := strings.Index(text[start:], "]")
	if end < 0 {
		return ""
	}
	return text[start : start+end+1]
}
