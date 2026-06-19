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

package mcp

import (
	"strings"
	"testing"

	mcpspec "github.com/mark3labs/mcp-go/mcp"
)

func TestServerConfigValidate(t *testing.T) {
	tests := []struct {
		name    string
		cfg     ServerConfig
		wantErr string
	}{
		{
			name: "stdio ok",
			cfg:  ServerConfig{Transport: "stdio", Command: "echo"},
		},
		{
			name: "stdio default empty transport",
			cfg:  ServerConfig{Command: "echo"},
		},
		{
			name:    "stdio without command",
			cfg:     ServerConfig{Transport: "stdio"},
			wantErr: "stdio transport requires command",
		},
		{
			name: "http ok",
			cfg:  ServerConfig{Transport: "http", URL: "http://x"},
		},
		{
			name:    "http without url",
			cfg:     ServerConfig{Transport: "http"},
			wantErr: "http transport requires url",
		},
		{
			name:    "unknown transport",
			cfg:     ServerConfig{Transport: "carrierpigeon"},
			wantErr: "unknown transport",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.cfg.validate("test")
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("error %q does not contain %q", err.Error(), tt.wantErr)
			}
		})
	}
}

func TestNormalizedTransport(t *testing.T) {
	cases := map[string]string{
		"":         "stdio",
		"stdio":    "stdio",
		"STDIO":    "stdio",
		" stdio ": "stdio",
		"http":     "http",
		"sse":      "sse",
	}
	for in, want := range cases {
		got := ServerConfig{Transport: in}.normalizedTransport()
		if got != want {
			t.Errorf("normalizedTransport(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestConfigDefaults(t *testing.T) {
	c := Config{}.withDefaults()
	if c.ToolPrefix != "mcp" {
		t.Errorf("ToolPrefix default = %q, want \"mcp\"", c.ToolPrefix)
	}
	if c.InitTimeoutSeconds != 15 {
		t.Errorf("InitTimeoutSeconds default = %d, want 15", c.InitTimeoutSeconds)
	}
	if c.CallTimeoutSeconds != 60 {
		t.Errorf("CallTimeoutSeconds default = %d, want 60", c.CallTimeoutSeconds)
	}
	// Existing values should be preserved.
	c2 := Config{ToolPrefix: "x", InitTimeoutSeconds: 1, CallTimeoutSeconds: 2}.withDefaults()
	if c2.ToolPrefix != "x" || c2.InitTimeoutSeconds != 1 || c2.CallTimeoutSeconds != 2 {
		t.Errorf("withDefaults overwrote non-zero values: %+v", c2)
	}
}

func TestBuildEngineName(t *testing.T) {
	cases := []struct {
		prefix string
		server string
		tool   string
		want   string
	}{
		{"mcp", "fs", "read_file", "mcp:fs:read_file"},
		{"", "fs", "read_file", "fs:read_file"},
		{"mcp", "", "read_file", "mcp:read_file"},
		{"mcp", "fs", "", "mcp:fs"},
	}
	for _, c := range cases {
		got := buildEngineName(c.prefix, c.server, c.tool)
		if got != c.want {
			t.Errorf("buildEngineName(%q, %q, %q) = %q, want %q",
				c.prefix, c.server, c.tool, got, c.want)
		}
	}
}

func TestParseArgs(t *testing.T) {
	// Build a tool whose schema has one required string field.
	schema := mcpspec.ToolInputSchema{
		Type:     "object",
		Required: []string{"path"},
		Properties: map[string]any{
			"path": map[string]any{"type": "string"},
		},
	}
	tl := &tool{inputSchema: schema}

	t.Run("empty input", func(t *testing.T) {
		args, err := tl.parseArgs("")
		if err != nil {
			t.Fatal(err)
		}
		if len(args) != 0 {
			t.Errorf("want empty map, got %v", args)
		}
	})

	t.Run("json object", func(t *testing.T) {
		args, err := tl.parseArgs(`{"path":"/etc/hosts","limit":10}`)
		if err != nil {
			t.Fatal(err)
		}
		if args["path"] != "/etc/hosts" {
			t.Errorf("path = %v, want /etc/hosts", args["path"])
		}
		// JSON numbers decode as float64
		if args["limit"].(float64) != 10 {
			t.Errorf("limit = %v, want 10", args["limit"])
		}
	})

	t.Run("bare string coerces to single required field", func(t *testing.T) {
		args, err := tl.parseArgs("/etc/hosts")
		if err != nil {
			t.Fatal(err)
		}
		if args["path"] != "/etc/hosts" {
			t.Errorf("expected coercion to {path: /etc/hosts}, got %v", args)
		}
	})

	t.Run("bare string with no coercion fallback", func(t *testing.T) {
		// Schema with no single required string field
		tl2 := &tool{inputSchema: mcpspec.ToolInputSchema{Type: "object"}}
		args, err := tl2.parseArgs("hello world")
		if err != nil {
			t.Fatal(err)
		}
		if args["input"] != "hello world" {
			t.Errorf("fallback should put under 'input', got %v", args)
		}
	})

	t.Run("invalid json falls through to coercion", func(t *testing.T) {
		args, err := tl.parseArgs("{not valid json")
		if err != nil {
			t.Fatal(err)
		}
		// Should fall back to coercion since schema has 1 required string
		if args["path"] != "{not valid json" {
			t.Errorf("expected {path: '{not valid json'}, got %v", args)
		}
	})
}

func TestSingleRequiredStringField(t *testing.T) {
	cases := []struct {
		name   string
		schema mcpspec.ToolInputSchema
		want   string
	}{
		{
			name: "single required string",
			schema: mcpspec.ToolInputSchema{
				Required:   []string{"path"},
				Properties: map[string]any{"path": map[string]any{"type": "string"}},
			},
			want: "path",
		},
		{
			name: "single required, type missing → trust required",
			schema: mcpspec.ToolInputSchema{
				Required:   []string{"path"},
				Properties: map[string]any{},
			},
			want: "path",
		},
		{
			name: "single required but not string",
			schema: mcpspec.ToolInputSchema{
				Required:   []string{"count"},
				Properties: map[string]any{"count": map[string]any{"type": "integer"}},
			},
			want: "",
		},
		{
			name: "two required",
			schema: mcpspec.ToolInputSchema{
				Required: []string{"a", "b"},
			},
			want: "",
		},
		{
			name:   "no required",
			schema: mcpspec.ToolInputSchema{},
			want:   "",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := singleRequiredStringField(c.schema)
			if got != c.want {
				t.Errorf("got %q, want %q", got, c.want)
			}
		})
	}
}

func TestRenderToolResult(t *testing.T) {
	t.Run("nil result", func(t *testing.T) {
		if got := renderToolResult(nil); got != "" {
			t.Errorf("nil → %q, want \"\"", got)
		}
	})

	t.Run("text content", func(t *testing.T) {
		r := &mcpspec.CallToolResult{
			Content: []mcpspec.Content{
				mcpspec.TextContent{Type: "text", Text: "hello"},
				mcpspec.TextContent{Type: "text", Text: "world"},
			},
		}
		got := renderToolResult(r)
		if !strings.Contains(got, "hello") || !strings.Contains(got, "world") {
			t.Errorf("missing text: %q", got)
		}
		if !strings.Contains(got, "\n\n") {
			t.Errorf("expected blank-line separator between content items, got %q", got)
		}
	})

	t.Run("error flag prefixed", func(t *testing.T) {
		r := &mcpspec.CallToolResult{
			IsError: true,
			Content: []mcpspec.Content{
				mcpspec.TextContent{Type: "text", Text: "boom"},
			},
		}
		got := renderToolResult(r)
		if !strings.HasPrefix(got, "[MCP TOOL ERROR]") {
			t.Errorf("expected error prefix, got %q", got)
		}
	})
}

func TestManagerLoadDisabled(t *testing.T) {
	m := NewManager(Config{Enabled: false})
	report, err := m.Load(t.Context())
	if err != nil {
		t.Fatalf("Load on disabled config: %v", err)
	}
	if len(report.Connected) != 0 || len(report.Errors) != 0 {
		t.Errorf("expected empty report, got %+v", report)
	}
	if got := m.Tools(); len(got) != 0 {
		t.Errorf("expected no tools, got %d", len(got))
	}
}

func TestManagerLoadValidates(t *testing.T) {
	m := NewManager(Config{
		Enabled: true,
		Servers: map[string]ServerConfig{
			"bad": {Transport: "stdio"}, // missing command
		},
	})
	report, err := m.Load(t.Context())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(report.Errors) != 1 {
		t.Fatalf("expected 1 error in report, got %+v", report)
	}
	if !strings.Contains(report.Errors[0].Err.Error(), "requires command") {
		t.Errorf("unexpected error: %v", report.Errors[0])
	}
}

func TestLoadReportString(t *testing.T) {
	r := &LoadReport{
		Connected: []ServerSummary{{Server: "fs", Transport: "stdio", ToolCount: 3}},
	}
	s := r.String()
	if !strings.Contains(s, "fs(stdio,3tools)") {
		t.Errorf("missing connected info: %q", s)
	}

	empty := (&LoadReport{}).String()
	if !strings.Contains(empty, "no servers configured") {
		t.Errorf("empty report message: %q", empty)
	}
}
