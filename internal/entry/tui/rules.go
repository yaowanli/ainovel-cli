// /rules —— 用户规则的查看与全稿体检。
//
// 存在的理由：规则检查原本只在 commit（新正文）与 novel_context（当前章）两处发生，
// 覆盖不到已落盘的存量章节。用户中途加一条规则只能保护未来，改不掉过去——
// 要知道哪些章已经违规，此前只能手工 grep。
//
// 本命令只产出事实，不触发改写：违规清单交给用户判断，再由 /rework 走
// editor 裁定与 PendingRewrites 串行改写，保持"工具只返事实"铁律。
package tui

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/voocel/ainovel-cli/internal/host"
	"github.com/voocel/ainovel-cli/internal/rules"
	"github.com/voocel/ainovel-cli/internal/userrules"
)

const rulesUsage = `用法：
  /rules list          查看当前生效的用户规则（结构化字段 + 疲劳词）
  /rules check         用当前规则回扫全书已写章节，列出违规章与词

check 只报告不改写。确认要修之后用 /rework <章号> 让 Editor 逐章裁定。`

// runRules 实现 /rules 的分发。返回是否已消费本次命令。
func runRules(m Model, args []string) (Model, bool) {
	if len(args) == 0 {
		m.applyEvent(host.Event{Time: time.Now(), Category: "SYSTEM", Level: "info", Summary: rulesUsage})
		m.refreshEventViewport()
		return m, true
	}
	switch args[0] {
	case "help", "-h", "--help":
		m.applyEvent(host.Event{Time: time.Now(), Category: "SYSTEM", Level: "info", Summary: rulesUsage})
		m.refreshEventViewport()
		return m, true
	case "list", "ls":
		snap, err := m.runtime.UserRulesSnapshot()
		if err != nil {
			m.applyEvent(host.Event{Time: time.Now(), Category: "ERROR", Level: "error", Summary: "读取规则失败：" + err.Error()})
			m.refreshEventViewport()
			return m, true
		}
		m.applyEvent(host.Event{Time: time.Now(), Category: "SYSTEM", Level: "info", Summary: formatRulesList(snap)})
		m.refreshEventViewport()
		return m, true
	case "check", "scan":
		return runRulesCheck(m), true
	}
	m.applyEvent(host.Event{
		Time: time.Now(), Category: "ERROR", Level: "error",
		Summary: fmt.Sprintf("未知子命令：%s\n%s", args[0], rulesUsage),
	})
	m.refreshEventViewport()
	return m, true
}

func runRulesCheck(m Model) Model {
	result, err := m.runtime.SweepRules()
	if err != nil {
		m.applyEvent(host.Event{Time: time.Now(), Category: "ERROR", Level: "error", Summary: "全稿扫描失败：" + err.Error()})
		m.refreshEventViewport()
		return m
	}
	m.applyEvent(host.Event{
		Time: time.Now(), Category: "SYSTEM", Level: levelForSweep(result),
		Summary: formatSweepResult(result),
	})
	m.refreshEventViewport()
	return m
}

// levelForSweep 让有 error 级违规的扫描结果在事件面板里显眼一些。
func levelForSweep(r userrules.SweepResult) string {
	for _, c := range r.Chapters {
		for _, v := range c.Violations {
			if v.Severity == rules.SeverityError {
				return "warn"
			}
		}
	}
	return "info"
}

// formatSweepResult 渲染 /rules check。
func formatSweepResult(r userrules.SweepResult) string {
	if r.Scanned == 0 {
		return "全书尚无已写章节，无从扫描。先写几章再体检。"
	}
	if len(r.Chapters) == 0 {
		return fmt.Sprintf("扫描 %d 章，未发现违规。当前 %d 条机械规则全部通过。", r.Scanned, r.RuleCount)
	}

	var b strings.Builder
	fmt.Fprintf(&b, "扫描 %d 章（%d 条机械规则），%d 章存在违规：%d 条违规记录，正文中共出现 %d 次。\n",
		r.Scanned, r.RuleCount, len(r.Chapters), r.Total(), r.Occurrences)
	for _, c := range r.Chapters {
		title := c.Title
		if title == "" {
			title = "（无标题）"
		}
		fmt.Fprintf(&b, "\n第 %d 章《%s》\n", c.Chapter, title)
		for _, v := range c.Violations {
			fmt.Fprintf(&b, "  · %s\n", formatViolation(v))
		}
	}
	b.WriteString("\n要修这些章：/rework ")
	b.WriteString(formatSweepRanges(r.AffectedChapters()))
	b.WriteString(" --yes")
	return b.String()
}

