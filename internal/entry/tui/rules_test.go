package tui

import (
	"strings"
	"testing"

	"github.com/voocel/ainovel-cli/internal/rules"
	"github.com/voocel/ainovel-cli/internal/userrules"
)

// 违规章号要压成 /rework 能直接粘贴的连续区间——扫描的产物就是返工的入口。
func TestFormatSweepRanges(t *testing.T) {
	cases := map[string][]int{
		"1-3 7 12-14 100": {1, 2, 3, 7, 12, 13, 14, 100},
		"1-3":             {1, 2, 3},
		"5":               {5},
		"1-2 4-5 7":       {1, 2, 4, 5, 7},
		"9":               {9},
	}
	for want, in := range cases {
		if got := formatSweepRanges(in); got != want {
			t.Errorf("输入 %v 期望 %q，实际 %q", in, want, got)
		}
	}
	// 乱序输入也要归并
	if got := formatSweepRanges([]int{5, 1, 2, 3}); got != "1-3 5" {
		t.Errorf("乱序应归并为 %q，实际 %q", "1-3 5", got)
	}
	if got := formatSweepRanges(nil); got != "" {
		t.Errorf("空输入应为空串，实际 %q", got)
	}
}

// 扫描结果必须给出可执行的下一步，否则用户拿到一堆事实却不知道怎么用。
func TestFormatSweepResultEndsWithActionableRework(t *testing.T) {
	r := userrules.SweepResult{
		Scanned: 112, RuleCount: 20, Occurrences: 9,
		Chapters: []userrules.ChapterViolation{
			{Chapter: 4, Title: "坞中初见", Violations: []rules.Violation{{
				Rule: "term_correction", Target: "沈相公", Actual: 3, Severity: rules.SeverityError,
				Suggestion: []string{"郎君", "先生"}, Note: "明清通行",
			}}},
			{Chapter: 5, Title: "算豆", Violations: []rules.Violation{{
				Rule: "term_correction", Target: "相公", Actual: 6, Severity: rules.SeverityError,
				Suggestion: []string{"郎君"},
			}}},
		},
	}
	got := formatSweepResult(r)
	for _, want := range []string{"扫描 112 章", "20 条机械规则", "2 章存在违规", "2 条违规记录", "共出现 9 次", "第 4 章《坞中初见》",
		"沈相公 ×3", "建议改为：郎君 / 先生", "第 5 章《算豆》", "/rework 4-5 --yes"} {
		if !strings.Contains(got, want) {
			t.Errorf("输出应含 %q，实际:\n%s", want, got)
		}
	}
	if n := strings.Count(got, "·"); n != 2 {
		t.Errorf("应 2 条违规明细，实际 %d 条:\n%s", n, got)
	}
}

func TestFormatSweepResultEmptyAndClean(t *testing.T) {
	if got := formatSweepResult(userrules.SweepResult{}); !strings.Contains(got, "尚无已写章节") {
		t.Errorf("无章时应给引导: %q", got)
	}
	clean := userrules.SweepResult{Scanned: 30, RuleCount: 18}
	if got := formatSweepResult(clean); !strings.Contains(got, "未发现违规") {
		t.Errorf("全清时应明确说明: %q", got)
	}
	// 有 error 级违规时事件级别要抬到 warn，否则在事件面板里看不出来
	clean.Chapters = []userrules.ChapterViolation{{Chapter: 1, Violations: []rules.Violation{
		{Severity: rules.SeverityWarning},
	}}}
	if got := levelForSweep(clean); got != "info" {
		t.Errorf("仅 warning 应为 info，实际 %q", got)
	}
	clean.Chapters[0].Violations = append(clean.Chapters[0].Violations,
		rules.Violation{Severity: rules.SeverityError})
	if got := levelForSweep(clean); got != "warn" {
		t.Errorf("含 error 应为 warn，实际 %q", got)
	}
}

// use 为空时不得显示"建议改为："——那是"禁用但用户没给替代"的情况，
// 空标签会让 writer 以为有替代项可用。
func TestFormatViolationOmitsEmptySuggestion(t *testing.T) {
	got := formatViolation(rules.Violation{
		Rule: "forbidden_phrases", Target: "毫无", Actual: 2, Severity: rules.SeverityError,
	})
	if strings.Contains(got, "建议改为") {
		t.Errorf("无替代项时不应出现该标签: %q", got)
	}
	if !strings.Contains(got, "毫无 ×2") {
		t.Errorf("应报出词与次数: %q", got)
	}
}

func TestFormatRulesList(t *testing.T) {
	if got := formatRulesList(nil); !strings.Contains(got, "尚未初始化") {
		t.Errorf("nil 应有兜底文案: %q", got)
	}
	snap := &rules.Snapshot{
		Version: rules.SnapshotVersion, Status: rules.StatusReady,
		Sources: []string{"system_defaults", "runtime_update"},
		Structured: rules.Structured{
			Genre: "东汉末年题材",
			TermCorrections: []rules.TermCorrection{
				{Banned: "沈相公", Use: []string{"郎君", "先生"}, Note: "明清通行"},
				{Banned: "媳妇"},
			},
			FatigueWords: map[string]int{"一丝": 2, "仿佛": 2},
		},
		Uncertain: []string{"对话占比提高：无阈值"},
	}
	got := formatRulesList(snap)
	for _, want := range []string{"v3", "ready", "system_defaults、runtime_update", "东汉末年题材",
		"术语对照 2 条", "沈相公 → 郎君 / 先生", "（明清通行）", "媳妇", "疲劳词 2 个", "一丝×2",
		"未提升为机械规则的项", "无阈值", "/rules check"} {
		if !strings.Contains(got, want) {
			t.Errorf("应含 %q，实际:\n%s", want, got)
		}
	}
	// 疲劳词按字典序（字节序，一=U+4E00 < 仿=U+4EFF），避免 map 遍历顺序抖动
	if strings.Index(got, "一丝×2") > strings.Index(got, "仿佛×2") {
		t.Errorf("疲劳词应字典序:\n%s", got)
	}
}
