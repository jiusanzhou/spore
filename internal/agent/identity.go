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
	"fmt"
	"os"
)

// Identity represents an agent's cryptographic identity.
type Identity struct {
	Name       string
	PublicKey  ed25519.PublicKey
	PrivateKey ed25519.PrivateKey
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
	return &Identity{
		PublicKey:  pub,
		PrivateKey: priv,
	}, nil
}

// Save writes the identity's private key seed to disk.
func (id *Identity) Save(path string) error {
	seed := id.PrivateKey.Seed()
	return os.WriteFile(path, []byte(hex.EncodeToString(seed)), 0600)
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
