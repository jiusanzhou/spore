// Package agent — bootstrap-time skill loading.
//
// This file holds methods that run at agent startup to populate the skill
// stores: importing skills declared in agent config, migrating from the legacy
// SQLite SkillStore to SkillFS, and seeding default skills when SkillFS is
// empty. None of this is task-time logic — it all runs once during New().
//
// Split out of agent.go during the Phase 3 refactor to keep agent.go focused
// on lifecycle (New/Run/Close).
package agent

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// importDeclaredSkills imports skills from agent config into the legacy skill store.
// DEPRECATED: use importDeclaredSkillsFS instead.
func (a *Agent) importDeclaredSkills() {
	if a.skillStore == nil {
		return
	}
	for _, skillName := range a.cfg.Agent.Skills {
		id := generateSkillID(skillName, "imported", "init")
		existing, _ := a.skillStore.GetSkill(id)
		if existing != nil {
			continue // already imported
		}
		// Check if any active skill with this name exists
		skills, _ := a.skillStore.ActiveSkills()
		found := false
		for _, s := range skills {
			if strings.EqualFold(s.Name, skillName) {
				found = true
				break
			}
		}
		if found {
			continue
		}

		rec := &SkillRecord{
			SkillID:     id,
			Name:        skillName,
			Description: fmt.Sprintf("Declared skill: %s", skillName),
			IsActive:    true,
			Origin:      SkillOriginImported,
			CreatedAt:   time.Now().UTC(),
			UpdatedAt:   time.Now().UTC(),
		}
		if err := a.skillStore.PutSkill(rec); err != nil {
			fmt.Printf("⚠️  [%s] Failed to import skill %s: %v\n", a.cfg.Agent.Name, skillName, err)
		}
	}
}

// importDeclaredSkillsFS imports skills from agent config into SkillFS.
func (a *Agent) importDeclaredSkillsFS() {
	if a.skillFS == nil {
		return
	}
	for _, skillName := range a.cfg.Agent.Skills {
		if _, exists := a.skillFS.Get(skillName); exists {
			continue
		}
		meta := SkillMeta{
			Name:        skillName,
			Description: fmt.Sprintf("Declared skill: %s", skillName),
			Category:    "declared",
			Origin:      "imported",
		}
		body := fmt.Sprintf("# %s\n\nDeclared skill from agent configuration.\n", skillName)
		if _, err := a.skillFS.Create(meta, body); err != nil {
			fmt.Printf("⚠️  [%s] Failed to import skill %s to SkillFS: %v\n", a.cfg.Agent.Name, skillName, err)
		}
	}
}

// migrateSkillStoreToFS migrates skills from the legacy SQLite SkillStore to SkillFS.
// Only runs if SkillFS is empty and legacy skills.db exists.
func (a *Agent) migrateSkillStoreToFS(workDir string) {
	if a.skillFS == nil {
		return
	}
	// Only migrate if SkillFS is empty
	if len(a.skillFS.List()) > 0 {
		return
	}

	legacyDB := filepath.Join(workDir, "skills", "skills.db")
	if _, err := os.Stat(legacyDB); os.IsNotExist(err) {
		return
	}

	legacy, err := NewSkillStore(filepath.Join(workDir, "skills"))
	if err != nil {
		return
	}
	defer legacy.Close()

	active, err := legacy.ActiveSkills()
	if err != nil || len(active) == 0 {
		return
	}

	migrated := 0
	for _, rec := range active {
		meta := SkillMeta{
			Name:        rec.Name,
			Description: rec.Description,
			Origin:      string(rec.Origin),
			Generation:  rec.Generation,
			ParentIDs:   rec.ParentIDs,
			SourceTask:  rec.SourceTaskID,
			CreatedAt:   rec.CreatedAt.Format(time.RFC3339),
		}
		// The old store kept everything in Description — use as body
		body := fmt.Sprintf("# %s\n\n%s\n", rec.Name, rec.Description)
		if rec.ChangeSummary != "" {
			body += fmt.Sprintf("\n## Change History\n%s\n", rec.ChangeSummary)
		}

		if _, err := a.skillFS.Create(meta, body); err != nil {
			fmt.Printf("⚠️  Migration: failed to migrate skill %s: %v\n", rec.Name, err)
			continue
		}
		migrated++
	}

	if migrated > 0 {
		fmt.Printf("📦 [%s] Migrated %d skills from legacy SkillStore to SkillFS\n", a.cfg.Agent.Name, migrated)
	}
}

// loadSeedSkillsFS imports default seed skills into SkillFS if no skills exist yet.
func (a *Agent) loadSeedSkillsFS() {
	if a.skillFS == nil {
		return
	}
	if len(a.skillFS.List()) > 0 {
		return // already have skills
	}

	seeds := DefaultSeedSkills()
	imported := 0
	for _, seed := range seeds {
		body := fmt.Sprintf("# %s\n\n", seed.Name)
		body += "## When to Use\n"
		if len(seed.Triggers) > 0 {
			body += fmt.Sprintf("Triggers: %s\n\n", strings.Join(seed.Triggers, ", "))
		}
		body += fmt.Sprintf("## Procedure\n%s\n", seed.Description)
		if len(seed.Dependencies) > 0 {
			body += fmt.Sprintf("\n## Dependencies\n%s\n", strings.Join(seed.Dependencies, ", "))
		}

		meta := SkillMeta{
			Name:         seed.Name,
			Description:  truncateStr(seed.Description, 200),
			Category:     seed.Category,
			Origin:       "imported",
			Triggers:     seed.Triggers,
			Priority:     seed.Priority,
			Dependencies: seed.Dependencies,
		}

		if _, err := a.skillFS.Create(meta, body); err != nil {
			fmt.Printf("⚠️  [%s] Failed to import seed skill %s: %v\n", a.cfg.Agent.Name, seed.Name, err)
			continue
		}
		imported++
	}

	if imported > 0 {
		fmt.Printf("🌱 [%s] Imported %d seed skills to SkillFS\n", a.cfg.Agent.Name, imported)
	}
}
