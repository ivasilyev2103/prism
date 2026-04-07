package privacy_test

import (
	"context"
	"errors"
	"testing"

	"github.com/helldriver666/prism/internal/privacy"
	"github.com/helldriver666/prism/internal/types"
)

// --- RegexDetector tests ---

func TestRegexDetector_Email(t *testing.T) {
	d := privacy.NewRegexDetector()
	entities, err := d.Detect(context.Background(), "Contact us at hello@example.com for info", privacy.ProfileModerate)
	if err != nil {
		t.Fatal(err)
	}
	assertContainsEntity(t, entities, "EMAIL", "hello@example.com")
}

func TestRegexDetector_Phone_International(t *testing.T) {
	d := privacy.NewRegexDetector()
	entities, err := d.Detect(context.Background(), "Call +7 (999) 123-45-67 now", privacy.ProfileModerate)
	if err != nil {
		t.Fatal(err)
	}
	assertContainsEntity(t, entities, "PHONE", "+7 (999) 123-45-67")
}

func TestRegexDetector_Phone_USLocal(t *testing.T) {
	d := privacy.NewRegexDetector()
	entities, err := d.Detect(context.Background(), "Dial (234) 567-8901 for support", privacy.ProfileModerate)
	if err != nil {
		t.Fatal(err)
	}
	assertContainsEntity(t, entities, "PHONE", "(234) 567-8901")
}

func TestRegexDetector_CreditCard_Spaces(t *testing.T) {
	d := privacy.NewRegexDetector()
	entities, err := d.Detect(context.Background(), "Card: 4111 1111 1111 1111", privacy.ProfileModerate)
	if err != nil {
		t.Fatal(err)
	}
	assertContainsEntity(t, entities, "CREDIT_CARD", "4111 1111 1111 1111")
}

func TestRegexDetector_CreditCard_Dashes(t *testing.T) {
	d := privacy.NewRegexDetector()
	entities, err := d.Detect(context.Background(), "Card: 5500-0000-0000-0004", privacy.ProfileModerate)
	if err != nil {
		t.Fatal(err)
	}
	assertContainsEntity(t, entities, "CREDIT_CARD", "5500-0000-0000-0004")
}

func TestRegexDetector_CreditCard_InvalidLuhn(t *testing.T) {
	d := privacy.NewRegexDetector()
	entities, err := d.Detect(context.Background(), "Card: 1234 5678 9012 3456", privacy.ProfileModerate)
	if err != nil {
		t.Fatal(err)
	}
	// 1234567890123456 fails Luhn check — should not be detected.
	for _, e := range entities {
		if e.Type == "CREDIT_CARD" && e.Value == "1234 5678 9012 3456" {
			t.Error("expected invalid Luhn card to be filtered out")
		}
	}
}

func TestRegexDetector_SSN(t *testing.T) {
	d := privacy.NewRegexDetector()
	entities, err := d.Detect(context.Background(), "SSN: 123-45-6789", privacy.ProfileModerate)
	if err != nil {
		t.Fatal(err)
	}
	assertContainsEntity(t, entities, "SSN", "123-45-6789")
}

func TestRegexDetector_IBAN(t *testing.T) {
	d := privacy.NewRegexDetector()
	entities, err := d.Detect(context.Background(), "IBAN: DE89 3704 0044 0532 0130 00", privacy.ProfileModerate)
	if err != nil {
		t.Fatal(err)
	}
	assertContainsEntity(t, entities, "IBAN", "DE89 3704 0044 0532 0130 00")
}

func TestRegexDetector_IPAddress(t *testing.T) {
	d := privacy.NewRegexDetector()
	entities, err := d.Detect(context.Background(), "Server at 192.168.1.100", privacy.ProfileModerate)
	if err != nil {
		t.Fatal(err)
	}
	assertContainsEntity(t, entities, "IP_ADDRESS", "192.168.1.100")
}

func TestRegexDetector_ProfileOff_ReturnsNil(t *testing.T) {
	d := privacy.NewRegexDetector()
	entities, err := d.Detect(context.Background(), "hello@example.com", privacy.ProfileOff)
	if err != nil {
		t.Fatal(err)
	}
	if len(entities) != 0 {
		t.Errorf("expected no entities for ProfileOff, got %d", len(entities))
	}
}

func TestRegexDetector_HealthCheck_AlwaysHealthy(t *testing.T) {
	d := privacy.NewRegexDetector()
	if err := d.HealthCheck(context.Background()); err != nil {
		t.Fatal("expected RegexDetector.HealthCheck to always succeed:", err)
	}
}

func TestRegexDetector_StructuredPII(t *testing.T) {
	d := privacy.NewRegexDetector()
	text := "Email: test@mail.ru, IP: 10.0.0.1, SSN: 078-05-1120"
	entities, err := d.Detect(context.Background(), text, privacy.ProfileStrict)
	if err != nil {
		t.Fatal(err)
	}
	types := entityTypes(entities)
	for _, want := range []string{"EMAIL", "IP_ADDRESS", "SSN"} {
		if !types[want] {
			t.Errorf("expected entity type %s not found", want)
		}
	}
}

