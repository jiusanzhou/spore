package swebench

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseTestList(t *testing.T) {
	cases := []struct {
		raw   string
		want  []string
		isErr bool
	}{
		{`["a", "b"]`, []string{"a", "b"}, false},
		{`[]`, nil, false},
		{``, nil, false},
		{`not-json`, nil, true},
	}
	for _, c := range cases {
		got, err := parseTestList(c.raw)
		if (err != nil) != c.isErr {
			t.Errorf("parseTestList(%q) err=%v, wantErr=%v", c.raw, err, c.isErr)
			continue
		}
		if c.isErr {
			continue
		}
		if len(got) != len(c.want) {
			t.Errorf("parseTestList(%q) got %d items, want %d", c.raw, len(got), len(c.want))
			continue
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Errorf("parseTestList(%q)[%d] = %q, want %q", c.raw, i, got[i], c.want[i])
			}
		}
	}
}

func TestLoadInstances(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tiny.jsonl")

	// Two valid instances + a comment line + a blank line.
	content := strings.Join([]string{
		`{"repo":"foo/bar","instance_id":"foo__bar-1","base_commit":"abc","problem_statement":"do x","FAIL_TO_PASS":"[\"t1\"]","PASS_TO_PASS":"[\"t2\",\"t3\"]"}`,
		`# this is a comment, skipped`,
		``,
		`{"repo":"baz/qux","instance_id":"baz__qux-99","base_commit":"def","problem_statement":"do y","FAIL_TO_PASS":"[]","PASS_TO_PASS":"[]"}`,
	}, "\n")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write tmp: %v", err)
	}

	got, err := LoadInstances(path)
	if err != nil {
		t.Fatalf("LoadInstances: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d instances, want 2", len(got))
	}
	if got[0].InstanceID != "foo__bar-1" || got[1].InstanceID != "baz__qux-99" {
		t.Errorf("instance_ids = %q,%q", got[0].InstanceID, got[1].InstanceID)
	}

	ftp, err := got[0].FailToPass()
	if err != nil {
		t.Fatalf("FailToPass: %v", err)
	}
	if len(ftp) != 1 || ftp[0] != "t1" {
		t.Errorf("FailToPass = %v, want [t1]", ftp)
	}

	ptp, err := got[0].PassToPass()
	if err != nil {
		t.Fatalf("PassToPass: %v", err)
	}
	if len(ptp) != 2 || ptp[0] != "t2" || ptp[1] != "t3" {
		t.Errorf("PassToPass = %v, want [t2 t3]", ptp)
	}
}

func TestFilterByIDs(t *testing.T) {
	all := []Instance{
		{InstanceID: "a"},
		{InstanceID: "b"},
		{InstanceID: "c"},
	}
	got := FilterByIDs(all, []string{"a", "c", "missing"})
	if len(got) != 2 {
		t.Fatalf("got %d, want 2", len(got))
	}
	if got[0].InstanceID != "a" || got[1].InstanceID != "c" {
		t.Errorf("got %v", got)
	}

	// empty filter returns input unchanged
	got = FilterByIDs(all, nil)
	if len(got) != 3 {
		t.Errorf("nil filter dropped instances")
	}
}

func TestSplitByMembership(t *testing.T) {
	expected := []string{"t1", "t2", "t3"}
	pass := []string{"t1", "t3", "extra"}
	fail := []string{"t2"}
	sum := splitByMembership(expected, pass, fail)
	if len(sum.Pass) != 2 || sum.Pass[0] != "t1" || sum.Pass[1] != "t3" {
		t.Errorf("Pass = %v", sum.Pass)
	}
	if len(sum.Fail) != 1 || sum.Fail[0] != "t2" {
		t.Errorf("Fail = %v", sum.Fail)
	}

	// Test that didn't appear in either bucket grades as fail.
	expected = []string{"missing"}
	sum = splitByMembership(expected, []string{"other"}, nil)
	if len(sum.Pass) != 0 || len(sum.Fail) != 1 {
		t.Errorf("uncollected test should grade as fail, got %+v", sum)
	}
}
