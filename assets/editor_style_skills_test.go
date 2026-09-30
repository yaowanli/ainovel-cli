package assets

import (
	"strings"
	"testing"
)

// editor 是唯一被要求拿 style_skills 当 AI 味标尺的角色：它审已写正文，
// 而 skill 正是"这本该写成什么样"的目标值。删掉这条等于把一个已经送达
// novel_context 的高价值信号重新变成死数据，所以用测试钉住。
func TestEditorPromptReferencesStyleSkillsAsAITicYardstick(t *testing.T) {
	prompt := loadPrompts().Editor
	if !strings.Contains(prompt, "style_skills") {
		t.Fatal("editor prompt must reference working_memory.style_skills")
	}

	// 必须与另两样依据并列出现，否则会被当成与 AI 味无关的旁支
	for _, anchor := range []string{"anti_ai_tone", "style_stats", "style_skills"} {
		if !strings.Contains(prompt, anchor) {
			t.Fatalf("editor aesthetic criteria must mention %q", anchor)
		}
	}

	// skill 的位置：novel_context 注入到 working_memory，editor 走 chapter>0 分支
	if !strings.Contains(prompt, "working_memory.style_skills") {
		t.Fatal("must name the exact container: working_memory.style_skills")
	}

	// 六个对话维度要写全，否则 editor 只看"标签+称谓"就交差
	for _, field := range []string{
		"dialogue_skill", "对白标签", "称谓", "语气词", "台词长度", "方言",
	} {
		if !strings.Contains(prompt, field) {
			t.Fatalf("editor prompt must name dialogue dimension %q", field)
		}
	}
}

// 三条边界防止新判据被滥用：照搬 skill 字面不算偏离、偏离不得单独升级 error、
// 合乎语料的写法不算缺陷。任一条丢失都会让 editor 拿它当机械门禁。
func TestEditorPromptConstrainsStyleSkillsYardstick(t *testing.T) {
	prompt := loadPrompts().Editor
	for _, guard := range []string{
		"不是可照抄的词表", // 防止把 skill 当词表比对字面
		"不得单独升级为",  // 防止偏离直接触发返工
		"不是枷锁",     // 防止惩罚偏离通用套路的合理创新
	} {
		if !strings.Contains(prompt, guard) {
			t.Fatalf("editor prompt must keep the guard %q", guard)
		}
	}

	// 偏离只能落在 warning：判定标准里要写明与 anti_ai_tone 的关系
	if !strings.Contains(prompt, "一律按 warning 处理") {
		t.Fatal("verdict criteria must pin style_skills deviation to warning")
	}
}

// writer 也挂 style_skillsGuidance，但它写新章、不审稿；确认 editor 侧那条
// 是唯一新增的"当标尺用"指令，没有误伤 writer 的行为约束。
func TestStyleSkillsGuidanceAppliesToBothWriterAndEditor(t *testing.T) {
	prompts := loadPrompts()
	for name, p := range map[string]string{
		"writer": prompts.Writer,
		"editor": prompts.Editor,
	} {
		if !strings.Contains(p, "## 风格 skill") {
			t.Fatalf("%s must carry the style skills guidance", name)
		}
	}
	// arbiter 不挂：它只做路由，不该被风格指引带偏
	if strings.Contains(prompts.ArbiterPlanStart, "## 风格 skill") {
		t.Fatal("arbiter must not carry the style skills guidance")
	}
}
