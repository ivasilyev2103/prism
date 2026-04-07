package privacy_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/helldriver666/prism/internal/privacy"
	"github.com/helldriver666/prism/internal/types"
)

func TestSanitize_TextParts_NotMessages(t *testing.T) {
	// Sanitize accepts TextParts and RawBody, not Messages.
	det := privacy.NewRegexDetector()
	p := privacy.NewPipeline(det)

	parts := []types.TextPart{
		{Role: "user", Content: "Email: test@example.com", Index: 0},
	}
	rawBody := json.RawMessage(`{"messages":[{"role":"user","content":"Email: test@example.com"}]}`)

	result, err := p.Sanitize(context.Background(), parts, rawBody, privacy.ProfileModerate, nil)
	if err != nil {
		t.Fatal(err)
	}

	if strings.Contains(result.SanitizedParts[0].Content, "test@example.com") {
		t.Error("expected PII to be replaced in SanitizedParts")
	}
}

func TestSanitize_SanitizedBody_RawBodyWithReplacedText(t *testing.T) {
	det := privacy.NewRegexDetector()
	p := privacy.NewPipeline(det)

	parts := []types.TextPart{
		{Role: "user", Content: "IP: 10.0.0.1", Index: 0},
	}
	rawBody := json.RawMessage(`{"prompt":"IP: 10.0.0.1"}`)

	result, err := p.Sanitize(context.Background(), parts, rawBody, privacy.ProfileModerate, nil)
	if err != nil {
		t.Fatal(err)
	}

	// SanitizedBody should have the IP replaced.
	if strings.Contains(string(result.SanitizedBody), "10.0.0.1") {
		t.Error("expected PII to be replaced in SanitizedBody")
	}
	// SanitizedBody should contain a placeholder.
	if !strings.Contains(string(result.SanitizedBody), "[IP_ADDRESS_") {
		t.Error("expected placeholder in SanitizedBody")
	}
	// SanitizedBody should be valid JSON.
	if !json.Valid(result.SanitizedBody) {
		t.Error("SanitizedBody is not valid JSON")
	}
}

func TestPlaceholderIsolationBetweenRequests(t *testing.T) {
	det := privacy.NewRegexDetector()
	p := privacy.NewPipeline(det)

	parts := []types.TextPart{
		{Role: "user", Content: "test@example.com", Index: 0},
	}

	result1, err := p.Sanitize(context.Background(), parts, nil, privacy.ProfileModerate, nil)
	if err != nil {
		t.Fatal(err)
	}
	result2, err := p.Sanitize(context.Background(), parts, nil, privacy.ProfileModerate, nil)
	if err != nil {
		t.Fatal(err)
	}

	// Two separate calls should produce different placeholder IDs.
	ph1 := result1.SanitizedParts[0].Content
	ph2 := result2.SanitizedParts[0].Content
	if ph1 == ph2 {
		t.Error("expected different placeholder IDs between requests, got identical")
	}
}

func TestRestoreFuncCalledOnce(t *testing.T) {
	det := privacy.NewRegexDetector()
	p := privacy.NewPipeline(det)

	parts := []types.TextPart{
		{Role: "user", Content: "test@example.com", Index: 0},
	}

	result, err := p.Sanitize(context.Background(), parts, nil, privacy.ProfileModerate, nil)
	if err != nil {
		t.Fatal(err)
	}

	placeholder := result.SanitizedParts[0].Content

	// First call: should restore.
	restored := result.RestoreFunc("Response: " + placeholder)
	if !strings.Contains(restored, "test@example.com") {
		t.Error("first RestoreFunc call should restore original value")
	}

	// Second call: Map Table is destroyed, should return input unchanged.
	second := result.RestoreFunc("Response: " + placeholder)
	if strings.Contains(second, "test@example.com") {
		t.Error("second RestoreFunc call should NOT restore (Map Table destroyed)")
	}
	if !strings.Contains(second, placeholder) {
		t.Error("second RestoreFunc call should return input with placeholder intact")
	}
}

func TestBinaryResponse_NoRestoration(t *testing.T) {
	det := privacy.NewRegexDetector()
	p := privacy.NewPipeline(det)

	parts := []types.TextPart{
		{Role: "user", Content: "Draw me alice@example.com", Index: 0},
	}

	result, err := p.Sanitize(context.Background(), parts, nil, privacy.ProfileModerate, nil)
	if err != nil {
		t.Fatal(err)
	}

	// For binary responses, RestoreFunc is not called.
	// The Map Table is destroyed on the first (and only) call, or by GC.
	// Verify that RestoreFunc works for text and doesn't panic with empty input.
	restored := result.RestoreFunc("")
	if restored != "" {
		t.Error("expected empty string for empty text response")
	}
}

func TestRestoreFunc_TextOnly(t *testing.T) {
	det := privacy.NewRegexDetector()
	p := privacy.NewPipeline(det)

	parts := []types.TextPart{
		{Role: "user", Content: "Contact alice@example.com", Index: 0},
	}

	result, err := p.Sanitize(context.Background(), parts, nil, privacy.ProfileModerate, nil)
	if err != nil {
		t.Fatal(err)
	}

	// RestoreFunc should only restore text content.
	placeholder := result.SanitizedParts[0].Content
	response := "Reply to " + strings.TrimPrefix(placeholder, "Contact ")

	restored := result.RestoreFunc(response)
	if !strings.Contains(restored, "alice@example.com") {
		t.Error("RestoreFunc should restore PII in text response")
	}
}

