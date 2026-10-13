package prompt

// 本测试已注释：TaskPrompt 不再注入 skills 信息（技能改由
// gobrave/internal/agent/skill 统一通过系统提示词注入），原断言随之失效。
//
// import (
// 	"os"
// 	"path/filepath"
// 	"testing"
//
// 	"github.com/biox-dev/opencode/internal/config"
// 	"github.com/biox-dev/opencode/internal/llm/models"
// 	"github.com/biox-dev/opencode/internal/skills"
// 	"github.com/stretchr/testify/require"
// )
//
// func TestTaskPromptIncludesSkillsInfo(t *testing.T) {
// 	tmpDir := t.TempDir()
// 	_, err := config.Load(tmpDir, false)
// 	require.NoError(t, err)
//
// 	cfg := config.Get()
// 	cfg.WorkingDir = tmpDir
//
// 	skillDir := filepath.Join(tmpDir, ".opencode", "skills", "task-skill-test")
// 	require.NoError(t, os.MkdirAll(skillDir, 0o755))
// 	require.NoError(t, os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(`---
// name: task-skill-test
// description: Skill used by task prompt test
// ---
// Test instructions.
// `), 0o644))
//
// 	paths, err := skills.DefaultPaths(config.WorkingDirectory())
// 	require.NoError(t, err)
// 	loaded := skills.Discover(paths)
// 	require.NotEmpty(t, loaded)
//
// 	promptText := TaskPrompt(models.ProviderAnthropic)
// 	require.Contains(t, promptText, "# Skills Information")
// 	require.Contains(t, promptText, "task-skill-test")
// }
