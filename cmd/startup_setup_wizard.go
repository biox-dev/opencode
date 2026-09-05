package cmd

import (
	"fmt"
	"os"
	"slices"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/biox-dev/opencode/internal/config"
	"github.com/biox-dev/opencode/internal/llm/models"
)

type startupSetupStep int

const (
	startupSelectProviderStep startupSetupStep = iota
	startupInputAPIKeyStep
	startupSelectModelStep
	startupDoneStep
	startupCancelledStep
)

type startupSetupResult struct {
	ModelID  models.ModelID
	Provider models.ModelProvider
	APIKey   string
}

type startupSetupModel struct {
	step startupSetupStep

	providers           []models.ModelProvider
	selectedProviderIdx int
	selectedProvider    models.ModelProvider

	models           []models.Model
	selectedModelIdx int

	selectedModel models.Model
	requiresKey   bool
	apiKeyEnvName string
	apiKeyInput   textinput.Model
	apiKey        string
	errMsg        string
}

func newStartupSetupModel() startupSetupModel {
	return startupSetupModel{
		step:                startupSelectProviderStep,
		providers:           getStartupWizardProviders(),
		selectedProviderIdx: 0,
		models:              []models.Model{},
		selectedModelIdx:    0,
		selectedModel:       models.Model{},
	}
}

func (m startupSetupModel) Init() tea.Cmd {
	return nil
}

func (m startupSetupModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch m.step {
		case startupSelectProviderStep:
			return m.updateSelectProviderStep(msg)
		case startupInputAPIKeyStep:
			return m.updateAPIKeyStep(msg)
		case startupSelectModelStep:
			return m.updateSelectModelStep(msg)
		}
	}

	if m.step == startupInputAPIKeyStep {
		var cmd tea.Cmd
		m.apiKeyInput, cmd = m.apiKeyInput.Update(msg)
		return m, cmd
	}

	return m, nil
}

func (m startupSetupModel) updateSelectProviderStep(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c", "esc", "q":
		m.step = startupCancelledStep
		return m, tea.Quit
	case "up", "k":
		if len(m.providers) == 0 {
			return m, nil
		}
		if m.selectedProviderIdx > 0 {
			m.selectedProviderIdx--
		} else {
			m.selectedProviderIdx = len(m.providers) - 1
		}
		return m, nil
	case "down", "j":
		if len(m.providers) == 0 {
			return m, nil
		}
		if m.selectedProviderIdx < len(m.providers)-1 {
			m.selectedProviderIdx++
		} else {
			m.selectedProviderIdx = 0
		}
		return m, nil
	case "enter":
		if len(m.providers) == 0 {
			m.errMsg = "No supported providers available for interactive setup"
			return m, nil
		}

		selectedProvider := m.providers[m.selectedProviderIdx]
		m.selectedProvider = selectedProvider
		m.models = getStartupWizardModelsForProvider(selectedProvider)
		m.selectedModelIdx = 0
		m.selectedModel = models.Model{}
		m.requiresKey = providerNeedsAPIKey(selectedProvider)
		m.apiKeyEnvName = providerAPIKeyName(selectedProvider)
		m.errMsg = ""

		if len(m.models) == 0 {
			m.errMsg = fmt.Sprintf("No models available for provider %s", selectedProvider)
			return m, nil
		}

		if !m.requiresKey {
			m.step = startupSelectModelStep
			return m, nil
		}

		ti := textinput.New()
		ti.Placeholder = fmt.Sprintf("Input %s", m.apiKeyEnvName)
		ti.Prompt = "> "
		ti.EchoMode = textinput.EchoPassword
		ti.EchoCharacter = '*'
		ti.Focus()
		m.apiKey = ""
		m.apiKeyInput = ti
		m.step = startupInputAPIKeyStep
		return m, textinput.Blink
	}

	return m, nil
}

func (m startupSetupModel) updateAPIKeyStep(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c", "q":
		m.step = startupCancelledStep
		return m, tea.Quit
	case "esc":
		m.step = startupSelectProviderStep
		m.errMsg = ""
		return m, nil
	case "enter":
		input := strings.TrimSpace(m.apiKeyInput.Value())
		if input == "" {
			m.errMsg = fmt.Sprintf("%s cannot be empty", m.apiKeyEnvName)
			return m, nil
		}

		m.apiKey = input
		m.errMsg = ""
		m.step = startupSelectModelStep
		return m, nil
	}

	var cmd tea.Cmd
	m.apiKeyInput, cmd = m.apiKeyInput.Update(msg)
	return m, cmd
}

