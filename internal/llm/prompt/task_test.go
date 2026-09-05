package prompt

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/biox-dev/opencode/internal/config"
	"github.com/biox-dev/opencode/internal/llm/models"
	"github.com/biox-dev/opencode/internal/skills"
	"github.com/stretchr/testify/require"
)

func TestTaskPromptIncludesSkillsInfo(t *testing.T) {
	tmpDir := t.TempDir()
	_, err := config.Load(tmpDir, false)
	require.NoError(t, err)

	cfg := config.Get()
	cfg.WorkingDir = tmpDir

	skillDir := filepath.Join(tmpDir, ".opencode", "skills", "task-skill-test")
	require.NoError(t, os.MkdirAll(skillDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(`---
name: task-skill-test
description: Skill used by task prompt test
---
Test instructions.
`), 0o644))

	paths, err := skills.DefaultPaths(config.WorkingDirectory())
	require.NoError(t, err)
	loaded := skills.Discover(paths)
	require.NotEmpty(t, loaded)

	promptText := TaskPrompt(models.ProviderAnthropic)
	require.Contains(t, promptText, "# Skills Information")
	require.Contains(t, promptText, "task-skill-test")
}
