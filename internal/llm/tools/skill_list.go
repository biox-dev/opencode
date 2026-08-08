package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/gobravedev/opencode/internal/config"
	"github.com/gobravedev/opencode/internal/skills"
)

type SkillListParams struct {
	IncludeDisabled bool `json:"include_disabled"`
	IncludeErrors   bool `json:"include_errors"`
}

type SkillListItem struct {
	Name                   string `json:"name"`
	Description            string `json:"description"`
	SkillFilePath          string `json:"skill_file_path"`
	UserInvocable          bool   `json:"user_invocable"`
	DisableModelInvocation bool   `json:"disable_model_invocation"`
}

type SkillListStateItem struct {
	Name  string `json:"name"`
	Path  string `json:"path"`
	State string `json:"state"`
	Error string `json:"error,omitempty"`
}

type SkillListResponseMetadata struct {
	Paths  []string             `json:"paths"`
	Skills []SkillListItem      `json:"skills"`
	States []SkillListStateItem `json:"states,omitempty"`
}

type skillListTool struct{}

const (
	SkillListToolName    = "skill_list"
	skillListDescription = `Skill listing tool that returns currently discovered skills for model routing.

WHEN TO USE THIS TOOL:
- Use before skill_load when you need to decide which skill best matches the user request
- Use to inspect which skills are currently available in this workspace

HOW TO USE:
- Call without parameters for normal model routing (only model-invocable skills)
- Set include_disabled=true to include disabled skills
- Set include_errors=true to include discovery/validation errors

OUTPUT:
- Returns a concise list of skills and metadata
- Includes file paths so you can call skill_load with a selected skill name`
)

func NewSkillListTool() BaseTool {
	return &skillListTool{}
}

func (s *skillListTool) Info() ToolInfo {
	return ToolInfo{
		Name:        SkillListToolName,
		Description: skillListDescription,
		Parameters: map[string]any{
			"include_disabled": map[string]any{
				"type":        "boolean",
				"description": "Include skills with disable-model-invocation=true. Defaults to false.",
			},
			"include_errors": map[string]any{
				"type":        "boolean",
				"description": "Include discovery/validation errors in response metadata. Defaults to false.",
			},
		},
		Required: []string{},
	}
}

func (s *skillListTool) Run(ctx context.Context, call ToolCall) (ToolResponse, error) {
	var params SkillListParams
	if strings.TrimSpace(call.Input) != "" {
		if err := json.Unmarshal([]byte(call.Input), &params); err != nil {
			return NewTextErrorResponse(fmt.Sprintf("error parsing parameters: %s", err)), nil
		}
	}

	paths, err := skills.DefaultPaths(config.WorkingDirectory())
	if err != nil {
		return ToolResponse{}, fmt.Errorf("failed to resolve skill paths: %w", err)
	}

	loaded, states := skills.DiscoverWithStates(paths)
	items := make([]SkillListItem, 0, len(loaded))
	for _, skill := range loaded {
		if !params.IncludeDisabled && skill.DisableModelInvocation {
			continue
		}
		items = append(items, SkillListItem{
			Name:                   skill.Name,
			Description:            skill.Description,
			SkillFilePath:          skill.SkillFilePath,
			UserInvocable:          skill.UserInvocable,
			DisableModelInvocation: skill.DisableModelInvocation,
		})
	}

	sort.SliceStable(items, func(i, j int) bool {
		return strings.ToLower(items[i].Name) < strings.ToLower(items[j].Name)
	})

	var sb strings.Builder
	sb.WriteString("Available skills:\n")
	if len(items) == 0 {
		sb.WriteString("- (none)\n")
	} else {
		for _, item := range items {
			invocation := "enabled"
			if item.DisableModelInvocation {
				invocation = "disabled"
			}
			sb.WriteString(fmt.Sprintf("- %s: %s (invocation=%s, path=%s)\n", item.Name, item.Description, invocation, item.SkillFilePath))
		}
	}

	stateItems := make([]SkillListStateItem, 0)
	if params.IncludeErrors {
		for _, st := range states {
			if st.State != skills.StateError {
				continue
			}
			errText := ""
			if st.Err != nil {
				errText = st.Err.Error()
			}
			name := st.Name
			if name == "" {
				name = "-"
			}
			stateItems = append(stateItems, SkillListStateItem{
				Name:  name,
				Path:  st.Path,
				State: "error",
				Error: errText,
			})
		}
		if len(stateItems) > 0 {
			sb.WriteString("\nDiscovery errors:\n")
			for _, st := range stateItems {
				sb.WriteString(fmt.Sprintf("- %s (%s): %s\n", st.Name, st.Path, st.Error))
			}
		}
	}

	metadata := SkillListResponseMetadata{
		Paths:  paths,
		Skills: items,
		States: stateItems,
	}

	return WithResponseMetadata(NewTextResponse(strings.TrimSpace(sb.String())), metadata), nil
}
