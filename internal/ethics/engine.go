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

// Package ethics implements a two-layer ethics engine for agent action validation.
//
// L0: Hard constraints — always enforced, cannot be overridden.
//   - No destructive commands (rm -rf /, mkfs, etc.)
//   - No network exfiltration of private data
//   - Budget limits per task
//
// L1: Soft constraints — configurable rules that can be adjusted per agent.
//   - Allowed/denied command prefixes
//   - Network access restrictions
//   - Custom rules
//
// Every decision (allow/deny) is logged to an audit trail in SQLite.
package ethics

import (
	"database/sql"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

// Decision represents the outcome of an ethics check.
type Decision string

const (
	Allow Decision = "allow"
	Deny  Decision = "deny"
)

// Level indicates which constraint layer triggered the decision.
type Level string

const (
	LevelL0 Level = "L0" // hard constraint
	LevelL1 Level = "L1" // soft constraint
)

// AuditEntry records a single ethics decision.
type AuditEntry struct {
	ID        int64
	Timestamp int64
	AgentID   string
	TaskID    string
	Action    string
	Decision  Decision
	Level     Level
	Reason    string
}

// Rule is a configurable L1 soft constraint.
type Rule struct {
	Name        string
	Description string
	Check       func(action string) (Decision, string) // returns decision + reason
}

// Config holds ethics engine configuration.
type Config struct {
	MaxBudgetPerTask float64  // max $ per task
	AllowedCommands  []string // if non-empty, only these prefixes allowed
	DeniedCommands   []string // additional denied command prefixes
	AllowNetwork     bool     // whether network access is allowed
	CustomRules      []Rule   // additional L1 rules
}

// DefaultConfig returns sensible defaults.
func DefaultConfig() *Config {
	return &Config{
		MaxBudgetPerTask: 1.0,
		AllowNetwork:     true,
	}
}

// Engine enforces ethical constraints on agent actions.
type Engine struct {
	cfg *Config
	db  *sql.DB
	mu  sync.RWMutex

	// Budget tracking per task
	budgets map[string]float64 // taskID -> spent so far
}

// New creates a new ethics engine with SQLite audit log.
func New(dbPath string, cfg *Config) (*Engine, error) {
	if cfg == nil {
		cfg = DefaultConfig()
	}
	if dbPath == "" {
		dbPath = ":memory:"
	}

	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		return nil, fmt.Errorf("opening ethics db: %w", err)
	}

	if err := migrateEthics(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrating ethics db: %w", err)
	}

	return &Engine{
		cfg:     cfg,
		db:      db,
		budgets: make(map[string]float64),
	}, nil
}

func migrateEthics(db *sql.DB) error {
	_, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS audit_log (
			id         INTEGER PRIMARY KEY AUTOINCREMENT,
			timestamp  INTEGER NOT NULL,
			agent_id   TEXT NOT NULL,
			task_id    TEXT NOT NULL,
			action     TEXT NOT NULL,
			decision   TEXT NOT NULL,
			level      TEXT NOT NULL,
			reason     TEXT NOT NULL
		);
		CREATE INDEX IF NOT EXISTS idx_audit_agent ON audit_log(agent_id);
		CREATE INDEX IF NOT EXISTS idx_audit_task ON audit_log(task_id);
		CREATE INDEX IF NOT EXISTS idx_audit_time ON audit_log(timestamp);
	`)
	return err
}

// ---- L0 Hard Constraints ----

// Destructive command patterns that are always blocked.
var destructivePatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)\brm\s+(-[a-z]*r[a-z]*f|-[a-z]*f[a-z]*r)\s+/\s*$`), // rm -rf /
	regexp.MustCompile(`(?i)\brm\s+(-[a-z]*r[a-z]*f|-[a-z]*f[a-z]*r)\s+/\*`),    // rm -rf /*
	regexp.MustCompile(`(?i)\brm\s+(-[a-z]*r[a-z]*f|-[a-z]*f[a-z]*r)\s+~\s*$`),   // rm -rf ~
	regexp.MustCompile(`(?i)\bmkfs\b`),           // mkfs.*
	regexp.MustCompile(`(?i)\bdd\s+.*of=/dev/`),  // dd of=/dev/*
	regexp.MustCompile(`(?i)>\s*/dev/sd[a-z]`),   // redirect to disk device
	regexp.MustCompile(`(?i)\bformat\s+[a-z]:`),  // Windows format
	regexp.MustCompile(`(?i):(){ :\|:& };:`),     // fork bomb
	regexp.MustCompile(`(?i)\bshutdown\b`),       // shutdown
	regexp.MustCompile(`(?i)\breboot\b`),         // reboot
	regexp.MustCompile(`(?i)\bhalt\b`),           // halt
	regexp.MustCompile(`(?i)\binit\s+0`),         // init 0
}

