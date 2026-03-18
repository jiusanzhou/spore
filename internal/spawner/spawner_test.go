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

package spawner

import (
	"os"
	"path/filepath"
	"testing"

	"go.zoe.im/spore/internal/agent"
)

func TestSpawner_Clone(t *testing.T) {
	dir := t.TempDir()
	s := New(dir, 5)

	parent := agent.DefaultConfig("parent", "gpt-4o")
	parent.Agent.Role = "coordinator"

	childCfg, childID, err := s.Spawn(parent, &Request{
		ParentName: "parent",
		ChildName:  "child-1",
		Mode:       ModeClone,
	})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}

	if childCfg.Agent.Name != "child-1" {
		t.Errorf("expected child name 'child-1', got %q", childCfg.Agent.Name)
	}
	if childCfg.Agent.Role != "coordinator" {
		t.Errorf("clone should inherit role, got %q", childCfg.Agent.Role)
	}
	if childCfg.LLM.Model != "gpt-4o" {
		t.Errorf("clone should inherit model, got %q", childCfg.LLM.Model)
	}
	if childID == nil {
		t.Fatal("expected non-nil child identity")
	}

	// Verify files were saved
	if _, err := os.Stat(filepath.Join(dir, "child-1", "spore.toml")); err != nil {
		t.Errorf("expected config file: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "child-1", "identity.key")); err != nil {
		t.Errorf("expected identity file: %v", err)
	}
}

func TestSpawner_Fork(t *testing.T) {
	dir := t.TempDir()
	s := New(dir, 5)

	parent := agent.DefaultConfig("parent", "gpt-4o")
	parent.Spawner.MaxChildren = 10

	childCfg, _, err := s.Spawn(parent, &Request{
		ParentName: "parent",
		ChildName:  "fork-1",
		Mode:       ModeFork,
		Role:       "specialist",
		Model:      "gpt-3.5-turbo",
	})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}

	if childCfg.Agent.Role != "specialist" {
		t.Errorf("fork should use role override, got %q", childCfg.Agent.Role)
	}
	if childCfg.LLM.Model != "gpt-3.5-turbo" {
		t.Errorf("fork should use model override, got %q", childCfg.LLM.Model)
	}
	// Fork halves max children
	if childCfg.Spawner.MaxChildren != 5 {
		t.Errorf("fork should halve max children (10/2=5), got %d", childCfg.Spawner.MaxChildren)
	}
}

func TestSpawner_MaxChildren(t *testing.T) {
	dir := t.TempDir()
	s := New(dir, 2)

	parent := agent.DefaultConfig("parent", "gpt-4o")

	// First two should succeed
	_, _, err := s.Spawn(parent, &Request{ChildName: "c1", Mode: ModeClone})
	if err != nil {
		t.Fatalf("first spawn: %v", err)
	}
	_, _, err = s.Spawn(parent, &Request{ChildName: "c2", Mode: ModeClone})
	if err != nil {
		t.Fatalf("second spawn: %v", err)
	}

	// Third should fail
	_, _, err = s.Spawn(parent, &Request{ChildName: "c3", Mode: ModeClone})
	if err == nil {
		t.Fatal("expected error for exceeding max children")
	}
}

func TestSpawner_ChildCount(t *testing.T) {
	dir := t.TempDir()
	s := New(dir, 10)

	if s.ChildCount() != 0 {
		t.Errorf("expected 0 children, got %d", s.ChildCount())
	}

	parent := agent.DefaultConfig("parent", "gpt-4o")
	s.Spawn(parent, &Request{ChildName: "c1", Mode: ModeClone})
	s.Spawn(parent, &Request{ChildName: "c2", Mode: ModeClone})

	if s.ChildCount() != 2 {
		t.Errorf("expected 2 children, got %d", s.ChildCount())
	}
}

func TestSpawner_BalanceTransfer(t *testing.T) {
	dir := t.TempDir()
	s := New(dir, 5)

	parentCfg := agent.DefaultConfig("parent", "gpt-4o")
	parentID, err := agent.NewIdentity("parent")
	if err != nil {
		t.Fatalf("NewIdentity: %v", err)
	}
	parentID.Credit(100.0)

	childCfg, childID, err := s.SpawnWithBalance(parentCfg, parentID, &Request{
		ChildName: "child-funded",
		Mode:      ModeClone,
	}, 25.0)
	if err != nil {
		t.Fatalf("SpawnWithBalance: %v", err)
	}

	// Parent should have been debited
	if parentID.Balance != 75.0 {
		t.Errorf("expected parent balance 75, got %f", parentID.Balance)
	}

	// Child should have received startup balance
	if childID.Balance != 25.0 {
		t.Errorf("expected child balance 25, got %f", childID.Balance)
	}

	if childCfg.Agent.Name != "child-funded" {
		t.Errorf("expected child name 'child-funded', got %q", childCfg.Agent.Name)
	}
}

func TestSpawner_BalanceTransfer_InsufficientFunds(t *testing.T) {
	dir := t.TempDir()
	s := New(dir, 5)

	parentCfg := agent.DefaultConfig("parent", "gpt-4o")
	parentID, _ := agent.NewIdentity("parent")
	parentID.Credit(10.0)

	_, _, err := s.SpawnWithBalance(parentCfg, parentID, &Request{
		ChildName: "child-broke",
		Mode:      ModeClone,
	}, 50.0)
	if err == nil {
		t.Fatal("expected error for insufficient parent balance")
	}

	// Parent balance should be unchanged
	if parentID.Balance != 10.0 {
		t.Errorf("expected parent balance unchanged at 10, got %f", parentID.Balance)
	}
}

func TestSpawner_BalanceTransfer_ZeroBalance(t *testing.T) {
	dir := t.TempDir()
	s := New(dir, 5)

	parentCfg := agent.DefaultConfig("parent", "gpt-4o")

	// Spawn without balance transfer (backward compatible)
	_, childID, err := s.Spawn(parentCfg, &Request{
		ChildName: "child-no-balance",
		Mode:      ModeClone,
	})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}

	if childID.Balance != 0 {
		t.Errorf("expected child balance 0 without transfer, got %f", childID.Balance)
	}
}
