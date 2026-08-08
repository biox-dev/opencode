package skills

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"
)

const (
	SkillFileName = "SKILL.md"

	MaxNameLength        = 64
	MaxDescriptionLength = 1024
)

var namePattern = regexp.MustCompile(`^[a-zA-Z0-9]+(-[a-zA-Z0-9]+)*$`)
var promptReplacer = strings.NewReplacer(
	"&", "&amp;",
	"<", "&lt;",
	">", "&gt;",
	"\"", "&quot;",
	"'", "&apos;",
)

type Skill struct {
	Name                   string            `yaml:"name" json:"name"`
	Description            string            `yaml:"description" json:"description"`
	UserInvocable          bool              `yaml:"user-invocable" json:"user_invocable"`
	DisableModelInvocation bool              `yaml:"disable-model-invocation" json:"disable_model_invocation"`
	License                string            `yaml:"license,omitempty" json:"license,omitempty"`
	Compatibility          string            `yaml:"compatibility,omitempty" json:"compatibility,omitempty"`
	Metadata               map[string]string `yaml:"metadata,omitempty" json:"metadata,omitempty"`

	Instructions  string `yaml:"-" json:"instructions"`
	Path          string `yaml:"-" json:"path"`
	SkillFilePath string `yaml:"-" json:"skill_file_path"`
}

type DiscoveryState int

const (
	StateNormal DiscoveryState = iota
	StateError
)

type SkillState struct {
	Name  string
	Path  string
	State DiscoveryState
	Err   error
}

func (s *Skill) Validate() error {
	var errs []error

	if s.Name == "" {
		errs = append(errs, errors.New("name is required"))
	} else {
		if len(s.Name) > MaxNameLength {
			errs = append(errs, fmt.Errorf("name exceeds %d characters", MaxNameLength))
		}
		if !namePattern.MatchString(s.Name) {
			errs = append(errs, errors.New("name must be alphanumeric with hyphens"))
		}
		if s.Path != "" && !strings.EqualFold(filepath.Base(s.Path), s.Name) {
			errs = append(errs, fmt.Errorf("name %q must match directory %q", s.Name, filepath.Base(s.Path)))
		}
	}

	if s.Description == "" {
		errs = append(errs, errors.New("description is required"))
	} else if len(s.Description) > MaxDescriptionLength {
		errs = append(errs, fmt.Errorf("description exceeds %d characters", MaxDescriptionLength))
	}

	return errors.Join(errs...)
}

func Parse(path string) (*Skill, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	skill, err := ParseContent(content)
	if err != nil {
		return nil, err
	}

	skill.Path = filepath.Dir(path)
	skill.SkillFilePath = path

	return skill, nil
}

func ParseContent(content []byte) (*Skill, error) {
	frontmatter, body, err := splitFrontmatter(string(content))
	if err != nil {
		return nil, err
	}

	var skill Skill
	if err := yaml.Unmarshal([]byte(frontmatter), &skill); err != nil {
		return nil, fmt.Errorf("parsing frontmatter: %w", err)
	}

	skill.Instructions = strings.TrimSpace(body)

	return &skill, nil
}

func splitFrontmatter(content string) (frontmatter, body string, err error) {
	content = strings.TrimPrefix(content, "\uFEFF")
	content = strings.ReplaceAll(content, "\r\n", "\n")
	content = strings.ReplaceAll(content, "\r", "\n")

	lines := strings.Split(content, "\n")
	start := slices.IndexFunc(lines, func(line string) bool {
		return strings.TrimSpace(line) != ""
	})
	if start == -1 || strings.TrimSpace(lines[start]) != "---" {
		return "", "", errors.New("no YAML frontmatter found")
	}

	endOffset := slices.IndexFunc(lines[start+1:], func(line string) bool {
		return strings.TrimSpace(line) == "---"
	})
	if endOffset == -1 {
		return "", "", errors.New("unclosed frontmatter")
	}

	end := start + 1 + endOffset
	frontmatter = strings.Join(lines[start+1:end], "\n")
	body = strings.Join(lines[end+1:], "\n")
	return frontmatter, body, nil
}

func Discover(paths []string) []*Skill {
	skills, _ := DiscoverWithStates(paths)
	return skills
}

func DiscoverWithStates(paths []string) ([]*Skill, []*SkillState) {
	var loaded []*Skill
	var states []*SkillState
	seen := map[string]bool{}

	addState := func(name, path string, state DiscoveryState, err error) {
		states = append(states, &SkillState{
			Name:  name,
			Path:  path,
			State: state,
			Err:   err,
		})
	}

	for _, base := range paths {
		walkErr := filepath.WalkDir(base, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				addState("", path, StateError, err)
				return nil
			}
			if d.IsDir() || d.Name() != SkillFileName {
				return nil
			}

			if seen[path] {
				return nil
			}
			seen[path] = true

			skill, parseErr := Parse(path)
			if parseErr != nil {
				addState("", path, StateError, parseErr)
				return nil
			}

			if validateErr := skill.Validate(); validateErr != nil {
				addState(skill.Name, path, StateError, validateErr)
				return nil
			}

			loaded = append(loaded, skill)
			addState(skill.Name, path, StateNormal, nil)
			return nil
		})
		if walkErr != nil && !os.IsNotExist(walkErr) {
			addState("", base, StateError, walkErr)
		}
	}

	slices.SortStableFunc(loaded, func(a, b *Skill) int {
		if c := strings.Compare(strings.ToLower(a.Path), strings.ToLower(b.Path)); c != 0 {
			return c
		}
		return strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name))
	})

	return loaded, states
}

func DefaultPaths(workingDir string) ([]string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("failed to resolve home directory: %w", err)
	}

	return []string{
		filepath.Join(home, ".opencode", "skills"),
		filepath.Join(workingDir, ".opencode", "skills"),
	}, nil
}

func ToPromptXML(skills []*Skill) string {
	if len(skills) == 0 {
		return ""
	}

	var sb strings.Builder
	sb.WriteString("<available_skills>\n")
	for _, s := range skills {
		if s.DisableModelInvocation {
			continue
		}
		sb.WriteString("  <skill>\n")
		fmt.Fprintf(&sb, "    <name>%s</name>\n", escapePromptValue(s.Name))
		fmt.Fprintf(&sb, "    <description>%s</description>\n", escapePromptValue(s.Description))
		fmt.Fprintf(&sb, "    <location>%s</location>\n", escapePromptValue(s.SkillFilePath))
		sb.WriteString("  </skill>\n")
	}
	sb.WriteString("</available_skills>")

	return sb.String()
}

func BuildPromptSection(paths []string) string {
	xml := ToPromptXML(Discover(paths))
	if xml == "" {
		return ""
	}

	return "# Skills Information\n" +
		"Skills may be available for specialized tasks. Prefer using relevant skills when they match the user's request.\n" +
		"When skills are needed, call `skill_list` first to inspect available options, then call `skill_load` for the selected skill name, read the full SKILL.md content, and follow it.\n" +
		xml
}

func escapePromptValue(s string) string {
	return promptReplacer.Replace(s)
}
