package swebench

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestApplyReport_Resolved(t *testing.T) {
	res := &EvalResult{InstanceID: "x", Reason: "patch-only mode"}
	rep := HarnessReport{
		PatchExists:              true,
		PatchSuccessfullyApplied: true,
		Resolved:                 true,
		TestsStatus: TestsStatus{
			FailToPass: TestBucket{Success: []string{"t1", "t2"}},
			PassToPass: TestBucket{Success: []string{"t3"}},
		},
	}
	applyReport(res, rep)
	if !res.Resolved {
		t.Errorf("Resolved should be true")
	}
	if res.Reason != "" {
		t.Errorf("Reason should clear on resolved, got %q", res.Reason)
	}
	if len(res.FailToPass.Pass) != 2 || len(res.PassToPass.Pass) != 1 {
		t.Errorf("test split wrong: %+v / %+v", res.FailToPass, res.PassToPass)
	}
}

func TestApplyReport_FailToPassMissing(t *testing.T) {
	res := &EvalResult{}
	rep := HarnessReport{
		PatchSuccessfullyApplied: true,
		TestsStatus: TestsStatus{
			FailToPass: TestBucket{
				Success: []string{"t1"},
				Failure: []string{"t2"},
			},
		},
	}
	applyReport(res, rep)
	if res.Resolved {
		t.Errorf("Resolved should be false")
	}
	if res.Reason == "" {
		t.Errorf("Reason should be populated when not resolved")
	}
}

func TestApplyReport_PatchEmpty(t *testing.T) {
	res := &EvalResult{}
	rep := HarnessReport{PatchIsNone: true}
	applyReport(res, rep)
	if res.Resolved {
		t.Errorf("Resolved should be false")
	}
	if res.Reason != "agent produced empty patch (per harness)" {
		t.Errorf("unexpected reason %q", res.Reason)
	}
}

func TestApplyReport_PatchApplyFailed(t *testing.T) {
	res := &EvalResult{}
	rep := HarnessReport{
		PatchExists:              true,
		PatchSuccessfullyApplied: false,
	}
	applyReport(res, rep)
	if res.Reason != "patch did not apply cleanly" {
		t.Errorf("unexpected reason %q", res.Reason)
	}
}

func TestLoadReport(t *testing.T) {
	dir := t.TempDir()
	runID := "test-run"
	model := "spore"
	instanceID := "foo__bar-1"

	// Mirror the harness's on-disk layout:
	//   <dir>/logs/run_evaluation/<run_id>/<model>/<instance_id>/report.json
	reportPath := filepath.Join(
		dir, "logs", "run_evaluation", runID, model, instanceID, "report.json",
	)
	if err := os.MkdirAll(filepath.Dir(reportPath), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	// Real harness output is `{instance_id: {report fields...}}`.
	body := map[string]any{
		instanceID: map[string]any{
			"patch_is_None":              false,
			"patch_exists":               true,
			"patch_successfully_applied": true,
			"resolved":                   true,
			"tests_status": map[string]any{
				"FAIL_TO_PASS": map[string][]string{
					"success": {"t1"},
					"failure": {},
				},
				"PASS_TO_PASS": map[string][]string{
					"success": {"t2", "t3"},
					"failure": {},
				},
			},
		},
	}
	raw, _ := json.Marshal(body)
	if err := os.WriteFile(reportPath, raw, 0o644); err != nil {
		t.Fatalf("write report: %v", err)
	}

	rep, ok := loadReport(dir, runID, model, instanceID)
	if !ok {
		t.Fatalf("loadReport should have found %s", reportPath)
	}
	if !rep.Resolved {
		t.Errorf("Resolved should be true")
	}
	if len(rep.TestsStatus.FailToPass.Success) != 1 ||
		rep.TestsStatus.FailToPass.Success[0] != "t1" {
		t.Errorf("FailToPass.Success = %v", rep.TestsStatus.FailToPass.Success)
	}

	// Missing report should return (zero, false).
	_, ok = loadReport(dir, runID, model, "nonexistent-instance")
	if ok {
		t.Errorf("loadReport should return false for missing report")
	}
}

func TestWritePredictions(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sub", "predictions.json")
	results := []*EvalResult{
		{InstanceID: "a", Patch: "diff a"},
		{InstanceID: "b", Patch: ""},
	}
	if err := WritePredictions(path, "spore-test", results); err != nil {
		t.Fatalf("WritePredictions: %v", err)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var preds []Prediction
	if err := json.Unmarshal(raw, &preds); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(preds) != 2 {
		t.Fatalf("got %d preds, want 2", len(preds))
	}
	if preds[0].InstanceID != "a" || preds[0].Model != "spore-test" || preds[0].Patch != "diff a" {
		t.Errorf("pred[0] = %+v", preds[0])
	}
	// Empty patches are kept so the harness sees them and emits a
	// "patch_is_None" report.
	if preds[1].InstanceID != "b" || preds[1].Patch != "" {
		t.Errorf("pred[1] = %+v", preds[1])
	}
}
