/*
 * Copyright (c) 2026 wellwell.work, LLC by Zoe
 *
 * Licensed under the Apache License 2.0 (the "License");
 * You may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     https://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package ethics

import (
	"regexp"
	"strings"
)

// Violation represents a detected privacy violation in text.
type Violation struct {
	Type     string `json:"type"`
	Match    string `json:"match"`
	Position int    `json:"position"`
}

// privacyPattern pairs a violation type with its regex.
type privacyPattern struct {
	Type    string
	Pattern *regexp.Regexp
}

// privacyPatterns defines the set of sensitive data patterns to detect.
var privacyPatterns = []privacyPattern{
	// API keys (generic key patterns)
	{Type: "api_key", Pattern: regexp.MustCompile(`(?i)(api[_-]?key|apikey)\s*[:=]\s*['"]?[A-Za-z0-9_\-]{16,}['"]?`)},
	// AWS access key
	{Type: "aws_key", Pattern: regexp.MustCompile(`AKIA[0-9A-Z]{16}`)},
	// Generic secret/password in assignments
	{Type: "password", Pattern: regexp.MustCompile(`(?i)(password|passwd|pwd)\s*[:=]\s*['"]?[^\s'"]{4,}['"]?`)},
	// Generic secret assignments
	{Type: "secret", Pattern: regexp.MustCompile(`(?i)(secret|secret_key|client_secret)\s*[:=]\s*['"]?[^\s'"]{4,}['"]?`)},
	// Email addresses
	{Type: "email", Pattern: regexp.MustCompile(`[a-zA-Z0-9._%+\-]+@[a-zA-Z0-9.\-]+\.[a-zA-Z]{2,}`)},
	// IPv4 addresses (non-loopback, non-private documentation ranges)
	{Type: "ip_address", Pattern: regexp.MustCompile(`\b(?:(?:25[0-5]|2[0-4]\d|[01]?\d\d?)\.){3}(?:25[0-5]|2[0-4]\d|[01]?\d\d?)\b`)},
	// Private key blocks (PEM format)
	{Type: "private_key", Pattern: regexp.MustCompile(`-----BEGIN\s+(RSA\s+)?PRIVATE\s+KEY-----`)},
	// Bearer tokens
	{Type: "bearer_token", Pattern: regexp.MustCompile(`(?i)Bearer\s+[A-Za-z0-9\-._~+/]+=*`)},
	// JWT tokens (three base64 parts separated by dots)
	{Type: "jwt", Pattern: regexp.MustCompile(`eyJ[A-Za-z0-9_-]{10,}\.eyJ[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_\-]{10,}`)},
	// SSN-like patterns (US Social Security Numbers)
	{Type: "ssn", Pattern: regexp.MustCompile(`\b\d{3}-\d{2}-\d{4}\b`)},
	// Credit card numbers (Visa, Mastercard, Amex patterns)
	{Type: "credit_card", Pattern: regexp.MustCompile(`\b(?:4\d{3}|5[1-5]\d{2}|3[47]\d{2})[\s-]?\d{4}[\s-]?\d{4}[\s-]?\d{4}\b`)},
	// GitHub personal access tokens
	{Type: "github_token", Pattern: regexp.MustCompile(`ghp_[A-Za-z0-9]{36}`)},
}

// PrivacyFilter scans text for sensitive data patterns and can sanitize them.
type PrivacyFilter struct{}

// NewPrivacyFilter creates a new PrivacyFilter.
func NewPrivacyFilter() *PrivacyFilter {
	return &PrivacyFilter{}
}

// Scan checks text for sensitive data patterns and returns all violations found.
func (f *PrivacyFilter) Scan(text string) []Violation {
	var violations []Violation
	for _, pp := range privacyPatterns {
		matches := pp.Pattern.FindAllStringIndex(text, -1)
		for _, loc := range matches {
			violations = append(violations, Violation{
				Type:     pp.Type,
				Match:    text[loc[0]:loc[1]],
				Position: loc[0],
			})
		}
	}
	return violations
}

// Sanitize replaces all detected sensitive data with [REDACTED].
func (f *PrivacyFilter) Sanitize(text string) string {
	result := text
	for _, pp := range privacyPatterns {
		result = pp.Pattern.ReplaceAllStringFunc(result, func(match string) string {
			// Keep the type label for context
			return "[REDACTED:" + strings.ToUpper(pp.Type) + "]"
		})
	}
	return result
}
