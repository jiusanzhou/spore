package engine

import (
	"context"
	"testing"
)

func TestWebSearchTool_Live(t *testing.T) {
	tool := &WebSearchTool{}
	result, err := tool.Execute(context.Background(), "Go programming language concurrency")
	if err != nil {
		t.Fatalf("search failed: %v", err)
	}
	if len(result) < 50 {
		t.Fatalf("search result too short: %s", result)
	}
	t.Logf("Search result (%d chars):\n%s", len(result), result[:min(len(result), 600)])
}

func TestWebFetchTool_Live(t *testing.T) {
	tool := &WebFetchTool{}
	result, err := tool.Execute(context.Background(), "https://go.dev")
	if err != nil {
		t.Fatalf("fetch failed: %v", err)
	}
	if len(result) < 100 {
		t.Fatalf("fetch result too short: %s", result)
	}
	t.Logf("Fetch result (%d chars):\n%s", len(result), result[:min(len(result), 600)])
}
