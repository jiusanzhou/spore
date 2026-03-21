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

package engine

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os/exec"
	"strings"
	"time"
)

// ShellTool executes shell commands. Use with caution.
type ShellTool struct {
	// AllowList restricts which commands can be run (empty = allow all).
	AllowList []string
	WorkDir   string
}

func (t *ShellTool) Name() string        { return "shell" }
func (t *ShellTool) Description() string { return "Execute a shell command and return output" }

func (t *ShellTool) Execute(ctx context.Context, input string) (string, error) {
	input = strings.TrimSpace(input)
	if input == "" {
		return "", fmt.Errorf("empty command")
	}

	if len(t.AllowList) > 0 {
		allowed := false
		for _, prefix := range t.AllowList {
			if strings.HasPrefix(input, prefix) {
				allowed = true
				break
			}
		}
		if !allowed {
			return "", fmt.Errorf("command not in allow list: %s", input)
		}
	}

	cmd := exec.CommandContext(ctx, "sh", "-c", input)
	if t.WorkDir != "" {
		cmd.Dir = t.WorkDir
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("command failed: %w\noutput: %s", err, string(out))
	}
	return string(out), nil
}

// WebSearchTool searches the web via DuckDuckGo HTML (no API key needed).
type WebSearchTool struct{}

func (t *WebSearchTool) Name() string        { return "search" }
func (t *WebSearchTool) Description() string { return "Search the web for information. Input: search query string" }

