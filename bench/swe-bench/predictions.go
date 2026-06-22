package swebench

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// Prediction is one row of the SWE-bench predictions.json file format.
//
// The schema is fixed by the upstream harness (swebench.harness.constants):
//
//	instance_id          — matches Instance.InstanceID
//	model_name_or_path   — free-form model tag; appears in report paths
//	model_patch          — the candidate diff to apply on top of base_commit
//
// We keep the JSON tags identical so the same struct round-trips through
// the harness without any rename layer.
type Prediction struct {
	InstanceID string `json:"instance_id"`
	Model      string `json:"model_name_or_path"`
	Patch      string `json:"model_patch"`
}

// WritePredictions converts a slice of EvalResult into the official
// predictions.json format expected by `python -m
// swebench.harness.run_evaluation`. The file is written to path and
// MUST be a JSON array (not the "list of dicts" inline form the docs
// occasionally suggest — the harness accepts both, but the array form
// is more portable and the one swebench.harness uses internally).
//
// modelTag is the value placed in model_name_or_path. Pick something
// stable per spore version + runtime so multiple eval runs can be
// distinguished in the report directory tree.
//
// Empty patches are still written: the harness sees them and emits a
// "patch_is_None" report which is exactly the signal we want — agent
// produced no fix => instance unresolved.
func WritePredictions(path, modelTag string, results []*EvalResult) error {
	preds := make([]Prediction, 0, len(results))
	for _, r := range results {
		preds = append(preds, Prediction{
			InstanceID: r.InstanceID,
			Model:      modelTag,
			Patch:      r.Patch,
		})
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("mkdir predictions dir: %w", err)
	}
	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("create predictions: %w", err)
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	if err := enc.Encode(preds); err != nil {
		return fmt.Errorf("encode predictions: %w", err)
	}
	return nil
}
