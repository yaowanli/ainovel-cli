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

	tea "github.com/charmbracelet/bubbletea"

	"github.com/voocel/ainovel-cli/internal/host"
	"github.com/voocel/ainovel-cli/internal/rules"
	storepkg "github.com/voocel/ainovel-cli/internal/store"
	"github.com/voocel/ainovel-cli/internal/userrules"
)

const rulesUsage = `用法：
  /rules list                      查看当前生效的用户规则（结构化字段 + 疲劳词）
  /rules check                     用当前规则回扫全书已写章节，列出违规章与词
  /rules era <朝代>                从内置静态对照表生成候选（不调用 LLM，推荐）
  /rules propose <朝代>            让模型现场生成候选（慢且不稳，优先用 era）
  /rules proposals                 查看待裁决候选
  /rules adopt <词> [<词>...]      采纳候选，写入生效规则
  /rules reject <词> [<词>...]     否决候选

check 只报告不改写。确认要修之后用 /rework <章号> 让 Editor 逐章裁定。

候选与生效规则分离：propose 产出的候选需逐条 adopt 才会生效，因为命中即
强制改写，一条错误的历史称谓代价比漏查更高。`

// runRules 实现 /rules 的分发。返回是否已消费本次命令。
func runRules(m Model, args []string) (Model, tea.Cmd, bool) {
	if len(args) == 0 {
		m.applyEvent(host.Event{Time: time.Now(), Category: "SYSTEM", Level: "info", Summary: rulesUsage})
		m.refreshEventViewport()
		return m, nil, true
	}
	switch args[0] {
	case "help", "-h", "--help":
		m.applyEvent(host.Event{Time: time.Now(), Category: "SYSTEM", Level: "info", Summary: rulesUsage})
		m.refreshEventViewport()
		return m, nil, true
	case "list", "ls":
		snap, err := m.runtime.UserRulesSnapshot()
		if err != nil {
			m.applyEvent(host.Event{Time: time.Now(), Category: "ERROR", Level: "error", Summary: "读取规则失败：" + err.Error()})
			m.refreshEventViewport()
			return m, nil, true
		}
		m.applyEvent(host.Event{Time: time.Now(), Category: "SYSTEM", Level: "info", Summary: formatRulesList(snap)})
		m.refreshEventViewport()
		return m, nil, true
	case "check", "scan":
		next := runRulesCheck(m)
		return next, nil, true
	case "era":
		return runRulesEra(m, args[1:])
	case "propose":
		return runRulesPropose(m, args[1:])
	case "proposals":
		next := runRulesProposals(m)
		return next, nil, true
	case "adopt":
		next := runRulesAdopt(m, args[1:])
		return next, nil, true
	case "reject":
		next := runRulesReject(m, args[1:])
		return next, nil, true
	}
	m.applyEvent(host.Event{
		Time: time.Now(), Category: "ERROR", Level: "error",
		Summary: fmt.Sprintf("未知子命令：%s\n%s", args[0], rulesUsage),
	})
	m.refreshEventViewport()
	return m, nil, true
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

// runRulesEra 从内置静态对照表生成候选。零 LLM 调用、瞬时完成、无失败风险——
// 这是首选路径；/rules propose 走模型，慢且可能因思考预算耗尽而失败。
func runRulesEra(m Model, args []string) (Model, tea.Cmd, bool) {
	if len(args) == 0 {
		m.applyEvent(host.Event{
			Time: time.Now(), Category: "ERROR", Level: "error",
			Summary: "用法：/rules era <朝代>，例如 /rules era 东汉末年\n" +
				"内置对照表覆盖先秦至清，标注哪一节的条目该采纳由你决定。\n" +
				"本书专用词条写在 output/novel/style/era-terminology.md，维护指南见 docs/era-terminology.md。",
		})
		m.refreshEventViewport()
		return m, nil, true
	}
	era := strings.Join(args, " ")
	doc, err := m.runtime.LoadEraProposalsFromTable(era)
	if err != nil {
		m.applyEvent(host.Event{Time: time.Now(), Category: "ERROR", Level: "error", Summary: err.Error()})
		m.refreshEventViewport()
		return m, nil, true
	}
	m.applyEvent(host.Event{
		Time: time.Now(), Category: "SYSTEM", Level: "info",
		Summary: fmt.Sprintf(
			"已从内置静态对照表生成 %d 条候选（%s）。未调用模型，瞬时完成。\n"+
				"逐条审阅后采纳：/rules adopt <词>\n查看：/rules proposals\n"+
				"提示：表里含各朝代条目，只采纳属于本书朝代的那部分——采纳后命中会被强制改写正文。",
			len(doc.Proposals), doc.Era),
	})
	m.refreshEventViewport()
	return m, nil, true
}

// runRulesPropose 生成时代术语候选表。朝代不给时只作建议、不猜——猜错朝代会
// 产出一整批无关候选，而用户看不出那些是猜错的。
func runRulesPropose(m Model, args []string) (Model, tea.Cmd, bool) {
	if len(args) == 0 {
		snap, err := m.runtime.UserRulesSnapshot()
		if err != nil {
			m.applyEvent(host.Event{Time: time.Now(), Category: "ERROR", Level: "error", Summary: "读取题材失败：" + err.Error()})
			m.refreshEventViewport()
			return m, nil, false
		}
		if guess := userrules.SuggestEra(snap); guess != "" {
			m.applyEvent(host.Event{
				Time: time.Now(), Category: "SYSTEM", Level: "info",
				Summary: "需要指定朝代。当前题材字段是「" + guess + "」。\n" +
					"若这就是本书的时代，执行：/rules propose " + guess + "\n" +
					"若不是（如「历史」这类宽泛题材），请显式给出，例如 /rules propose 东汉末年。\n" +
					"系统不会替你猜朝代——猜错会产出一整批无关候选。",
			})
			m.refreshEventViewport()
			return m, nil, false
		}
		m.applyEvent(host.Event{
			Time: time.Now(), Category: "ERROR", Level: "error",
			Summary: "需要指定朝代：/rules propose <朝代>，例如 /rules propose 东汉末年",
		})
		m.refreshEventViewport()
		return m, nil, false
	}

	era := strings.Join(args, " ")
	m.simSeq++
	state, cmd, err := startEraPropose(m.runtime, m.simSeq, era, m.width, m.height)
	if err != nil {
		m.applyEvent(host.Event{Time: time.Now(), Category: "ERROR", Level: "error", Summary: "启动候选生成失败：" + err.Error()})
		m.refreshEventViewport()
		return m, nil, false
	}
	m.simulator = state
	m.textarea.Blur()
	return m, cmd, true
}

func runRulesProposals(m Model) Model {
	pending, err := m.runtime.PendingEraProposals()
	if err != nil {
		m.applyEvent(host.Event{Time: time.Now(), Category: "ERROR", Level: "error", Summary: "读取候选失败：" + err.Error()})
		m.refreshEventViewport()
		return m
	}
	if len(pending) == 0 {
		m.applyEvent(host.Event{
			Time: time.Now(), Category: "SYSTEM", Level: "info",
			Summary: "没有待裁决候选。生成新的：/rules propose <朝代>",
		})
		m.refreshEventViewport()
		return m
	}
	m.applyEvent(host.Event{Time: time.Now(), Category: "SYSTEM", Level: "info", Summary: formatEraProposals(pending)})
	m.refreshEventViewport()
	return m
}

func runRulesAdopt(m Model, args []string) Model {
	if len(args) == 0 {
		m.applyEvent(host.Event{
			Time: time.Now(), Category: "ERROR", Level: "error",
			Summary: "用法：/rules adopt <词> [<词>...]\n" + formatEraProposalsShort(m),
		})
		m.refreshEventViewport()
		return m
	}
	var done, failed []string
	for _, w := range args {
		tc, err := m.runtime.AdoptEraProposal(w)
		if err != nil {
			failed = append(failed, fmt.Sprintf("%s（%s）", w, err.Error()))
			continue
		}
		line := fmt.Sprintf("%s → %s", tc.Banned, strings.Join(tc.Use, " / "))
		if tc.Note != "" {
			line += "（" + tc.Note + "）"
		}
		done = append(done, line)
	}
	report := "已采纳并生效：" + strings.Join(done, "；")
	if len(failed) > 0 {
		report += "\n未采纳：" + strings.Join(failed, "；")
	}
	m.applyEvent(host.Event{
		Time: time.Now(), Category: "SYSTEM", Level: levelBySuccess(len(done)),
		Summary: report + "\n已写章节不会被自动改动；用 /rules check 查存量，用 /rework 逐章返工。",
	})
	m.refreshEventViewport()
	return m
}

func runRulesReject(m Model, args []string) Model {
	if len(args) == 0 {
		m.applyEvent(host.Event{
			Time: time.Now(), Category: "ERROR", Level: "error",
			Summary: "用法：/rules reject <词> [<词>...]",
		})
		m.refreshEventViewport()
		return m
	}
	var failed []string
	for _, w := range args {
		if err := m.runtime.RejectEraProposal(w); err != nil {
			failed = append(failed, fmt.Sprintf("%s（%s）", w, err.Error()))
		}
	}
	msg := fmt.Sprintf("已否决 %d 条。", len(args)-len(failed))
	if len(failed) > 0 {
		msg += "\n未否决：" + strings.Join(failed, "；")
	}
	m.applyEvent(host.Event{Time: time.Now(), Category: "SYSTEM", Level: "info", Summary: msg})
	m.refreshEventViewport()
	return m
}

func levelBySuccess(n int) string {
	if n == 0 {
		return "error"
	}
	return "info"
}

func formatEraProposals(pending []storepkg.EraProposal) string {
	var b strings.Builder
	fmt.Fprintf(&b, "待裁决候选 %d 条（low 把握在前，这些最需要你亲自核实）：\n", len(pending))
	for i, p := range pending {
		mark := "high"
		if p.Confidence == "low" {
			mark = "low "
		}
		fmt.Fprintf(&b, "%2d. [%s] %s → %s", i+1, mark, p.Banned, strings.Join(p.Use, " / "))
		if p.Note != "" {
			fmt.Fprintf(&b, "（%s）", p.Note)
		}
		b.WriteString("\n")
	}
	b.WriteString("\n采纳：/rules adopt <词>　否决：/rules reject <词>")
	return b.String()
}

func formatEraProposalsShort(m Model) string {
	pending, err := m.runtime.PendingEraProposals()
	if err != nil || len(pending) == 0 {
		return ""
	}
	words := make([]string, 0, len(pending))
	for _, p := range pending {
		words = append(words, p.Banned)
	}
	return "待裁决：" + strings.Join(words, "、")
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