func (t *WebSearchTool) Execute(ctx context.Context, input string) (string, error) {
	input = strings.TrimSpace(input)
	if input == "" {
		return "", fmt.Errorf("search query cannot be empty")
	}

	// DuckDuckGo HTML-only endpoint (no JS required)
	searchURL := "https://html.duckduckgo.com/html/?q=" + url.QueryEscape(input)

	req, err := http.NewRequestWithContext(ctx, "GET", searchURL, nil)
	if err != nil {
		return "", fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; SporeAgent/0.1)")

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("search request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 64*1024)) // 64KB max
	if err != nil {
		return "", fmt.Errorf("reading response: %w", err)
	}

	// Extract result snippets from DuckDuckGo HTML
	html := string(body)
	results := extractDDGResults(html, 5)
	if len(results) == 0 {
		return "No search results found for: " + input, nil
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Search results for: %s\n\n", input))
	for i, r := range results {
		sb.WriteString(fmt.Sprintf("%d. %s\n   %s\n   %s\n\n", i+1, r.title, r.url, r.snippet))
	}
	return sb.String(), nil
}

type ddgResult struct {
	title   string
	url     string
	snippet string
}

// extractDDGResults parses DuckDuckGo HTML results (class="result__*").
func extractDDGResults(html string, maxResults int) []ddgResult {
	var results []ddgResult

	// DuckDuckGo HTML uses class="result__a" for title links and class="result__snippet" for snippets
	remaining := html
	for len(results) < maxResults {
		// Find result title link
		titleIdx := strings.Index(remaining, `class="result__a"`)
		if titleIdx == -1 {
			break
		}
		remaining = remaining[titleIdx:]

		// Extract href
		hrefStart := strings.Index(remaining, `href="`)
		if hrefStart == -1 {
			break
		}
		hrefStart += 6
		hrefEnd := strings.Index(remaining[hrefStart:], `"`)
		if hrefEnd == -1 {
			break
		}
		rawURL := remaining[hrefStart : hrefStart+hrefEnd]

		// Extract title text (between > and </a>)
		titleTextStart := strings.Index(remaining, ">")
		if titleTextStart == -1 {
			break
		}
		titleTextStart++
		titleTextEnd := strings.Index(remaining[titleTextStart:], "</a>")
		if titleTextEnd == -1 {
			break
		}
		title := stripHTML(remaining[titleTextStart : titleTextStart+titleTextEnd])

		// Extract snippet
		snippet := ""
		snippetIdx := strings.Index(remaining, `class="result__snippet"`)
		if snippetIdx != -1 {
			snippetContent := remaining[snippetIdx:]
			snipStart := strings.Index(snippetContent, ">")
			if snipStart != -1 {
				snipStart++
				snipEnd := strings.Index(snippetContent[snipStart:], "</")
				if snipEnd != -1 {
					snippet = stripHTML(snippetContent[snipStart : snipStart+snipEnd])
				}
			}
		}

		// Clean up DuckDuckGo redirect URL → extract actual URL
		cleanURL := rawURL
		if strings.Contains(rawURL, "uddg=") {
			if u, err := url.Parse(rawURL); err == nil {
				if uddg := u.Query().Get("uddg"); uddg != "" {
					cleanURL = uddg
				}
			}
		}

		if title != "" {
			results = append(results, ddgResult{
				title:   strings.TrimSpace(title),
				url:     cleanURL,
				snippet: strings.TrimSpace(snippet),
			})
		}

		// Move past this result
		remaining = remaining[1:]
	}

	return results
}

// stripHTML removes HTML tags from a string.
func stripHTML(s string) string {
	var sb strings.Builder
	inTag := false
	for _, r := range s {
		if r == '<' {
			inTag = true
			continue
		}
		if r == '>' {
			inTag = false
			continue
		}
		if !inTag {
			sb.WriteRune(r)
		}
	}
	return sb.String()
}

// WebFetchTool fetches a URL and returns readable text content.
type WebFetchTool struct{}

func (t *WebFetchTool) Name() string { return "fetch" }
func (t *WebFetchTool) Description() string {
	return "Fetch a web page and return its text content. Input: URL to fetch"
}

func (t *WebFetchTool) Execute(ctx context.Context, input string) (string, error) {
	input = strings.TrimSpace(input)
	if input == "" {
		return "", fmt.Errorf("URL cannot be empty")
	}
	if !strings.HasPrefix(input, "http://") && !strings.HasPrefix(input, "https://") {
		input = "https://" + input
	}

	req, err := http.NewRequestWithContext(ctx, "GET", input, nil)
	if err != nil {
		return "", fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; SporeAgent/0.1)")
	req.Header.Set("Accept", "text/html,application/xhtml+xml,text/plain")

	client := &http.Client{
		Timeout: 20 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return fmt.Errorf("too many redirects")
			}
			return nil
		},
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("fetch failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return "", fmt.Errorf("HTTP %d: %s", resp.StatusCode, resp.Status)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 128*1024)) // 128KB max
	if err != nil {
		return "", fmt.Errorf("reading body: %w", err)
	}

	content := string(body)

	// If HTML, extract readable text
	ct := resp.Header.Get("Content-Type")
	if strings.Contains(ct, "html") {
		content = htmlToText(content)
	}

	// Truncate to reasonable size for LLM context
	if len(content) > 8000 {
		content = content[:8000] + "\n\n[... truncated at 8000 chars]"
	}

	return fmt.Sprintf("Fetched %s (%d bytes):\n\n%s", input, len(body), content), nil
}

// htmlToText does a rough HTML→text conversion: removes scripts/styles, strips tags, collapses whitespace.
func htmlToText(html string) string {
	// Remove script and style blocks
	for _, tag := range []string{"script", "style", "noscript"} {
		for {
			start := strings.Index(strings.ToLower(html), "<"+tag)
			if start == -1 {
				break
			}
			end := strings.Index(strings.ToLower(html[start:]), "</"+tag+">")
			if end == -1 {
				html = html[:start]
				break
			}
			html = html[:start] + html[start+end+len("</"+tag+">"):]
		}
	}

	// Strip remaining tags
	text := stripHTML(html)

	// Collapse whitespace
	lines := strings.Split(text, "\n")
	var cleaned []string
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line != "" {
			cleaned = append(cleaned, line)
		}
	}
	return strings.Join(cleaned, "\n")
}

// MemoryTool reads/writes agent memory.
type MemoryTool struct {
	Store interface {
		Get(key string) (interface{ }, error)
		Put(entry interface{}) error
	}
}

func (t *MemoryTool) Name() string        { return "memory" }
func (t *MemoryTool) Description() string { return "Read or write to agent memory. Use 'get <key>' or 'set <key> <value>'" }

func (t *MemoryTool) Execute(ctx context.Context, input string) (string, error) {
	// TODO: implement memory read/write
	return fmt.Sprintf("[memory operation: %s] (not yet implemented)", input), nil
}

// DelegateTool sends a task to another agent via the message bus.
type DelegateTool struct {
	SendFunc func(to, taskDesc string) error
}

func (t *DelegateTool) Name() string        { return "delegate" }
func (t *DelegateTool) Description() string { return "Delegate a sub-task to another agent. Usage: delegate <agent_id> <task description>" }

func (t *DelegateTool) Execute(ctx context.Context, input string) (string, error) {
	parts := strings.SplitN(input, " ", 2)
	if len(parts) < 2 {
		return "", fmt.Errorf("usage: delegate <agent_id> <task>")
	}
	if t.SendFunc == nil {
		return "", fmt.Errorf("message bus not configured")
	}
	if err := t.SendFunc(parts[0], parts[1]); err != nil {
		return "", err
	}
	return fmt.Sprintf("Task delegated to %s", parts[0]), nil
}
