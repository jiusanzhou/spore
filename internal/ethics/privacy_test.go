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
	"strings"
	"testing"
)

func TestPrivacyFilter_ScanAPIKeys(t *testing.T) {
	f := NewPrivacyFilter()

	tests := []struct {
		name  string
		input string
		want  int // expected number of violations
	}{
		{"api key assignment", `api_key = "sk_live_abcdef1234567890"`, 1},
		{"aws access key", `AKIAIOSFODNN7EXAMPLE`, 1},
		{"password assignment", `password=mysecretpass123`, 1},
		{"secret assignment", `client_secret: "super_secret_value_here"`, 1},
		{"no violations", `hello world this is normal text`, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			violations := f.Scan(tt.input)
			if len(violations) != tt.want {
				t.Errorf("expected %d violations, got %d: %+v", tt.want, len(violations), violations)
			}
		})
	}
}

func TestPrivacyFilter_ScanEmail(t *testing.T) {
	f := NewPrivacyFilter()
	violations := f.Scan("contact me at user@example.com for details")
	if len(violations) != 1 {
		t.Errorf("expected 1 email violation, got %d", len(violations))
	}
	if len(violations) > 0 && violations[0].Type != "email" {
		t.Errorf("expected type 'email', got %q", violations[0].Type)
	}
}

func TestPrivacyFilter_ScanIPAddress(t *testing.T) {
	f := NewPrivacyFilter()
	violations := f.Scan("server at 192.168.1.100 is down")
	if len(violations) != 1 {
		t.Errorf("expected 1 ip_address violation, got %d", len(violations))
	}
}

func TestPrivacyFilter_ScanPrivateKey(t *testing.T) {
	f := NewPrivacyFilter()
	violations := f.Scan("-----BEGIN RSA PRIVATE KEY-----\nMIIEpAIBAAKCAQ...")
	if len(violations) != 1 {
		t.Errorf("expected 1 private_key violation, got %d", len(violations))
	}
}

func TestPrivacyFilter_ScanBearerToken(t *testing.T) {
	f := NewPrivacyFilter()
	violations := f.Scan("Authorization: Bearer eyJhbGciOiJIUzI1NiJ9.test.signature")
	found := false
	for _, v := range violations {
		if v.Type == "bearer_token" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected bearer_token violation, got %+v", violations)
	}
}

func TestPrivacyFilter_ScanJWT(t *testing.T) {
	f := NewPrivacyFilter()
	jwt := "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.dozjgNryP4J3jVmNHl0w5N_XgL0n3I9PlFUP0THsR8U"
	violations := f.Scan(jwt)
	found := false
	for _, v := range violations {
		if v.Type == "jwt" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected jwt violation, got %+v", violations)
	}
}

func TestPrivacyFilter_ScanSSN(t *testing.T) {
	f := NewPrivacyFilter()
	violations := f.Scan("SSN: 123-45-6789")
	if len(violations) != 1 {
		t.Errorf("expected 1 ssn violation, got %d", len(violations))
	}
}

func TestPrivacyFilter_ScanCreditCard(t *testing.T) {
	f := NewPrivacyFilter()
	violations := f.Scan("Card: 4111 1111 1111 1111")
	found := false
	for _, v := range violations {
		if v.Type == "credit_card" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected credit_card violation, got %+v", violations)
	}
}

func TestPrivacyFilter_ScanGitHubToken(t *testing.T) {
	f := NewPrivacyFilter()
	violations := f.Scan("token: ghp_ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghij")
	found := false
	for _, v := range violations {
		if v.Type == "github_token" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected github_token violation, got %+v", violations)
	}
}

func TestPrivacyFilter_Sanitize(t *testing.T) {
	f := NewPrivacyFilter()
	input := `config: api_key = "sk_live_abcdef1234567890" and email user@example.com`
	result := f.Sanitize(input)

	if strings.Contains(result, "sk_live") {
		t.Error("sanitized output still contains API key")
	}
	if strings.Contains(result, "user@example.com") {
		t.Error("sanitized output still contains email")
	}
	if !strings.Contains(result, "[REDACTED:") {
		t.Error("sanitized output missing REDACTED markers")
	}
}

func TestPrivacyFilter_ScanNoFalsePositives(t *testing.T) {
	f := NewPrivacyFilter()
	safe := []string{
		"the weather is nice today",
		"go build ./...",
		"func main() { fmt.Println(42) }",
		"version = 1.0.0",
	}
	for _, s := range safe {
		violations := f.Scan(s)
		if len(violations) > 0 {
			t.Errorf("unexpected violations for %q: %+v", s, violations)
		}
	}
}
