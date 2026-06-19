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
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/mark3labs/mcp-go/client"
	mcpspec "github.com/mark3labs/mcp-go/mcp"
)

// tool wraps one MCP-server tool as an engine.Tool. The struct is unexported;
// callers receive it via the EngineTool interface from Manager.Tools().
type tool struct {
	engineName  string        // user-visible name like "mcp:fs:read_file"
	serverName  string        // logical server key from config
	remoteName  string        // tool name as the MCP server reports it
	description string        // assembled help text shown to the LLM
	inputSchema mcpspec.ToolInputSchema
	client      *client.Client
	callTimeout time.Duration
}

// Name implements engine.Tool.
func (t *tool) Name() string { return t.engineName }

// Description implements engine.Tool.
func (t *tool) Description() string { return t.description }

// Execute implements engine.Tool. The engine passes a raw string; we accept
// either a JSON object (the MCP-native form) or, when the schema has a single
// required string property, a bare string that we wrap into {field: value}.
//
// This keeps the engine prompt simple ("ACTION: mcp:fs:read_file /etc/hosts")
// while still supporting full structured input when the LLM produces JSON.
func (t *tool) Execute(ctx context.Context, input string) (string, error) {
	if t.client == nil {
		return "", fmt.Errorf("mcp tool %s: client not initialized", t.engineName)
	}

	args, err := t.parseArgs(input)
	if err != nil {
		return "", fmt.Errorf("mcp tool %s: %w", t.engineName, err)
	}

	callCtx := ctx
	if t.callTimeout > 0 {
		var cancel context.CancelFunc
		callCtx, cancel = context.WithTimeout(ctx, t.callTimeout)
		defer cancel()
	}

	req := mcpspec.CallToolRequest{}
	req.Params.Name = t.remoteName
	req.Params.Arguments = args

	resp, err := t.client.CallTool(callCtx, req)
	if err != nil {
		return "", fmt.Errorf("mcp tool %s: call failed: %w", t.engineName, err)
	}

	return renderToolResult(resp), nil
}

// parseArgs converts the engine's free-form string input into a map[string]any
// suitable for MCP CallTool. It handles three cases:
//
//  1. input is empty           → {}
//  2. input parses as JSON obj → use as-is
//  3. input is a bare string AND the schema has exactly one required string
//     property → {prop: input}
//  4. fallback                 → {"input": input} (best-effort; many simple
//     servers accept this)
func (t *tool) parseArgs(input string) (map[string]any, error) {
	input = strings.TrimSpace(input)
	if input == "" {
		return map[string]any{}, nil
	}

	// Case 2: structured JSON
	if strings.HasPrefix(input, "{") {
		var m map[string]any
		if err := json.Unmarshal([]byte(input), &m); err == nil {
			return m, nil
		}
		// fall through if invalid JSON — maybe the LLM passed raw text
	}

	// Case 3: try to coerce a bare string into the single required field
	if field := singleRequiredStringField(t.inputSchema); field != "" {
		return map[string]any{field: input}, nil
	}

	// Case 4: dump under "input" — better than failing
	return map[string]any{"input": input}, nil
}

// singleRequiredStringField returns the name of the only required string-typed
// property in the schema, or "" if the schema has 0 or >1 such properties.
func singleRequiredStringField(s mcpspec.ToolInputSchema) string {
	if len(s.Required) != 1 {
		return ""
	}
	field := s.Required[0]
	prop, ok := s.Properties[field]
	if !ok {
		return field // schema is loose; trust required
	}
	// prop is interface{}; expect a JSON-schema fragment with "type":"string"
	if pm, ok := prop.(map[string]any); ok {
		if t, ok := pm["type"].(string); ok && t == "string" {
			return field
		}
	}
	return ""
}

// renderToolResult flattens a CallToolResult's content array into a single
// string for the engine. MCP results can be text, image, or resource;
// we render text directly and stub the rest with a marker.
func renderToolResult(r *mcpspec.CallToolResult) string {
	if r == nil {
		return ""
	}
	var sb strings.Builder
	if r.IsError {
		sb.WriteString("[MCP TOOL ERROR] ")
	}
	for i, c := range r.Content {
		if i > 0 {
			sb.WriteString("\n\n")
		}
		switch v := c.(type) {
		case mcpspec.TextContent:
			sb.WriteString(v.Text)
		case *mcpspec.TextContent:
			sb.WriteString(v.Text)
		case mcpspec.ImageContent:
			sb.WriteString(fmt.Sprintf("[image %s, %d bytes base64]", v.MIMEType, len(v.Data)))
		case mcpspec.AudioContent:
			sb.WriteString(fmt.Sprintf("[audio %s, %d bytes base64]", v.MIMEType, len(v.Data)))
		default:
			// Fallback: marshal to JSON so the LLM at least sees the shape.
			b, err := json.Marshal(c)
			if err != nil {
				sb.WriteString(fmt.Sprintf("[unrenderable content: %T]", c))
			} else {
				sb.WriteString(string(b))
			}
		}
	}
	return sb.String()
}
