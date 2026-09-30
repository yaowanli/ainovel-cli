package style

import (
	"context"
	"strings"
	"testing"

	"github.com/voocel/agentcore"
	"github.com/voocel/agentcore/llm"
	"github.com/voocel/ainovel-cli/internal/host/corpus"
	"github.com/voocel/ainovel-cli/internal/llmcontract"
)

func TestStyleContractsAreStrictReady(t *testing.T) {
	for _, contract := range []llmcontract.Contract{sourceReportContract, skillsContract} {
		if err := llmcontract.ValidateStrictReady(contract.Schema); err != nil {
			t.Fatalf("%s: %v", contract.Name, err)
		}
	}
}

// 六个对话维度必须都在契约里，否则模型没被要求产出，skill 就是空的。
func TestSourceReportContractCoversAllDialogueDimensions(t *testing.T) {
	props := sourceReportContract.Schema["properties"].(map[string]any)
	for _, field := range []string{
		"speech_tags", "forms_of_address", "modal_particles",
		"verbal_tics", "line_length", "dialect",
	} {
		if _, ok := props[field]; !ok {
			t.Fatalf("source report contract missing %q", field)
		}
	}
}

func TestSkillsContractNestsAllDialogueDimensions(t *testing.T) {
	props := skillsContract.Schema["properties"].(map[string]any)
	dialogue, ok := props["dialogue_skill"].(map[string]any)
	if !ok {
		t.Fatal("dialogue_skill missing")
	}
	inner := dialogue["properties"].(map[string]any)
	for _, field := range []string{
		"speech_tags", "forms_of_address", "modal_particles",
		"verbal_tics", "line_length", "dialect",
	} {
		if _, ok := inner[field]; !ok {
			t.Fatalf("dialogue_skill missing %q", field)
		}
	}
}

// skillList 把 50 字上限写进描述；compact 阶段会丢弃超长条目，
// 契约不写清楚就会静默丢条目。
func TestSkillListMentionsLengthCap(t *testing.T) {
	if !strings.Contains(skillList("对白标签习惯")["description"].(string), "50 字") {
		t.Fatal("skill list description must state the 50-rune cap")
	}
}

type nativeStyleModel struct {
	response string
	messages []agentcore.Message
	config   agentcore.CallConfig
}

func (m *nativeStyleModel) Capabilities() llm.Capabilities {
	return llm.Capabilities{
		Provider:   "openai",
		Model:      "gpt-test",
		Structured: llm.StructuredCapabilities{JSONSchema: llm.SupportYes, Strict: llm.SupportYes},
	}
}

func (m *nativeStyleModel) Generate(_ context.Context, messages []agentcore.Message, _ []agentcore.ToolSpec, opts ...agentcore.CallOption) (*agentcore.LLMResponse, error) {
	m.messages = messages
	m.config = agentcore.ResolveCallConfig(opts)
	return &agentcore.LLMResponse{Message: agentcore.Message{
		Role:       agentcore.RoleAssistant,
		Content:    []agentcore.ContentBlock{agentcore.TextBlock(m.response)},
		StopReason: agentcore.StopReasonStop,
	}}, nil
}

func TestAnalyzeSourceUsesNativeSchema(t *testing.T) {
	model := &nativeStyleModel{response: validSourceReportJSON("清晰摘要")}
	report, err := AnalyzeSource(t.Context(), model, "只分析风格模式。", corpus.Source{RelativePath: "a.txt"})
	if err != nil {
		t.Fatalf("AnalyzeSource: %v", err)
	}
	if report.Summary == "" {
		t.Fatal("summary 为空")
	}
	format := model.config.ResponseFormat
	if format == nil || format.JSONSchema == nil || format.JSONSchema.Name != sourceReportContract.Name {
		t.Fatalf("response format = %#v", format)
	}
	if strings.Contains(model.messages[0].TextContent(), "<output-json-schema>") {
		t.Fatalf("native prompt 不应注入 schema: %s", model.messages[0].TextContent())
	}
}

func TestAnalyzeSourcePromptModeRepairsMissingRequiredFields(t *testing.T) {
	model := &scriptedLLM{responses: []string{
		`{}`,
		validSourceReportJSON("修正后的摘要"),
	}}
	report, err := AnalyzeSource(t.Context(), model, "只分析风格模式。", corpus.Source{RelativePath: "a.txt"})
	if err != nil {
		t.Fatalf("AnalyzeSource: %v", err)
	}
	if report.Summary != "修正后的摘要" || model.calls.Load() != 2 {
		t.Fatalf("缺字段后应反馈自愈: report=%+v calls=%d", report, model.calls.Load())
	}
}

var _ LLMChat = (*nativeStyleModel)(nil)
