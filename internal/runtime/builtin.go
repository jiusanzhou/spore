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

package runtime

import (
	"context"

	"go.zoe.im/spore/internal/engine"
	"go.zoe.im/spore/internal/llm"
	"go.zoe.im/spore/internal/memory"
)

// Builtin is the default runtime that uses Spore's own engine
// (Observe → Think → Act → Reflect loop with LLM).
type Builtin struct {
	provider llm.Provider
	store    memory.Store
}

// NewBuiltin creates the built-in LLM-based runtime.
func NewBuiltin(provider llm.Provider, store memory.Store) *Builtin {
	return &Builtin{
		provider: provider,
		store:    store,
	}
}

func (b *Builtin) Info() Info {
	return Info{
		Name:    "builtin",
		Version: "0.1.0",
		Capabilities: []Capability{
			{Name: "general", Description: "LLM-powered task execution with tool use", Tags: []string{"general", "shell"}},
		},
		MaxConcurrent: 5,
	}
}

func (b *Builtin) Execute(ctx context.Context, task TaskInput) (*TaskOutput, error) {
	eng := engine.New(b.provider, b.store)
	eng.RegisterTool(&engine.ShellTool{WorkDir: task.WorkDir})
	eng.RegisterTool(&engine.WebSearchTool{})

	t := &engine.Task{
		ID:          task.ID,
		Description: task.Description,
		State:       engine.TaskPending,
	}

	err := eng.Run(ctx, t)
	output := &TaskOutput{
		Success: err == nil,
		Result:  t.Result,
	}
	if err != nil {
		output.Error = err.Error()
	}
	return output, nil
}

func (b *Builtin) Healthy(ctx context.Context) error {
	return nil // always healthy
}

func (b *Builtin) Close() error { return nil }
