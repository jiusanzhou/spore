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
	"os/exec"
	"strings"
	"testing"
)

// TestAutoDiscover_ACPClaimsClaudeCode verifies the registry's discovery
// order: when claude-agent-acp is in PATH, the "claude-code" name resolves
// to an ACPRuntime, not the legacy abox adapter. This is the wiring contract
// for RFC-001 Stage 1: external callers asking for "claude-code" by name
// transparently get the ACP path.
func TestAutoDiscover_ACPClaimsClaudeCode(t *testing.T) {
	if _, err := exec.LookPath("claude-agent-acp"); err != nil {
		t.Skip("claude-agent-acp not in PATH; skipping (this is the negative path)")
	}

	reg := NewRegistry()
	discovered := reg.AutoDiscover(context.Background())

	rt, ok := reg.Get("claude-code")
	if !ok {
		t.Fatalf("claude-code not registered. discovered=%v", discovered)
	}

	if _, isACP := rt.(*ACPRuntime); !isACP {
		t.Errorf("claude-code resolved to %T, want *ACPRuntime — abox adapter "+
			"shadowed the ACP runtime, ordering in AutoDiscover is wrong", rt)
	}
}

// TestAutoDiscover_LegacyClaudeStillRegistered: even when ACP claims
// "claude-code", the abox-backed "claude" (different name) should still be
// discovered separately if its CLI is available — the two names coexist.
func TestAutoDiscover_LegacyClaudeStillRegistered(t *testing.T) {
	if _, err := exec.LookPath("claude"); err != nil {
		t.Skip("claude (Claude Code CLI) not in PATH")
	}

	reg := NewRegistry()
	reg.AutoDiscover(context.Background())

	if _, ok := reg.Get("claude"); !ok {
		t.Errorf("expected 'claude' (abox) registered alongside 'claude-code' (acp)")
	}
}

// TestAutoDiscover_FallbackWithoutACP: if claude-agent-acp is absent, the
// legacy abox claude-code or whatever else is available should still be
// discovered. We can't easily simulate "absent" without sandboxing PATH;
// instead this asserts the registry never returns empty when at least one
// candidate is available — the fallback chain works.
func TestAutoDiscover_NeverEmpty(t *testing.T) {
	reg := NewRegistry()
	discovered := reg.AutoDiscover(context.Background())
	if len(discovered) == 0 {
		t.Skip("no agent CLIs available in PATH; nothing to assert")
	}
	if len(reg.List()) != len(discovered) {
		t.Errorf("registry size %d != discovered count %d",
			len(reg.List()), len(discovered))
	}
}

// TestAutoDiscover_ACPLabel: the discovery label for ACP runtimes carries
// "(acp)" so the runtimes CLI can show source. This is the contract that
// cmd/runtimes.go's parser depends on.
func TestAutoDiscover_ACPLabel(t *testing.T) {
	if _, err := exec.LookPath("claude-agent-acp"); err != nil {
		t.Skip("claude-agent-acp not in PATH")
	}

	reg := NewRegistry()
	discovered := reg.AutoDiscover(context.Background())

	found := false
	for _, label := range discovered {
		if label == "claude-code (acp)" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("no 'claude-code (acp)' label in discovery output: %v", discovered)
	}
}

// TestAutoDiscover_AliasFallback: when ACP isn't available, the legacy
// abox "claude" adapter should be exposed under the canonical "claude-code"
// name via aliasRuntime. We can't easily make claude-agent-acp un-findable
// from inside the test, so we directly exercise the alias type.
func TestAutoDiscover_AliasRuntime(t *testing.T) {
	// Use the existing builtin runtime as the inner — it's always healthy
	// and doesn't need external deps.
	inner := &Builtin{}
	alias := &aliasRuntime{inner: inner, name: "claude-code"}

	if got := alias.Info().Name; got != "claude-code" {
		t.Errorf("alias.Info().Name = %q, want claude-code", got)
	}
	// Capabilities should pass through.
	if len(alias.Info().Capabilities) != len(inner.Info().Capabilities) {
		t.Errorf("alias capabilities not forwarded from inner")
	}
	// Healthy passes through.
	if err := alias.Healthy(context.Background()); err != nil {
		t.Errorf("alias.Healthy = %v, want nil (builtin always healthy)", err)
	}
}

// TestAutoDiscover_AliasOnlyWhenNoACP: alias only fires when ACP didn't
// claim "claude-code". Driven by inspecting the discovery output: with
// claude-agent-acp installed there should be no "(alias→claude)" tag.
func TestAutoDiscover_NoAliasWhenACPPresent(t *testing.T) {
	if _, err := exec.LookPath("claude-agent-acp"); err != nil {
		t.Skip("claude-agent-acp not in PATH; can't verify alias suppression")
	}

	reg := NewRegistry()
	discovered := reg.AutoDiscover(context.Background())

	for _, label := range discovered {
		if strings.Contains(label, "alias→claude") {
			t.Errorf("alias should not fire when ACP claims claude-code: %v",
				discovered)
		}
	}
}

// TestRoute_PrefersACPForCoding: tag-based routing should land on the
// ACP runtime for coding tasks when it's registered (it claims coding/shell
// tags same as legacy adapters; tie-breaking is "first in map" so we just
// verify *some* coding-capable runtime wins, not specifically ACP).
func TestRoute_CodingTagFindsRuntime(t *testing.T) {
	reg := NewRegistry()
	reg.AutoDiscover(context.Background())

	if len(reg.List()) == 0 {
		t.Skip("no runtimes available")
	}

	rt, err := reg.Route([]string{"coding"})
	if err != nil {
		t.Fatalf("Route(coding) failed: %v", err)
	}

	gotCoding := false
	for _, cap := range rt.Info().Capabilities {
		for _, tag := range cap.Tags {
			if tag == "coding" {
				gotCoding = true
			}
		}
	}
	if !gotCoding {
		t.Errorf("Route(coding) returned %s with no coding tag: %+v",
			rt.Info().Name, rt.Info().Capabilities)
	}
}

// TestRegistry_NameUniqueness: registering the same name twice replaces;
// ACP-then-abox does NOT clobber, abox skips when name exists. Running
// AutoDiscover twice must not grow the registry — the second pass is
// idempotent at the registry level (even if the discovered-this-pass list
// shows the ACP re-registration since that block is unconditional).
func TestRegistry_AutoDiscoverIdempotent(t *testing.T) {
	reg := NewRegistry()
	reg.AutoDiscover(context.Background())
	sizeAfterFirst := len(reg.List())

	reg.AutoDiscover(context.Background())
	sizeAfterSecond := len(reg.List())

	if sizeAfterFirst != sizeAfterSecond {
		t.Errorf("registry grew on re-discovery: first=%d second=%d",
			sizeAfterFirst, sizeAfterSecond)
	}
}
