package privacy

import (
	"context"
	"regexp"
)

type regexPattern struct {
	entityType string
	pattern    *regexp.Regexp
	score      float64
	validate   func(string) bool // optional post-match validation
}

type regexDetector struct {
	patterns []regexPattern
}

// NewRegexDetector creates a built-in Go PII detector (Tier 1).
// Zero external dependencies. Always available.
// Covers: EMAIL, PHONE, CREDIT_CARD, SSN, IBAN, IP_ADDRESS.
func NewRegexDetector() Detector {
	return &regexDetector{
		patterns: defaultPatterns(),
	}
}

func defaultPatterns() []regexPattern {
	return []regexPattern{
		{
			entityType: "EMAIL",
			pattern:    regexp.MustCompile(`[a-zA-Z0-9._%+\-]+@[a-zA-Z0-9.\-]+\.[a-zA-Z]{2,}`),
			score:      0.99,
		},
		{
			entityType: "PHONE",
			pattern: regexp.MustCompile(
				`(?:\+\d{1,3}[\s\-]?\(?\d{3,4}\)?[\s.\-]?\d{3,4}[\s.\-]?\d{2,4}(?:[\s.\-]?\d{2,4})?)` +
					`|(?:8[\s\-]?\(?\d{3}\)?[\s.\-]?\d{3}[\s.\-]?\d{2}[\s.\-]?\d{2})` +
					`|(?:\(\d{3}\)\s?\d{3}[\s\-]?\d{4})`),
			score:    0.90,
			validate: validatePhone,
		},
		{
			entityType: "CREDIT_CARD",
			pattern: regexp.MustCompile(
				`\b\d{4}[\s\-]\d{4}[\s\-]\d{4}[\s\-]\d{4}\b` +
					`|\b\d{4}[\s\-]\d{6}[\s\-]\d{5}\b` +
					`|\b\d{13,19}\b`),
			score:    0.95,
			validate: validateCreditCard,
		},
		{
			entityType: "SSN",
			pattern:    regexp.MustCompile(`\b\d{3}-\d{2}-\d{4}\b`),
			score:      0.95,
		},
		{
			entityType: "IBAN",
			pattern:    regexp.MustCompile(`\b[A-Z]{2}\d{2}\s?[A-Z0-9]{4}(?:\s?[A-Z0-9]{4}){1,7}(?:\s?[A-Z0-9]{1,4})?\b`),
			score:      0.95,
		},
		{
			entityType: "IP_ADDRESS",
			pattern:    regexp.MustCompile(`\b(?:(?:25[0-5]|2[0-4]\d|[01]?\d\d?)\.){3}(?:25[0-5]|2[0-4]\d|[01]?\d\d?)\b`),
			score:      0.90,
		},
	}
}

func (d *regexDetector) Detect(_ context.Context, text string, profile Profile) ([]Entity, error) {
	if profile == ProfileOff {
		return nil, nil
	}

	var entities []Entity
	for _, p := range d.patterns {
		locs := p.pattern.FindAllStringIndex(text, -1)
		for _, loc := range locs {
			value := text[loc[0]:loc[1]]
			if p.validate != nil && !p.validate(value) {
				continue
			}
			entities = append(entities, Entity{
				Type:  p.entityType,
				Value: value,
				Score: p.score,
				Start: loc[0],
				End:   loc[1],
			})
		}
	}
	return entities, nil
}

func (d *regexDetector) HealthCheck(_ context.Context) error {
	return nil // Always healthy — built-in, no external dependencies.
}

// validatePhone checks that a matched string contains 10–15 digits.
func validatePhone(s string) bool {
	digits := 0
	for _, c := range s {
		if c >= '0' && c <= '9' {
			digits++
		}
	}
	return digits >= 10 && digits <= 15
}

// validateCreditCard applies the Luhn algorithm to filter false positives.
func validateCreditCard(s string) bool {
	var digits []int
	for _, c := range s {
		if c >= '0' && c <= '9' {
			digits = append(digits, int(c-'0'))
		}
	}
	if len(digits) < 13 || len(digits) > 19 {
		return false
	}
	sum := 0
	alt := false
	for i := len(digits) - 1; i >= 0; i-- {
		d := digits[i]
		if alt {
			d *= 2
			if d > 9 {
				d -= 9
			}
		}
		sum += d
		alt = !alt
	}
	return sum%10 == 0
}
