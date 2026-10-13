package prompt

import (
	"fmt"
	"strings"

	"github.com/biox-dev/opencode/internal/llm/models"
)

func TaskPrompt(_ models.ModelProvider) string {
	agentPrompt := `You are an agent for GoBrave. Given the user's prompt, you should use the tools available to you to answer the user's question.
Notes:
1. IMPORTANT: You should be concise, direct, and to the point, since your responses will be displayed on a command line interface. Answer the user's question directly, without elaboration, explanation, or details. One word answers are best. Avoid introductions, conclusions, and explanations. You MUST avoid text before/after your response, such as "The answer is <answer>.", "Here is the content of the file..." or "Based on the information provided, the answer is..." or "Here is what I will do next...".
2. When relevant, share file names and code snippets relevant to the query
3. Any file paths you return in your final response MUST be absolute. DO NOT use relative paths.`

	// skillsInformation() 已注释：技能提示词由 gobrave/internal/agent/skill 统一注入。
	sections := []string{agentPrompt, getEnvironmentInfo()}
	nonEmpty := make([]string, 0, len(sections))
	for _, section := range sections {
		if strings.TrimSpace(section) != "" {
			nonEmpty = append(nonEmpty, section)
		}
	}

	return fmt.Sprintf("%s\n", strings.Join(nonEmpty, "\n\n"))
}
