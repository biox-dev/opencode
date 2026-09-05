package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/biox-dev/opencode/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSkillListTool_Info(t *testing.T) {
	tool := NewSkillListTool()
	info := tool.Info()

	assert.Equal(t, SkillListToolName, info.Name)
	assert.NotEmpty(t, info.Description)
	assert.Contains(t, info.Parameters, "include_disabled")
	assert.Contains(t, info.Parameters, "include_errors")
}

func TestSkillListTool_Run(t *testing.T) {
	tmpDir := t.TempDir()
	_, err := config.Load(tmpDir, false)
	require.NoError(t, err)

	invocablePath := filepath.Join(tmpDir, ".opencode", "skills", "skill-list-invocable", "SKILL.md")
	require.NoError(t, os.MkdirAll(filepath.Dir(invocablePath), 0o755))
	require.NoError(t, os.WriteFile(invocablePath, []byte(`---
name: skill-list-invocable
description: Invocable skill for list testing
---
Do invocable workflow.
`), 0o644))

	disabledPath := filepath.Join(tmpDir, ".opencode", "skills", "skill-list-disabled", "SKILL.md")
	require.NoError(t, os.MkdirAll(filepath.Dir(disabledPath), 0o755))
	require.NoError(t, os.WriteFile(disabledPath, []byte(`---
name: skill-list-disabled
description: Disabled skill for list testing
disable-model-invocation: true
---
Do disabled workflow.
`), 0o644))

	invalidPath := filepath.Join(tmpDir, ".opencode", "skills", "skill-list-invalid", "SKILL.md")
	require.NoError(t, os.MkdirAll(filepath.Dir(invalidPath), 0o755))
	require.NoError(t, os.WriteFile(invalidPath, []byte(`---
name: invalid_name
description: invalid sample
---
Should trigger validation error.
`), 0o644))

	tool := NewSkillListTool()

	t.Run("default excludes disabled skills", func(t *testing.T) {
		resp, err := tool.Run(context.Background(), ToolCall{Name: SkillListToolName, Input: `{}`})
		require.NoError(t, err)
		assert.False(t, resp.IsError)
		assert.Contains(t, resp.Content, "skill-list-invocable")
		assert.NotContains(t, resp.Content, "skill-list-disabled")
	})

	t.Run("include_disabled includes disabled skills", func(t *testing.T) {
		params, err := json.Marshal(SkillListParams{IncludeDisabled: true})
		require.NoError(t, err)
		resp, err := tool.Run(context.Background(), ToolCall{Name: SkillListToolName, Input: string(params)})
		require.NoError(t, err)
		assert.False(t, resp.IsError)
		assert.Contains(t, resp.Content, "skill-list-invocable")
		assert.Contains(t, resp.Content, "skill-list-disabled")
	})

	t.Run("include_errors includes discovery error entries", func(t *testing.T) {
		params, err := json.Marshal(SkillListParams{IncludeErrors: true})
		require.NoError(t, err)
		resp, err := tool.Run(context.Background(), ToolCall{Name: SkillListToolName, Input: string(params)})
		require.NoError(t, err)
		assert.False(t, resp.IsError)
		assert.Contains(t, resp.Content, "Discovery errors")
		assert.Contains(t, resp.Content, "invalid_name")
	})

	t.Run("handles invalid parameters", func(t *testing.T) {
		resp, err := tool.Run(context.Background(), ToolCall{Name: SkillListToolName, Input: `{"include_disabled":`})
		require.NoError(t, err)
		assert.True(t, resp.IsError)
		assert.Contains(t, resp.Content, "error parsing parameters")
	})
}
