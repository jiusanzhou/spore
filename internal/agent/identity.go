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
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// Identity represents an agent's cryptographic identity.
type Identity struct {
	Name       string
	PublicKey  ed25519.PublicKey
	PrivateKey ed25519.PrivateKey

	mu         sync.Mutex
	Balance    float64 `json:"balance"`
	TotalEarned float64 `json:"total_earned"`
	TotalSpent  float64 `json:"total_spent"`
}

// identityJSON is the JSON sidecar format for persisting identity state.
type identityJSON struct {
	Name        string  `json:"name"`
	PublicKey   string  `json:"public_key"`
	Balance     float64 `json:"balance"`
	TotalEarned float64 `json:"total_earned"`
	TotalSpent  float64 `json:"total_spent"`
}

// NewIdentity generates a new Ed25519 identity for the agent.
func NewIdentity(name string) (*Identity, error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("generating ed25519 key: %w", err)
	}
	return &Identity{
		Name:       name,
		PublicKey:  pub,
		PrivateKey: priv,
	}, nil
}

// LoadIdentity reads a saved identity from disk.
func LoadIdentity(path string) (*Identity, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading identity file: %w", err)
	}
	seed, err := hex.DecodeString(string(data))
	if err != nil {
		return nil, fmt.Errorf("decoding identity: %w", err)
	}
	priv := ed25519.NewKeyFromSeed(seed)
	pub := priv.Public().(ed25519.PublicKey)

	id := &Identity{
		PublicKey:  pub,
		PrivateKey: priv,
	}

	// Try to load JSON sidecar for balance state.
	jsonPath := sidecarPath(path)
	if jdata, err := os.ReadFile(jsonPath); err == nil {
		var ij identityJSON
		if err := json.Unmarshal(jdata, &ij); err == nil {
			id.Name = ij.Name
			id.Balance = ij.Balance
			id.TotalEarned = ij.TotalEarned
			id.TotalSpent = ij.TotalSpent
		}
	}

	return id, nil
}

// Save writes the identity's private key seed to disk.
func (id *Identity) Save(path string) error {
	seed := id.PrivateKey.Seed()
	if err := os.WriteFile(path, []byte(hex.EncodeToString(seed)), 0600); err != nil {
		return err
	}
	return id.SaveState(path)
}

// SaveState writes the identity's balance state to the JSON sidecar file.
func (id *Identity) SaveState(keyPath string) error {
	id.mu.Lock()
	defer id.mu.Unlock()

	ij := identityJSON{
		Name:        id.Name,
		PublicKey:   hex.EncodeToString(id.PublicKey),
		Balance:     id.Balance,
		TotalEarned: id.TotalEarned,
		TotalSpent:  id.TotalSpent,
	}
	data, err := json.MarshalIndent(ij, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling identity state: %w", err)
	}
	return os.WriteFile(sidecarPath(keyPath), data, 0600)
}

// sidecarPath returns the JSON sidecar path for an identity key file.
func sidecarPath(keyPath string) string {
	dir := filepath.Dir(keyPath)
	base := strings.TrimSuffix(filepath.Base(keyPath), filepath.Ext(keyPath))
	return filepath.Join(dir, base+".json")
}

// Credit adds tokens to the identity's balance.
func (id *Identity) Credit(amount float64) {
	id.mu.Lock()
	defer id.mu.Unlock()
	id.Balance += amount
	id.TotalEarned += amount
}

// Debit subtracts tokens from the identity's balance.
// Returns an error if the balance is insufficient.
func (id *Identity) Debit(amount float64) error {
	id.mu.Lock()
	defer id.mu.Unlock()
	if id.Balance < amount {
		return fmt.Errorf("insufficient balance: have %.4f, need %.4f", id.Balance, amount)
	}
	id.Balance -= amount
	id.TotalSpent += amount
	return nil
}

// CanAfford returns true if the identity has enough balance.
func (id *Identity) CanAfford(amount float64) bool {
	id.mu.Lock()
	defer id.mu.Unlock()
	return id.Balance >= amount
}

// PublicKeyHex returns the hex-encoded public key.
func (id *Identity) PublicKeyHex() string {
	return hex.EncodeToString(id.PublicKey)
}

// Sign signs a message with the agent's private key.
func (id *Identity) Sign(message []byte) []byte {
	return ed25519.Sign(id.PrivateKey, message)
}

// Verify checks a signature against this identity's public key.
func (id *Identity) Verify(message, sig []byte) bool {
	return ed25519.Verify(id.PublicKey, message, sig)
}