func TestMapTableDestroyedOnPanic(t *testing.T) {
	det := privacy.NewRegexDetector()
	p := privacy.NewPipeline(det)

	parts := []types.TextPart{
		{Role: "user", Content: "test@example.com", Index: 0},
	}

	result, err := p.Sanitize(context.Background(), parts, nil, privacy.ProfileModerate, nil)
	if err != nil {
		t.Fatal(err)
	}

	placeholder := result.SanitizedParts[0].Content

	// Simulate panic during processing — RestoreFunc should still
	// be callable after recovery and return pass-through.
	func() {
		defer func() { recover() }()
		_ = result.RestoreFunc("first call: " + placeholder)
		panic("simulated panic")
	}()

	// After panic recovery, RestoreFunc was already called once,
	// so second call returns input unchanged (Map Table destroyed).
	second := result.RestoreFunc("second: " + placeholder)
	if strings.Contains(second, "test@example.com") {
		t.Error("expected Map Table to be destroyed after first call")
	}
}

func TestMapTableDestroyedOnError(t *testing.T) {
	det := privacy.NewRegexDetector()
	p := privacy.NewPipeline(det)

	parts := []types.TextPart{
		{Role: "user", Content: "test@example.com", Index: 0},
	}

	result, err := p.Sanitize(context.Background(), parts, nil, privacy.ProfileModerate, nil)
	if err != nil {
		t.Fatal(err)
	}

	placeholder := result.SanitizedParts[0].Content

	// First call destroys the Map Table.
	_ = result.RestoreFunc(placeholder)

	// Second call — Map Table is empty.
	got := result.RestoreFunc(placeholder)
	if got != placeholder {
		t.Errorf("expected placeholder returned unchanged, got: %s", got)
	}
}

func TestSanitize_CustomPatterns(t *testing.T) {
	det := privacy.NewRegexDetector()
	p := privacy.NewPipeline(det)

	parts := []types.TextPart{
		{Role: "user", Content: "User ID: USR-12345678", Index: 0},
	}

	customs := []privacy.Pattern{
		{Name: "INTERNAL_USER_ID", Regex: `USR-\d{8}`, Score: 0.95},
	}

	result, err := p.Sanitize(context.Background(), parts, nil, privacy.ProfileModerate, customs)
	if err != nil {
		t.Fatal(err)
	}

	if strings.Contains(result.SanitizedParts[0].Content, "USR-12345678") {
		t.Error("expected custom pattern to replace user ID")
	}
	if !strings.Contains(result.SanitizedParts[0].Content, "[INTERNAL_USER_ID_") {
		t.Error("expected custom pattern placeholder")
	}
}

func TestSanitize_ProfileOff_PassThrough(t *testing.T) {
	det := privacy.NewRegexDetector()
	p := privacy.NewPipeline(det)

	parts := []types.TextPart{
		{Role: "user", Content: "test@example.com", Index: 0},
	}

	result, err := p.Sanitize(context.Background(), parts, nil, privacy.ProfileOff, nil)
	if err != nil {
		t.Fatal(err)
	}

	if result.SanitizedParts[0].Content != "test@example.com" {
		t.Error("ProfileOff should pass through without modification")
	}
	if result.PrivacyScore != 0 {
		t.Error("ProfileOff should have zero privacy score")
	}
}

func TestSanitize_NoPII_PassThrough(t *testing.T) {
	det := privacy.NewRegexDetector()
	p := privacy.NewPipeline(det)

	parts := []types.TextPart{
		{Role: "user", Content: "What is the weather?", Index: 0},
	}

	result, err := p.Sanitize(context.Background(), parts, nil, privacy.ProfileModerate, nil)
	if err != nil {
		t.Fatal(err)
	}

	if result.SanitizedParts[0].Content != "What is the weather?" {
		t.Error("no PII should mean pass-through")
	}
	if len(result.EntitiesFound) != 0 {
		t.Error("expected zero entities")
	}
}

func TestSanitize_EmptyParts(t *testing.T) {
	det := privacy.NewRegexDetector()
	p := privacy.NewPipeline(det)

	result, err := p.Sanitize(context.Background(), nil, nil, privacy.ProfileModerate, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.PrivacyScore != 0 {
		t.Error("empty parts should have zero score")
	}
}

func TestSanitize_MultiplePartsAndRawBody(t *testing.T) {
	det := privacy.NewRegexDetector()
	p := privacy.NewPipeline(det)

	parts := []types.TextPart{
		{Role: "system", Content: "Respond to user@corp.ru", Index: 0},
		{Role: "user", Content: "My SSN is 123-45-6789", Index: 1},
	}
	rawBody := json.RawMessage(`{"messages":[{"role":"system","content":"Respond to user@corp.ru"},{"role":"user","content":"My SSN is 123-45-6789"}]}`)

	result, err := p.Sanitize(context.Background(), parts, rawBody, privacy.ProfileModerate, nil)
	if err != nil {
		t.Fatal(err)
	}

	for _, sp := range result.SanitizedParts {
		if strings.Contains(sp.Content, "user@corp.ru") {
			t.Error("email should be replaced in SanitizedParts")
		}
		if strings.Contains(sp.Content, "123-45-6789") {
			t.Error("SSN should be replaced in SanitizedParts")
		}
	}

	bodyStr := string(result.SanitizedBody)
	if strings.Contains(bodyStr, "user@corp.ru") {
		t.Error("email should be replaced in SanitizedBody")
	}
	if strings.Contains(bodyStr, "123-45-6789") {
		t.Error("SSN should be replaced in SanitizedBody")
	}
	if !json.Valid(result.SanitizedBody) {
		t.Error("SanitizedBody must be valid JSON")
	}

	if result.PrivacyScore <= 0 {
		t.Error("expected non-zero privacy score")
	}
}