func TestRegexDetector_NoPII(t *testing.T) {
	d := privacy.NewRegexDetector()
	entities, err := d.Detect(context.Background(), "What is the weather like today?", privacy.ProfileModerate)
	if err != nil {
		t.Fatal(err)
	}
	if len(entities) != 0 {
		t.Errorf("expected no entities, got %d: %v", len(entities), entities)
	}
}

// --- CompositeDetector tests ---

func TestCompositeDetector_MergesResults(t *testing.T) {
	d1 := &mockDetector{
		detectFn: func(_ context.Context, _ string, _ privacy.Profile) ([]privacy.Entity, error) {
			return []privacy.Entity{
				{Type: "EMAIL", Value: "a@b.com", Score: 0.99, Start: 0, End: 7},
			}, nil
		},
	}
	d2 := &mockDetector{
		detectFn: func(_ context.Context, _ string, _ privacy.Profile) ([]privacy.Entity, error) {
			return []privacy.Entity{
				{Type: "PERSON", Value: "John", Score: 0.85, Start: 10, End: 14},
			}, nil
		},
	}

	cd := privacy.NewCompositeDetector(d1, d2)
	entities, err := cd.Detect(context.Background(), "a@b.com -- John", privacy.ProfileModerate)
	if err != nil {
		t.Fatal(err)
	}
	if len(entities) != 2 {
		t.Fatalf("expected 2 merged entities, got %d", len(entities))
	}
}

func TestCompositeDetector_MaxScorePerOverlap(t *testing.T) {
	d1 := &mockDetector{
		detectFn: func(_ context.Context, _ string, _ privacy.Profile) ([]privacy.Entity, error) {
			return []privacy.Entity{
				{Type: "SSN", Value: "123-45-6789", Score: 0.95, Start: 0, End: 11},
			}, nil
		},
	}
	d2 := &mockDetector{
		detectFn: func(_ context.Context, _ string, _ privacy.Profile) ([]privacy.Entity, error) {
			return []privacy.Entity{
				{Type: "PHONE", Value: "123-45-6789", Score: 0.80, Start: 0, End: 11},
			}, nil
		},
	}

	cd := privacy.NewCompositeDetector(d1, d2)
	entities, err := cd.Detect(context.Background(), "123-45-6789", privacy.ProfileModerate)
	if err != nil {
		t.Fatal(err)
	}
	if len(entities) != 1 {
		t.Fatalf("expected 1 entity after merge, got %d", len(entities))
	}
	if entities[0].Score != 0.95 {
		t.Errorf("expected max score 0.95, got %f", entities[0].Score)
	}
}

func TestCompositeDetector_RegexOnly_NoDependencies(t *testing.T) {
	// Tier 1: RegexDetector only — zero external dependencies.
	cd := privacy.NewCompositeDetector(privacy.NewRegexDetector())
	entities, err := cd.Detect(context.Background(), "Email: test@example.com", privacy.ProfileModerate)
	if err != nil {
		t.Fatal(err)
	}
	assertContainsEntity(t, entities, "EMAIL", "test@example.com")
}

func TestCompositeDetector_PropagatesError(t *testing.T) {
	failing := &mockDetector{
		detectFn: func(_ context.Context, _ string, _ privacy.Profile) ([]privacy.Entity, error) {
			return nil, errors.New("detector down")
		},
	}
	cd := privacy.NewCompositeDetector(privacy.NewRegexDetector(), failing)
	_, err := cd.Detect(context.Background(), "test@example.com", privacy.ProfileModerate)
	if err == nil {
		t.Fatal("expected error from failing detector")
	}
}

func TestCompositeDetector_HealthCheck(t *testing.T) {
	healthy := &mockDetector{}
	failing := &mockDetector{
		healthCheckFn: func(_ context.Context) error { return errors.New("down") },
	}

	cd := privacy.NewCompositeDetector(healthy, failing)
	if err := cd.HealthCheck(context.Background()); err == nil {
		t.Fatal("expected HealthCheck to return error")
	}
}

// --- HandleDetectorError tests ---

func TestFailClosed_CloudProvider(t *testing.T) {
	err := errors.New("detector unavailable")
	for _, provider := range []string{"claude", "openai", "gemini"} {
		result := privacy.HandleDetectorError(err, types.ProviderID(provider))
		if result == nil {
			t.Errorf("expected fail-closed for cloud provider %s", provider)
		}
	}
}

func TestPassThrough_LocalProvider(t *testing.T) {
	err := errors.New("detector unavailable")
	result := privacy.HandleDetectorError(err, types.ProviderOllama)
	if result != nil {
		t.Errorf("expected pass-through for local provider, got error: %v", result)
	}
}

func TestHandleDetectorError_NilError(t *testing.T) {
	result := privacy.HandleDetectorError(nil, types.ProviderClaude)
	if result != nil {
		t.Errorf("expected nil for nil error, got: %v", result)
	}
}

// --- helpers ---

func assertContainsEntity(t *testing.T, entities []privacy.Entity, entityType, value string) {
	t.Helper()
	for _, e := range entities {
		if e.Type == entityType && e.Value == value {
			return
		}
	}
	t.Errorf("expected entity type=%s value=%q not found in %v", entityType, value, entities)
}

func entityTypes(entities []privacy.Entity) map[string]bool {
	m := make(map[string]bool)
	for _, e := range entities {
		m[e.Type] = true
	}
	return m
}
