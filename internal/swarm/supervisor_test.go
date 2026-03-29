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

package swarm

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSupervisor_PidFile(t *testing.T) {
	dir := t.TempDir()
	pidFile := filepath.Join(dir, "test.pid")

	sw := New(dir, 3)
	cfg := SupervisorConfig{
		PidFile:     pidFile,
		MaxRestarts: 3,
	}
	sv := NewSupervisor(sw, cfg)

	// Write PID file
	if err := sv.writePidFile(); err != nil {
		t.Fatalf("writePidFile: %v", err)
	}

	// Check it exists
	pid := ReadPidFile(pidFile)
	if pid != os.Getpid() {
		t.Errorf("expected pid %d, got %d", os.Getpid(), pid)
	}

	// Check IsRunning (our own PID should be running)
	if !IsRunning(pid) {
		t.Error("expected own process to be running")
	}

	// Remove PID file
	sv.removePidFile()
	if _, err := os.Stat(pidFile); !os.IsNotExist(err) {
		t.Error("PID file should be removed")
	}
}

func TestSupervisor_ReadPidFile_Missing(t *testing.T) {
	pid := ReadPidFile("/nonexistent/path")
	if pid != 0 {
		t.Errorf("expected 0 for missing file, got %d", pid)
	}
}

func TestSupervisor_IsRunning_Invalid(t *testing.T) {
	if IsRunning(0) {
		t.Error("pid 0 should not be running")
	}
	if IsRunning(-1) {
		t.Error("pid -1 should not be running")
	}
}

func TestSupervisor_ChangelogFeedback(t *testing.T) {
	dir := t.TempDir()
	sw := New(dir, 3)
	cfg := DefaultSupervisorConfig(dir)
	sv := NewSupervisor(sw, cfg)

	if sv.Changelog() == nil {
		t.Error("changelog should not be nil")
	}
	if sv.Feedback() == nil {
		t.Error("feedback should not be nil")
	}

	// Submit feedback through supervisor
	sv.Feedback().SubmitFeedback(FeedbackEntry{
		Type:    FeedbackUpvote,
		Author:  "zoe",
		Target:  "forge",
		Message: "good job",
	})

	// Should appear in changelog
	if sv.Changelog().Count() != 1 {
		t.Errorf("expected 1 changelog entry, got %d", sv.Changelog().Count())
	}
}
