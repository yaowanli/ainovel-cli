package style

import (
	"github.com/voocel/agentcore/schema"
	"github.com/voocel/ainovel-cli/internal/llmcontract"
)

// skillList 的描述统一带上"每条 ≤50 字"：compact 阶段会丢弃超长条目，
// 契约里不写清楚，模型很容易给出 200 字的长段，蒸馏结果会被整条丢掉。
func skillList(description string) map[string]any {
	return schema.Array(description+"；每条不超过 50 字", schema.String(description))
}

var sourceReportContract = llmcontract.Contract{
	Name:        "style_skill_source_report",
	Description: "从单篇参考语料提炼写作与对话风格规则，只沉淀模式，不复制原文表达",
	Schema: schema.Object(
		schema.Property("summary", schema.String("这篇语料的写法与说话方式概括")).Required(),
		schema.Property("narrative_voice", skillList("叙述人称、叙述距离与信息控制")).Required(),
		schema.Property("sentence_rhythm", skillList("句式节奏与长短句配比")).Required(),
		schema.Property("description_texture", skillList("描写质感与意象偏好")).Required(),
		schema.Property("pacing", skillList("场景推进与信息释放节奏")).Required(),
		schema.Property("speech_tags", skillList("对白标签习惯：说/道/问/答等标签动词偏好、裸对白比例")).Required(),
		schema.Property("forms_of_address", skillList("称谓习惯：人称、敬称、职务称呼")).Required(),
		schema.Property("modal_particles", skillList("语气词偏好：句末与句中虚词")).Required(),
		schema.Property("verbal_tics", skillList("口头禅的抽象模式（只写类别与位置，不写具体字面）")).Required(),
		schema.Property("line_length", skillList("台词长度感与对白段落形态")).Required(),
		schema.Property("dialect", skillList("方言与口音特征（抽象描述，不照搬方言词）")).Required(),
		schema.Property("taboos", skillList("禁止照搬的原文表达类别")).Required(),
	),
}

var skillsContract = llmcontract.Contract{
	Name:        "style_skills",
	Description: "把语料报告蒸馏成短且可执行的写作与对话风格 skill",
	Schema: schema.Object(
		schema.Property("prose_skill", schema.Object(
			schema.Property("narrative_voice", skillList("叙述声音规则")).Required(),
			schema.Property("sentence_rhythm", skillList("句式节奏规则")).Required(),
			schema.Property("description_texture", skillList("描写质地规则")).Required(),
			schema.Property("pacing", skillList("节奏规则")).Required(),
		)).Required(),
		schema.Property("dialogue_skill", schema.Object(
			schema.Property("speech_tags", skillList("对白标签习惯")).Required(),
			schema.Property("forms_of_address", skillList("称谓规则")).Required(),
			schema.Property("modal_particles", skillList("语气词规则")).Required(),
			schema.Property("verbal_tics", skillList("口头禅模式规则")).Required(),
			schema.Property("line_length", skillList("台词长度规则")).Required(),
			schema.Property("dialect", skillList("方言与口音规则")).Required(),
		)).Required(),
		schema.Property("taboos", skillList("写作时必须避免的照搬与套用")).Required(),
	),
}
