package tools

import "testing"

// 时代称谓错误与章节号无关：第 200 章照样会写「相公」。所以 era 表必须全程注入，
// 不能像 style_reference 那样只在前 3 章给。
func TestEraTerminologyInjectedInEveryChapter(t *testing.T) {
	ct := &ContextTool{}
	ct.refs = References{
		EraTerminology: "相公 → 郎君",
		StyleReference: "称谓不能混",
	}
	for _, chapter := range []int{1, 3, 4, 50, 235} {
		refs := ct.writerReferences(chapter)
		if refs["era_terminology"] == "" {
			t.Errorf("第 %d 章缺 era_terminology", chapter)
		}
	}
	// 对照：style_reference 仍只在前 3 章
	if ct.writerReferences(50)["style_reference"] != "" {
		t.Error("style_reference 不该出现在第 50 章（防回归）")
	}
	if ct.writerReferences(2)["style_reference"] == "" {
		t.Error("style_reference 应出现在第 2 章（防回归）")
	}
}

// architect 大纲阶段也要能拿到——人名、封号在大纲阶段就该定对。
func TestEraTerminologyInjectedForArchitect(t *testing.T) {
	ct := &ContextTool{}
	ct.refs = References{EraTerminology: "相公 → 郎君"}
	if got := ct.architectReferences()["era_terminology"]; got == "" {
		t.Error("architect 阶段缺 era_terminology")
	}
}

// 空表不得产出空 key，否则提示词里会出现一个空的参考段。
func TestEraTerminologyAbsentWhenEmpty(t *testing.T) {
	ct := &ContextTool{}
	ct.refs = References{}
	if _, ok := ct.writerReferences(1)["era_terminology"]; ok {
		t.Error("无表时不应出现 era_terminology 键")
	}
	if _, ok := ct.architectReferences()["era_terminology"]; ok {
		t.Error("无表时 architect 不应出现 era_terminology 键")
	}
}
