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

package memory

import "fmt"

// Entry represents a single memory entry.
type Entry struct {
	ID        string
	AgentID   string
	Key       string
	Value     string
	Metadata  map[string]string
	CreatedAt int64
	UpdatedAt int64
	AccessCnt int
	CID       string // IPFS content identifier
	Shared    bool   // whether shared to IPFS
}

// Store is the interface for memory backends.
type Store interface {
	// Put stores a memory entry.
	Put(entry *Entry) error

	// Get retrieves a memory entry by key.
	Get(key string) (*Entry, error)

	// Search finds entries matching a query.
	Search(query string, limit int) ([]*Entry, error)

	// Delete removes an entry.
	Delete(key string) error

	// Close releases resources.
	Close() error
}

// NewStore creates a memory store by backend name.
func NewStore(backend, path string, opts ...StoreOption) (Store, error) {
	o := &storeOptions{}
	for _, opt := range opts {
		opt(o)
	}
	switch backend {
	case "sqlite", "":
		return NewSQLiteStore(path)
	case "ipfs":
		endpoint := o.ipfsEndpoint
		if endpoint == "" {
			endpoint = "localhost:5001"
		}
		return NewIPFSStore(path, IPFSConfig{APIEndpoint: endpoint})
	default:
		return nil, fmt.Errorf("unknown memory backend: %s", backend)
	}
}

type storeOptions struct {
	ipfsEndpoint string
}

// StoreOption configures store creation.
type StoreOption func(*storeOptions)

// WithIPFSEndpoint sets the IPFS API endpoint.
func WithIPFSEndpoint(endpoint string) StoreOption {
	return func(o *storeOptions) {
		o.ipfsEndpoint = endpoint
	}
}
