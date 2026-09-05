package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/biox-dev/opencode/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSkillLoadTool_Info(t *testing.T) {
	tool := NewSkillLoadTool()
	info := tool.Info()

	assert.Equal(t, SkillLoadToolName, info.Name)
	assert.NotEmpty(t, info.Description)
	assert.Contains(t, info.Parameters, "name")
	assert.Contains(t, info.Required, "name")
}

func TestSkillLoadTool_Run(t *testing.T) {
	tmpDir := t.TempDir()
	_, err := config.Load(tmpDir, false)
	require.NoError(t, err)

	uniqueSuffix := fmt.Sprintf("%d", time.Now().UnixNano())
	invocableName := "skill-load-test-invocable-" + uniqueSuffix
	disabledName := "skill-load-test-disabled-" + uniqueSuffix

	invocablePath := filepath.Join(tmpDir, ".opencode", "skills", invocableName, "SKILL.md")
	require.NoError(t, os.MkdirAll(filepath.Dir(invocablePath), 0o755))
	require.NoError(t, os.WriteFile(invocablePath, []byte("---\nname: "+invocableName+"\ndescription: Invocable test skill\n---\nFollow this workflow strictly.\n"), 0o644))

	disabledPath := filepath.Join(tmpDir, ".opencode", "skills", disabledName, "SKILL.md")
	require.NoError(t, os.MkdirAll(filepath.Dir(disabledPath), 0o755))
	require.NoError(t, os.WriteFile(disabledPath, []byte("---\nname: "+disabledName+"\ndescription: Disabled test skill\ndisable-model-invocation: true\n---\nShould not be loaded by model.\n"), 0o644))

	tool := NewSkillLoadTool()

	t.Run("loads full skill content by name", func(t *testing.T) {
		input, err := json.Marshal(SkillLoadParams{Name: invocableName})
		require.NoError(t, err)

		resp, err := tool.Run(context.Background(), ToolCall{Name: SkillLoadToolName, Input: string(input)})
		require.NoError(t, err)
		assert.False(t, resp.IsError)
		assert.Contains(t, resp.Content, "Loaded skill")
		assert.Contains(t, resp.Content, "name: "+invocableName)
		assert.Contains(t, resp.Content, "Follow this workflow strictly.")
		assert.NotEmpty(t, resp.Metadata)
	})

	t.Run("rejects disabled skill", func(t *testing.T) {
		input, err := json.Marshal(SkillLoadParams{Name: disabledName})
		require.NoError(t, err)

		resp, err := tool.Run(context.Background(), ToolCall{Name: SkillLoadToolName, Input: string(input)})
		require.NoError(t, err)
		assert.True(t, resp.IsError)
		assert.Contains(t, resp.Content, "disabled for model invocation")
	})

	t.Run("returns not found with available skills", func(t *testing.T) {
		input, err := json.Marshal(SkillLoadParams{Name: "skill-not-exists-" + uniqueSuffix})
		require.NoError(t, err)

		resp, err := tool.Run(context.Background(), ToolCall{Name: SkillLoadToolName, Input: string(input)})
		require.NoError(t, err)
		assert.True(t, resp.IsError)
		assert.Contains(t, resp.Content, "not found")
		assert.Contains(t, resp.Content, invocableName)
		assert.NotContains(t, resp.Content, disabledName)
	})

	t.Run("requires name", func(t *testing.T) {
		resp, err := tool.Run(context.Background(), ToolCall{Name: SkillLoadToolName, Input: `{}`})
		require.NoError(t, err)
		assert.True(t, resp.IsError)
		assert.Contains(t, resp.Content, "name is required")
	})
}
