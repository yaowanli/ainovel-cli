package rules

import (
	"strings"
	"testing"
)

// 替代项必须跟着违规一起报出去——writer 删掉禁词后该写什么，
// 不该由它自行推断（历史称谓猜错率高）。
func TestCheckTermCorrectionCarriesSuggestion(t *testing.T) {
	s := Structured{TermCorrections: []TermCorrection{{
		Banned: "沈相公",
		Use:    []string{"郎君", "先生", "茂才"},
		Note:   "相公明清才通行，东汉士人互称郎君/先生",
	}}}
	vs := Check("沈砚入门，沈相公正在灯下读简。", s)
	if len(vs) != 1 {
		t.Fatalf("应恰好 1 条违规，实际 %d: %+v", len(vs), vs)
	}
	v := vs[0]
	if v.Rule != "term_correction" {
		t.Errorf("rule 应为 term_correction，实际 %q", v.Rule)
	}
	if v.Severity != SeverityError {
		t.Errorf("应 error 级，实际 %q", v.Severity)
	}
	if strings.Join(v.Suggestion, ",") != "郎君,先生,茂才" {
		t.Errorf("替代项未带出: %v", v.Suggestion)
	}
	if v.Note == "" {
		t.Error("禁用理由未带出")
	}
}

// term_corrections 与 forbidden_phrases 是同一条检测路径的两种写法。
func TestCheckTermCorrectionAndForbiddenPhraseSamePath(t *testing.T) {
	// 同一个词写在两张表里：只报一条，且以信息更多的 term_correction 为准。
	s := Structured{
		ForbiddenPhrases: []string{"相公"},
		TermCorrections:  []TermCorrection{{Banned: "相公", Use: []string{"郎君"}}},
	}
	vs := Check("「相公，这契书按不按？」", s)
	if len(vs) != 1 {
		t.Fatalf("同一词不应重复报，实际 %d 条: %+v", len(vs), vs)
	}
	if vs[0].Rule != "term_correction" || len(vs[0].Suggestion) != 1 {
		t.Fatalf("应以 term_correction 为准: %+v", vs[0])
	}
}

func TestBannedTermsUnionDedup(t *testing.T) {
	s := Structured{
		ForbiddenPhrases: []string{"相公", "  大哥  ", "相公"},
		TermCorrections: []TermCorrection{
			{Banned: "相公", Use: []string{"郎君"}},
			{Banned: "  ", Use: []string{"x"}},
			{Banned: " 媳妇 "},
		},
	}
	got := s.BannedTerms()
	want := []string{"相公", "大哥", "媳妇"}
	if len(got) != len(want) {
		t.Fatalf("期望 %v，实际 %v", want, got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("第 %d 项期望 %q，实际 %q（顺序应稳定：forbidden_phrases 在前）", i, want[i], got[i])
		}
	}
}

func TestIsEmptyWithTermCorrectionsOnly(t *testing.T) {
	// 只有 term_corrections 也算有规则，否则 checker 会整体跳过，规则形同虚设。
	if (Structured{}).IsEmpty() != true {
		t.Error("空结构应 IsEmpty")
	}
	s := Structured{TermCorrections: []TermCorrection{{Banned: "相公"}}}
	if s.IsEmpty() {
		t.Error("仅有 term_corrections 时不应 IsEmpty")
	}
}

// 对照表是逐次累积的（今天加"沈相公"，明天加"相公"），整表覆盖会抹掉旧条目。
func TestMergeTermCorrectionsAccumulates(t *testing.T) {
	dst := []TermCorrection{{Banned: "沈相公", Use: []string{"郎君"}, Note: "第一版"}}
	got := mergeTermCorrections(dst, []TermCorrection{{Banned: "相公", Use: []string{"郎君", "先生"}}})
	if len(got) != 2 {
		t.Fatalf("新条目应叠加而非覆盖整表: %+v", got)
	}
	if got[0].Banned != "沈相公" || got[1].Banned != "相公" {
		t.Fatalf("顺序应为 旧→新: %+v", got)
	}
}

// 同键覆盖 = 用户改主意了，新的理由/替代项替换旧的。
func TestMergeTermCorrectionsSameKeyOverrides(t *testing.T) {
	dst := []TermCorrection{{Banned: "相公", Use: []string{"郎君"}, Note: "旧理由"}}
	got := mergeTermCorrections(dst, []TermCorrection{{Banned: "相公", Use: []string{"君"}, Note: "新理由"}})
	if len(got) != 1 {
		t.Fatalf("同键应覆盖，期望 1 条，实际 %d: %+v", len(got), got)
	}
	if got[0].Note != "新理由" || strings.Join(got[0].Use, ",") != "君" {
		t.Fatalf("同键应取新值: %+v", got[0])
	}
}

// 深拷贝：上游候选被后续修改不能串味到快照里。
func TestMergeTermCorrectionsDeepCopiesUse(t *testing.T) {
	src := []TermCorrection{{Banned: "相公", Use: []string{"郎君", "先生"}}}
	got := mergeTermCorrections(nil, src)
	src[0].Use[0] = "被改坏"
	if got[0].Use[0] != "郎君" {
		t.Fatalf("Use 未深拷贝，源被污染: %+v", got[0])
	}
}

func TestSanitizeTermCorrections(t *testing.T) {
	got := sanitizeTermCorrections([]TermCorrection{
		{Banned: "  相公 ", Use: []string{" 郎君 ", "", "  "}, Note: "  理由  "},
		{Banned: "相公", Use: []string{"君"}},
		{Banned: "   "},
	})
	if len(got) != 1 {
		t.Fatalf("去重去空后期望 1 条，实际 %d: %+v", len(got), got)
	}
	if got[0].Banned != "相公" || got[0].Note != "理由" {
		t.Fatalf("未去空白: %+v", got[0])
	}
	if len(got[0].Use) != 1 || got[0].Use[0] != "郎君" {
		t.Fatalf("Use 未去空白去空: %+v", got[0].Use)
	}
}

// 快照合并走按键叠加：后到的候选不能把先前的对照表清空。
func TestOverlaySnapshotKeepsPriorTermCorrections(t *testing.T) {
	base := Snapshot{Structured: Structured{TermCorrections: []TermCorrection{
		{Banned: "沈相公", Use: []string{"郎君"}},
	}}}
	cand := Candidate{Structured: Structured{TermCorrections: []TermCorrection{
		{Banned: "相公", Use: []string{"先生"}},
	}}}
	got := OverlaySnapshot(base, cand)
	if len(got.Structured.TermCorrections) != 2 {
		t.Fatalf("运行中追加规则不应抹掉既有条目: %+v", got.Structured.TermCorrections)
	}
}
