/*
 * Copyright (c) 2026 wellwell.work, LLC by Zoe
 *
 * spore-acp-server: expose a spore runtime as an ACP-compliant agent over
 * stdio. RFC-001 Stage 2.
 *
 * Configure your ACP client (Zed, JetBrains, Neovim, etc.) to spawn:
 *
 *     spore-acp-server [--inner <runtime>] [--cwd <dir>]
 *
 * --inner names which spore runtime drives the prompts. Defaults to
 * "claude-code", which routes through ACPRuntime → claude-agent-acp when
 * available, falling back to the abox legacy adapter via aliasRuntime.
 * Use "builtin" to drive spore's own engine (requires LLM provider
 * config; not wired up here yet).
 */

package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"go.zoe.im/spore/internal/runtime"
)

func main() {
	var (
		inner   = flag.String("inner", "claude-code", "inner runtime name (claude-code, codex, openclaw, ...)")
		verbose = flag.Bool("verbose", false, "log protocol-level events to stderr")
	)
	flag.Parse()

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	// Discover available runtimes; pick the requested inner.
	reg := runtime.NewRegistry()
	discovered := reg.AutoDiscover(ctx)
	if *verbose {
		fmt.Fprintf(os.Stderr, "[spore-acp-server] discovered: %v\n", discovered)
	}

	rt, ok := reg.Get(*inner)
	if !ok {
		fmt.Fprintf(os.Stderr,
			"[spore-acp-server] inner runtime %q not available. discovered=%v\n",
			*inner, discovered)
		os.Exit(1)
	}

	if *verbose {
		fmt.Fprintf(os.Stderr,
			"[spore-acp-server] inner=%s (%T) — starting ACP server on stdio\n",
			rt.Info().Name, rt)
	}

	srv := runtime.NewACPServer(rt)
	if *verbose {
		srv.Logger = func(s string) {
			fmt.Fprintf(os.Stderr, "[spore-acp-server] %s\n", s)
		}
	}

	if err := srv.Serve(ctx, os.Stdin, stdoutCloser{}); err != nil {
		fmt.Fprintf(os.Stderr, "[spore-acp-server] error: %v\n", err)
		os.Exit(1)
	}
}

// stdoutCloser wraps os.Stdout as io.WriteCloser. We don't actually want
// to close stdout (that's the parent's resource), but ACPServer demands
// the interface so it can attempt to close on ctx-cancel write-unblock.
type stdoutCloser struct{}

func (stdoutCloser) Write(p []byte) (int, error) { return os.Stdout.Write(p) }
func (stdoutCloser) Close() error                { return nil }

// Compile-time assertion that os.Stdout still satisfies io.Reader for the
// dual-direction wiring above. (The actual server reads from os.Stdin.)
var _ io.Reader = (*os.File)(nil)