func (m startupSetupModel) updateSelectModelStep(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c", "q":
		m.step = startupCancelledStep
		return m, tea.Quit
	case "esc":
		if m.requiresKey {
			m.step = startupInputAPIKeyStep
			return m, nil
		}
		m.step = startupSelectProviderStep
		return m, nil
	case "up", "k":
		if len(m.models) == 0 {
			return m, nil
		}
		if m.selectedModelIdx > 0 {
			m.selectedModelIdx--
		} else {
			m.selectedModelIdx = len(m.models) - 1
		}
		return m, nil
	case "down", "j":
		if len(m.models) == 0 {
			return m, nil
		}
		if m.selectedModelIdx < len(m.models)-1 {
			m.selectedModelIdx++
		} else {
			m.selectedModelIdx = 0
		}
		return m, nil
	case "enter":
		if len(m.models) == 0 {
			m.errMsg = "No supported models available for selected provider"
			return m, nil
		}
		m.selectedModel = m.models[m.selectedModelIdx]
		m.step = startupDoneStep
		return m, tea.Quit
	}

	return m, nil
}

func (m startupSetupModel) View() string {
	if m.step == startupSelectProviderStep {
		return m.renderProviderSelection()
	}
	if m.step == startupInputAPIKeyStep {
		return m.renderAPIKeyInput()
	}

	return m.renderModelSelection()
}

func (m startupSetupModel) renderProviderSelection() string {
	var b strings.Builder
	b.WriteString("OpenCode setup required: no available model/provider configured\n\n")
	b.WriteString("Select a provider (up/down, enter confirm, esc cancel):\n\n")

	if len(m.providers) == 0 {
		b.WriteString("  No providers available\n")
		return b.String()
	}

	for i, provider := range m.providers {
		cursor := "  "
		if i == m.selectedProviderIdx {
			cursor = "> "
		}
		b.WriteString(fmt.Sprintf("%s%s\n", cursor, providerLabel(provider)))
	}

	if m.errMsg != "" {
		b.WriteString("\nError: ")
		b.WriteString(m.errMsg)
		b.WriteString("\n")
	}

	return b.String()
}

func (m startupSetupModel) renderModelSelection() string {
	var b strings.Builder
	b.WriteString("OpenCode setup required\n\n")
	b.WriteString(fmt.Sprintf("Selected provider: %s\n", providerLabel(m.selectedProvider)))
	b.WriteString("Select a model (up/down, enter confirm, esc back):\n\n")

	if len(m.models) == 0 {
		b.WriteString("  No models available\n")
		return b.String()
	}

	for i, model := range m.models {
		cursor := "  "
		if i == m.selectedModelIdx {
			cursor = "> "
		}
		b.WriteString(fmt.Sprintf("%s%s\n", cursor, model.Name))
	}

	if m.errMsg != "" {
		b.WriteString("\n")
		b.WriteString("Error: ")
		b.WriteString(m.errMsg)
		b.WriteString("\n")
	}

	return b.String()
}

func (m startupSetupModel) renderAPIKeyInput() string {
	var b strings.Builder
	b.WriteString("OpenCode setup required\n\n")
	b.WriteString(fmt.Sprintf("Selected provider: %s\n", providerLabel(m.selectedProvider)))
	b.WriteString(fmt.Sprintf("Input %s (enter confirm, esc back):\n\n", m.apiKeyEnvName))
	b.WriteString(m.apiKeyInput.View())
	b.WriteString("\n")

	if m.errMsg != "" {
		b.WriteString("\nError: ")
		b.WriteString(m.errMsg)
		b.WriteString("\n")
	}

	return b.String()
}

func runStartupSetupWizard() (startupSetupResult, error) {
	m := newStartupSetupModel()
	program := tea.NewProgram(
		m,
		tea.WithAltScreen(),
		tea.WithInput(os.Stdin),
		tea.WithOutput(os.Stdout),
	)

	finalModel, err := program.Run()
	if err != nil {
		return startupSetupResult{}, err
	}

	resultModel, ok := finalModel.(startupSetupModel)
	if !ok {
		return startupSetupResult{}, fmt.Errorf("unexpected setup model type")
	}

	if resultModel.step == startupCancelledStep {
		return startupSetupResult{}, fmt.Errorf("interactive setup canceled")
	}
	if resultModel.step != startupDoneStep {
		return startupSetupResult{}, fmt.Errorf("interactive setup did not complete")
	}

	return startupSetupResult{
		ModelID:  resultModel.selectedModel.ID,
		Provider: resultModel.selectedModel.Provider,
		APIKey:   resultModel.apiKey,
	}, nil
}

