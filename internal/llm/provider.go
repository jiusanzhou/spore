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

package llm

import (
	"context"
	"fmt"
)

// Message represents a chat message.
type Message struct {
	Role    string `json:"role"`    // system, user, assistant
	Content string `json:"content"`
}

// Response represents a completion response.
type Response struct {
	Content          string
	PromptTokens     int
	CompletionTokens int
}

// ProviderConfig holds common config for all providers.
type ProviderConfig struct {
	BaseURL string
	APIKey  string
	Model   string
}

// Provider is the interface all LLM backends implement.
type Provider interface {
	// Chat sends messages and returns a completion.
	Chat(ctx context.Context, messages []Message) (*Response, error)

	// Model returns the current model name.
	Model() string
}

// NewProvider creates a provider by name.
func NewProvider(name string, cfg ProviderConfig) (Provider, error) {
	switch name {
	case "openai", "":
		return NewOpenAIProvider(cfg), nil
	default:
		return nil, fmt.Errorf("unknown LLM provider: %s", name)
	}
}