// Data exfiltration patterns
var exfilPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)\bcurl\b.*(-d|--data).*\b(password|secret|token|key|credential)\b`),
	regexp.MustCompile(`(?i)\bwget\b.*--post-data.*\b(password|secret|token|key|credential)\b`),
	regexp.MustCompile(`(?i)\bnc\s+`),                   // netcat
	regexp.MustCompile(`(?i)/dev/tcp/`),                 // bash tcp
	regexp.MustCompile(`(?i)\bscp\b.*\b(\.ssh|\.gnupg|\.aws)\b`), // scp private dirs
}

// checkL0 verifies hard constraints. These cannot be overridden.
func checkL0(action string) (Decision, string) {
	for _, pat := range destructivePatterns {
		if pat.MatchString(action) {
			return Deny, fmt.Sprintf("L0: destructive command blocked: matches %s", pat.String())
		}
	}

	for _, pat := range exfilPatterns {
		if pat.MatchString(action) {
			return Deny, fmt.Sprintf("L0: potential data exfiltration blocked: matches %s", pat.String())
		}
	}

	return Allow, ""
}

// ---- L1 Soft Constraints ----

func (e *Engine) checkL1(action string) (Decision, string) {
	e.mu.RLock()
	defer e.mu.RUnlock()

	// Check denied commands
	for _, prefix := range e.cfg.DeniedCommands {
		if strings.HasPrefix(strings.TrimSpace(action), prefix) {
			return Deny, fmt.Sprintf("L1: command prefix denied: %s", prefix)
		}
	}

	// Check allowed commands (if allow list is set, everything else is denied)
	if len(e.cfg.AllowedCommands) > 0 {
		allowed := false
		for _, prefix := range e.cfg.AllowedCommands {
			if strings.HasPrefix(strings.TrimSpace(action), prefix) {
				allowed = true
				break
			}
		}
		if !allowed {
			return Deny, "L1: command not in allow list"
		}
	}

	// Check custom rules
	for _, rule := range e.cfg.CustomRules {
		decision, reason := rule.Check(action)
		if decision == Deny {
			return Deny, fmt.Sprintf("L1 rule %q: %s", rule.Name, reason)
		}
	}

	return Allow, ""
}

// ---- Budget Tracking ----

// RecordCost records LLM cost for a task.
func (e *Engine) RecordCost(taskID string, cost float64) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.budgets[taskID] += cost
}

// CheckBudget verifies the task hasn't exceeded its budget.
func (e *Engine) CheckBudget(taskID string) (Decision, string) {
	e.mu.RLock()
	defer e.mu.RUnlock()

	spent := e.budgets[taskID]
	if e.cfg.MaxBudgetPerTask > 0 && spent >= e.cfg.MaxBudgetPerTask {
		return Deny, fmt.Sprintf("L0: budget exceeded for task %s: $%.4f >= $%.4f", taskID, spent, e.cfg.MaxBudgetPerTask)
	}
	return Allow, ""
}

// ---- Main Check Entry Point ----

// Check evaluates an action against all constraint layers.
// Returns the decision, level that triggered it, and reason.
func (e *Engine) Check(agentID, taskID, action string) (Decision, Level, string) {
	// L0 hard constraints always first
	if dec, reason := checkL0(action); dec == Deny {
		e.logAudit(agentID, taskID, action, Deny, LevelL0, reason)
		return Deny, LevelL0, reason
	}

	// L0 budget check
	if dec, reason := e.CheckBudget(taskID); dec == Deny {
		e.logAudit(agentID, taskID, action, Deny, LevelL0, reason)
		return Deny, LevelL0, reason
	}

	// L1 soft constraints
	if dec, reason := e.checkL1(action); dec == Deny {
		e.logAudit(agentID, taskID, action, Deny, LevelL1, reason)
		return Deny, LevelL1, reason
	}

	// Allowed
	e.logAudit(agentID, taskID, action, Allow, LevelL1, "all checks passed")
	return Allow, LevelL1, "all checks passed"
}

// ---- Audit Logging ----

func (e *Engine) logAudit(agentID, taskID, action string, dec Decision, lvl Level, reason string) {
	_, err := e.db.Exec(`
		INSERT INTO audit_log (timestamp, agent_id, task_id, action, decision, level, reason)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`, time.Now().Unix(), agentID, taskID, action, string(dec), string(lvl), reason)
	if err != nil {
		// Log but don't fail — audit is best-effort
		fmt.Printf("⚠️  ethics audit log error: %v\n", err)
	}
}

// AuditLog retrieves recent audit entries.
func (e *Engine) AuditLog(limit int) ([]AuditEntry, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := e.db.Query(`
		SELECT id, timestamp, agent_id, task_id, action, decision, level, reason
		FROM audit_log ORDER BY id DESC LIMIT ?
	`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var entries []AuditEntry
	for rows.Next() {
		var entry AuditEntry
		if err := rows.Scan(&entry.ID, &entry.Timestamp, &entry.AgentID, &entry.TaskID,
			&entry.Action, &entry.Decision, &entry.Level, &entry.Reason); err != nil {
			return nil, err
		}
		entries = append(entries, entry)
	}
	return entries, rows.Err()
}

// Close releases the ethics engine resources.
func (e *Engine) Close() error {
	return e.db.Close()
}
