package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/biox-dev/opencode/internal/config"
	"github.com/biox-dev/opencode/internal/skills"
)

type SkillLoadParams struct {
	Name string `json:"name"`
}

type SkillLoadResponseMetadata struct {
	Name                   string `json:"name"`
	Description            string `json:"description"`
	Path                   string `json:"path"`
	SkillFilePath          string `json:"skill_file_path"`
	UserInvocable          bool   `json:"user_invocable"`
	DisableModelInvocation bool   `json:"disable_model_invocation"`
}

type skillLoadTool struct{}

const (
	SkillLoadToolName    = "skill_load"
	skillLoadDescription = `Skill loader tool that loads the full SKILL.md content for a specific skill by name.

WHEN TO USE THIS TOOL:
- Use after identifying a relevant skill from the available skills list
- Use before executing a specialized workflow that depends on skill instructions

HOW TO USE:
- Provide the skill name in the 'name' parameter
- Read the returned full SKILL.md content
- Follow the loaded instructions when answering the user

BEHAVIOR:
- Searches configured skill directories for a matching skill name
- Returns the complete SKILL.md content including frontmatter and instructions
- Rejects skills marked with disable-model-invocation=true`
)

func NewSkillLoadTool() BaseTool {
	return &skillLoadTool{}
}

func (s *skillLoadTool) Info() ToolInfo {
	return ToolInfo{
		Name:        SkillLoadToolName,
		Description: skillLoadDescription,
		Parameters: map[string]any{
			"name": map[string]any{
				"type":        "string",
				"description": "Skill name to load (for example: bug-triage)",
			},
		},
		Required: []string{"name"},
	}
}

func (s *skillLoadTool) Run(ctx context.Context, call ToolCall) (ToolResponse, error) {
	var params SkillLoadParams
	if err := json.Unmarshal([]byte(call.Input), &params); err != nil {
		return NewTextErrorResponse(fmt.Sprintf("error parsing parameters: %s", err)), nil
	}

	params.Name = strings.TrimSpace(params.Name)
	if params.Name == "" {
		return NewTextErrorResponse("name is required"), nil
	}

	paths, err := skills.DefaultPaths(config.WorkingDirectory())
	if err != nil {
		return ToolResponse{}, fmt.Errorf("failed to resolve skill paths: %w", err)
	}

	loaded := skills.Discover(paths)
	matches := make([]*skills.Skill, 0)
	for _, skill := range loaded {
		if strings.EqualFold(skill.Name, params.Name) {
			matches = append(matches, skill)
		}
	}

	if len(matches) == 0 {
		available := make([]string, 0, len(loaded))
		for _, skill := range loaded {
			if !skill.DisableModelInvocation {
				available = append(available, skill.Name)
			}
		}

		if len(available) == 0 {
			return NewTextErrorResponse(fmt.Sprintf("skill %q not found and no model-invocable skills are available", params.Name)), nil
		}

		return NewTextErrorResponse(fmt.Sprintf("skill %q not found. Available skills: %s", params.Name, strings.Join(available, ", "))), nil
	}

	if len(matches) > 1 {
		paths := make([]string, 0, len(matches))
		for _, skill := range matches {
			paths = append(paths, skill.SkillFilePath)
		}
		return NewTextErrorResponse(fmt.Sprintf("multiple skills found for %q: %s", params.Name, strings.Join(paths, ", "))), nil
	}

	matched := matches[0]
	if matched.DisableModelInvocation {
		return NewTextErrorResponse(fmt.Sprintf("skill %q is disabled for model invocation", matched.Name)), nil
	}

	raw, err := os.ReadFile(matched.SkillFilePath)
	if err != nil {
		return ToolResponse{}, fmt.Errorf("failed to read skill file: %w", err)
	}

	content := fmt.Sprintf("Loaded skill %q from %s\n\n%s", matched.Name, matched.SkillFilePath, string(raw))
	response := NewTextResponse(content)
	metadata := SkillLoadResponseMetadata{
		Name:                   matched.Name,
		Description:            matched.Description,
		Path:                   matched.Path,
		SkillFilePath:          matched.SkillFilePath,
		UserInvocable:          matched.UserInvocable,
		DisableModelInvocation: matched.DisableModelInvocation,
	}

	return WithResponseMetadata(response, metadata), nil
}
