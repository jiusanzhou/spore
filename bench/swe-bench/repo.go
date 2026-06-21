package swebench

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// PrepareRepo clones inst.Repo into <workRoot>/<instance_id>/ and checks
// out inst.BaseCommit. If the clone already exists at the expected
// commit, we reuse it — clones are expensive (sqlfluff alone is ~50MB
// of objects) and SWE-bench runs hit the same repo across many
// instances. Reset --hard before checkout so a previous agent's failed
// attempt doesn't leak in.
//
// Returns the absolute path to the checked-out working tree.
func PrepareRepo(ctx context.Context, workRoot string, inst Instance) (string, error) {
	workDir := filepath.Join(workRoot, inst.InstanceID)

	cloneURL := fmt.Sprintf("https://github.com/%s.git", inst.Repo)

	// Fast path: already cloned. Reset to a clean state at base_commit.
	if exists(filepath.Join(workDir, ".git")) {
		if err := runGit(ctx, workDir, "reset", "--hard", "HEAD"); err != nil {
			return "", fmt.Errorf("reset existing clone: %w", err)
		}
		if err := runGit(ctx, workDir, "clean", "-fdx"); err != nil {
			return "", fmt.Errorf("clean existing clone: %w", err)
		}
		// Try to checkout the requested base_commit. If it's missing
		// (shallow clone, or we updated to a newer base), fetch first.
		if err := runGit(ctx, workDir, "checkout", inst.BaseCommit); err != nil {
			if fetchErr := runGit(ctx, workDir, "fetch", "--all", "--tags"); fetchErr != nil {
				return "", fmt.Errorf("fetch missing base_commit: %w", fetchErr)
			}
			if err := runGit(ctx, workDir, "checkout", inst.BaseCommit); err != nil {
				return "", fmt.Errorf("checkout base_commit after fetch: %w", err)
			}
		}
		return workDir, nil
	}

	// Cold clone. Use --no-tags / --filter=blob:none for speed where
	// the host's git supports it; fall back to plain clone otherwise.
	if err := os.MkdirAll(filepath.Dir(workDir), 0o755); err != nil {
		return "", fmt.Errorf("mkdir parent: %w", err)
	}
	cloneArgs := []string{"clone", "--filter=blob:none", "--no-tags", cloneURL, workDir}
	if err := runGit(ctx, "", cloneArgs...); err != nil {
		// Retry without filters — older git or proxies that don't speak partial clone.
		if rmErr := os.RemoveAll(workDir); rmErr != nil {
			return "", fmt.Errorf("clone failed (%v) + cleanup failed: %w", err, rmErr)
		}
		if err := runGit(ctx, "", "clone", cloneURL, workDir); err != nil {
			return "", fmt.Errorf("clone: %w", err)
		}
	}
	if err := runGit(ctx, workDir, "checkout", inst.BaseCommit); err != nil {
		return "", fmt.Errorf("checkout base_commit: %w", err)
	}
	return workDir, nil
}

// runGit runs `git <args...>` in cwd (or the process cwd if empty),
// capturing stderr so the caller's error message is actionable rather
// than the bare "exit status 1" git defaults to.
func runGit(ctx context.Context, cwd string, args ...string) error {
	// Hard ceiling on git operations so a stalled clone doesn't pin
	// the harness forever. Most clones finish in <30s; we give 5m for
	// large repos on slow networks.
	subCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(subCtx, "git", args...)
	if cwd != "" {
		cmd.Dir = cwd
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git %s: %w\n%s", strings.Join(args, " "), err, string(out))
	}
	return nil
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