// formatViolation 单条违规。替代项一并列出——writer 删掉禁词后该写什么，
// 不该由它自行推断。
func formatViolation(v rules.Violation) string {
	s := fmt.Sprintf("%s ×%v（%s）", v.Target, v.Actual, v.Rule)
	if len(v.Suggestion) > 0 {
		s += " → 建议改为：" + strings.Join(v.Suggestion, " / ")
	}
	if v.Note != "" {
		s += "（" + v.Note + "）"
	}
	return s
}

// formatSweepRanges 把违规章号压成最小连续区间，供 /rework 直接粘贴。
func formatSweepRanges(chapters []int) string {
	if len(chapters) == 0 {
		return ""
	}
	nums := append([]int(nil), chapters...)
	sort.Ints(nums)
	var parts []string
	start, prev := nums[0], nums[0]
	flush := func() {
		if start == prev {
			parts = append(parts, fmt.Sprintf("%d", start))
		} else {
			parts = append(parts, fmt.Sprintf("%d-%d", start, prev))
		}
	}
	for _, n := range nums[1:] {
		if n == prev+1 {
			prev = n
			continue
		}
		flush()
		start, prev = n, n
	}
	flush()
	return strings.Join(parts, " ")
}

// formatRulesList 渲染 /rules list。
func formatRulesList(snap *rules.Snapshot) string {
	if snap == nil {
		return "规则快照尚未初始化。"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "规则快照 v%d　状态 %s", snap.Version, snap.Status)
	if len(snap.Sources) == 0 {
		b.WriteString("\n来源：（无）")
	} else {
		b.WriteString("\n来源：" + strings.Join(snap.Sources, "、"))
	}
	if g := snap.Structured.Genre; g != "" {
		fmt.Fprintf(&b, "\n题材：%s", g)
	}
	if len(snap.Structured.ForbiddenChars) > 0 {
		fmt.Fprintf(&b, "\n禁用字符：%s", strings.Join(snap.Structured.ForbiddenChars, " "))
	}
	if p := snap.Structured.ForbiddenPhrases; len(p) > 0 {
		fmt.Fprintf(&b, "\n禁用短语：%s", strings.Join(p, "、"))
	}
	if tcs := snap.Structured.TermCorrections; len(tcs) > 0 {
		fmt.Fprintf(&b, "\n术语对照 %d 条：", len(tcs))
		for _, tc := range tcs {
			line := "\n  · " + tc.Banned
			if len(tc.Use) > 0 {
				line += " → " + strings.Join(tc.Use, " / ")
			}
			if tc.Note != "" {
				line += "（" + tc.Note + "）"
			}
			b.WriteString(line)
		}
	}
	if fw := snap.Structured.FatigueWords; len(fw) > 0 {
		words := make([]string, 0, len(fw))
		for w, n := range fw {
			words = append(words, fmt.Sprintf("%s×%d", w, n))
		}
		sort.Strings(words)
		fmt.Fprintf(&b, "\n疲劳词 %d 个：%s", len(fw), strings.Join(words, "、"))
	}
	if strings.TrimSpace(snap.Preferences) != "" {
		fmt.Fprintf(&b, "\n\n自然语言偏好：\n%s", snap.Preferences)
	}
	if len(snap.Uncertain) > 0 {
		fmt.Fprintf(&b, "\n\n未提升为机械规则的项：")
		for _, u := range snap.Uncertain {
			b.WriteString("\n  · " + u)
		}
	}
	b.WriteString("\n\n回扫已写章节：/rules check")
	return b.String()
}
