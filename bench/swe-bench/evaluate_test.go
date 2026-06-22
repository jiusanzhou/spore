package swebench

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReadResults_Roundtrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "results.json")

	original := []*EvalResult{
		{
			InstanceID: "foo__bar-1",
			Resolved:   true,
			Patch:      "diff --git a/x b/x\n@@ ...",
		},
		{
			InstanceID: "foo__bar-2",
			Resolved:   false,
			Reason:     "no F2P pass",
			Patch:      "diff --git a/y b/y\n@@ ...",
		},
	}
	if err := WriteResults(path, original); err != nil {
		t.Fatalf("WriteResults: %v", err)
	}

	loaded, err := ReadResults(path)
	if err != nil {
		t.Fatalf("ReadResults: %v", err)
	}
	if len(loaded) != len(original) {
		t.Fatalf("len mismatch: got %d, want %d", len(loaded), len(original))
	}
	for i, r := range loaded {
		if r.InstanceID != original[i].InstanceID {
			t.Errorf("[%d] InstanceID: got %q, want %q", i, r.InstanceID, original[i].InstanceID)
		}
		if r.Resolved != original[i].Resolved {
			t.Errorf("[%d] Resolved: got %v, want %v", i, r.Resolved, original[i].Resolved)
		}
		if r.Patch != original[i].Patch {
			t.Errorf("[%d] Patch mismatch", i)
		}
		if r.Reason != original[i].Reason {
			t.Errorf("[%d] Reason: got %q, want %q", i, r.Reason, original[i].Reason)
		}
	}
}

func TestReadResults_FileMissing(t *testing.T) {
	if _, err := ReadResults("/nonexistent/results.json"); err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestReadResults_Malformed(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.json")
	if err := os.WriteFile(path, []byte("not json"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	if _, err := ReadResults(path); err == nil {
		t.Fatal("expected error for malformed JSON")
	}
}