func applyStartupSetupResult(result startupSetupResult) error {
	if providerNeedsAPIKey(result.Provider) {
		if err := config.UpdateProviderAPIKey(result.Provider, result.APIKey); err != nil {
			return err
		}
	}

	agents := []config.AgentName{
		config.AgentCoder,
		config.AgentSummarizer,
		config.AgentTask,
		config.AgentTitle,
	}
	for _, agentName := range agents {
		if err := config.UpdateAgentModel(agentName, result.ModelID); err != nil {
			return fmt.Errorf("failed to set model for %s: %w", agentName, err)
		}
	}

	return nil
}

func shouldLaunchStartupSetup(err error) bool {
	if err == nil {
		return false
	}

	errText := strings.ToLower(err.Error())
	triggers := []string{
		"agent coder not found",
		"agent title not found",
		"agent summarizer not found",
		"provider",
		"not enabled",
		"not supported",
		"no valid provider",
	}
	for _, trigger := range triggers {
		if strings.Contains(errText, trigger) {
			return true
		}
	}
	return false
}

func getStartupWizardModels() []models.Model {
	return getStartupWizardModelsForProvider("")
}

func getStartupWizardProviders() []models.ModelProvider {
	allowed := map[models.ModelProvider]bool{
		models.ProviderAnthropic:  true,
		models.ProviderOpenAI:     true,
		models.ProviderDeepSeek:   true,
		models.ProviderGemini:     true,
		models.ProviderGROQ:       true,
		models.ProviderOpenRouter: true,
		models.ProviderXAI:        true,
		models.ProviderCopilot:    true,
	}

	providerSet := make(map[models.ModelProvider]bool)
	for _, model := range models.SupportedModels {
		if allowed[model.Provider] {
			providerSet[model.Provider] = true
		}
	}

	providers := make([]models.ModelProvider, 0, len(providerSet))
	for provider := range providerSet {
		providers = append(providers, provider)
	}

	slices.SortFunc(providers, func(a, b models.ModelProvider) int {
		rankA := models.ProviderPopularity[a]
		rankB := models.ProviderPopularity[b]
		if rankA == 0 {
			rankA = 999
		}
		if rankB == 0 {
			rankB = 999
		}
		if rankA != rankB {
			return rankA - rankB
		}
		return strings.Compare(string(a), string(b))
	})

	return providers
}

func getStartupWizardModelsForProvider(provider models.ModelProvider) []models.Model {
	allowed := map[models.ModelProvider]bool{
		models.ProviderAnthropic:  true,
		models.ProviderOpenAI:     true,
		models.ProviderDeepSeek:   true,
		models.ProviderGemini:     true,
		models.ProviderGROQ:       true,
		models.ProviderOpenRouter: true,
		models.ProviderXAI:        true,
		models.ProviderCopilot:    true,
	}

	available := make([]models.Model, 0, len(models.SupportedModels))
	for _, model := range models.SupportedModels {
		if !allowed[model.Provider] {
			continue
		}
		if provider != "" && model.Provider != provider {
			continue
		}
		available = append(available, model)
	}

	slices.SortFunc(available, func(a, b models.Model) int {
		return strings.Compare(a.Name, b.Name)
	})

	return available
}

func providerLabel(provider models.ModelProvider) string {
	name := string(provider)
	if name == "" {
		return "Unknown"
	}
	return strings.ToUpper(name[:1]) + name[1:]
}

func providerNeedsAPIKey(provider models.ModelProvider) bool {
	switch provider {
	case models.ProviderAnthropic,
		models.ProviderOpenAI,
		models.ProviderDeepSeek,
		models.ProviderGemini,
		models.ProviderGROQ,
		models.ProviderOpenRouter,
		models.ProviderXAI,
		models.ProviderCopilot:
		return true
	default:
		return false
	}
}

func providerAPIKeyName(provider models.ModelProvider) string {
	switch provider {
	case models.ProviderAnthropic:
		return "ANTHROPIC_API_KEY"
	case models.ProviderOpenAI:
		return "OPENAI_API_KEY"
	case models.ProviderDeepSeek:
		return "DEEPSEEK_API_KEY"
	case models.ProviderGemini:
		return "GEMINI_API_KEY"
	case models.ProviderGROQ:
		return "GROQ_API_KEY"
	case models.ProviderOpenRouter:
		return "OPENROUTER_API_KEY"
	case models.ProviderXAI:
		return "XAI_API_KEY"
	case models.ProviderCopilot:
		return "GITHUB_TOKEN"
	default:
		return "API_KEY"
	}
}
