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

package agent

import (
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
)

func TestIdentity_Balance_CreditDebit(t *testing.T) {
	id, err := NewIdentity("test-agent")
	if err != nil {
		t.Fatalf("NewIdentity: %v", err)
	}

	if id.Balance != 0 {
		t.Errorf("expected initial balance 0, got %f", id.Balance)
	}

	id.Credit(100.0)
	if id.Balance != 100.0 {
		t.Errorf("expected balance 100, got %f", id.Balance)
	}
	if id.TotalEarned != 100.0 {
		t.Errorf("expected total earned 100, got %f", id.TotalEarned)
	}

	err = id.Debit(30.0)
	if err != nil {
		t.Fatalf("Debit: %v", err)
	}
	if id.Balance != 70.0 {
		t.Errorf("expected balance 70, got %f", id.Balance)
	}
	if id.TotalSpent != 30.0 {
		t.Errorf("expected total spent 30, got %f", id.TotalSpent)
	}
}

func TestIdentity_Debit_InsufficientBalance(t *testing.T) {
	id, _ := NewIdentity("test-agent")
	id.Credit(10.0)

	err := id.Debit(20.0)
	if err == nil {
		t.Fatal("expected error for insufficient balance")
	}
	// Balance should be unchanged
	if id.Balance != 10.0 {
		t.Errorf("expected balance unchanged at 10, got %f", id.Balance)
	}
}

func TestIdentity_CanAfford(t *testing.T) {
	id, _ := NewIdentity("test-agent")
	id.Credit(50.0)

	if !id.CanAfford(50.0) {
		t.Error("should afford exactly 50")
	}
	if !id.CanAfford(25.0) {
		t.Error("should afford 25")
	}
	if id.CanAfford(100.0) {
		t.Error("should not afford 100")
	}
}

func TestIdentity_SaveLoadWithBalance(t *testing.T) {
	dir := t.TempDir()
	keyPath := filepath.Join(dir, "identity.key")

	// Create and save identity with balance.
	id, err := NewIdentity("test-agent")
	if err != nil {
		t.Fatalf("NewIdentity: %v", err)
	}
	id.Credit(42.5)
	id.Debit(10.0)

	if err := id.Save(keyPath); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// Verify JSON sidecar exists.
	jsonPath := filepath.Join(dir, "identity.json")
	if _, err := os.Stat(jsonPath); err != nil {
		t.Fatalf("expected identity.json sidecar: %v", err)
	}

	// Load identity back.
	loaded, err := LoadIdentity(keyPath)
	if err != nil {
		t.Fatalf("LoadIdentity: %v", err)
	}

	if loaded.Balance != 32.5 {
		t.Errorf("expected loaded balance 32.5, got %f", loaded.Balance)
	}
	if loaded.TotalEarned != 42.5 {
		t.Errorf("expected loaded total earned 42.5, got %f", loaded.TotalEarned)
	}
	if loaded.TotalSpent != 10.0 {
		t.Errorf("expected loaded total spent 10, got %f", loaded.TotalSpent)
	}
	if loaded.Name != "test-agent" {
		t.Errorf("expected loaded name 'test-agent', got %q", loaded.Name)
	}

	// Verify crypto keys match.
	if loaded.PublicKeyHex() != id.PublicKeyHex() {
		t.Error("public keys don't match after load")
	}
}

func TestIdentity_LoadWithoutSidecar(t *testing.T) {
	dir := t.TempDir()
	keyPath := filepath.Join(dir, "identity.key")

	// Create and save just the key (old format).
	id, _ := NewIdentity("test-agent")
	seed := id.PrivateKey.Seed()
	os.WriteFile(keyPath, []byte(hex.EncodeToString(seed)), 0600)

	// Load should work, just with zero balance.
	loaded, err := LoadIdentity(keyPath)
	if err != nil {
		t.Fatalf("LoadIdentity: %v", err)
	}
	if loaded.Balance != 0 {
		t.Errorf("expected zero balance without sidecar, got %f", loaded.Balance)
	}
}
