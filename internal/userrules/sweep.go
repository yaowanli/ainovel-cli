// Package userrules 的全稿扫描。
//
// 为什么需要它：规则检查原本只在两个点上发生——commit_chapter 查新提交的正文，
// novel_context 查"当前正在处理的那一章"。两者都覆盖不到已落盘的存量章节，所以
// 用户中途加一条规则（如"东汉不许写相公"）只能保护未来，改不掉过去。
//
// 本文件补上第三个点：按当前快照回扫全部已完成章节，回答"哪些已写章节违反了
// 现在生效的规则"。扫描只产出事实清单，不触发任何改写——改写仍由 editor 裁定、
// PendingRewrites 串行执行，保持铁律一。
package userrules

import (
	"sort"

	"github.com/voocel/ainovel-cli/internal/rules"
	"github.com/voocel/ainovel-cli/internal/store"
)

// ChapterViolation 是单章的违规事实集合。
type ChapterViolation struct {
	Chapter    int
	Title      string
	Violations []rules.Violation
}

// SweepResult 是一次全稿扫描的结果。
type SweepResult struct {
	Status    string
	Chapters  []ChapterViolation // 仅含存在违规的章，按章号升序
	Scanned   int                // 实际扫描章数
	RuleCount int                // 参与检测的机械规则条数

	// Occurrences 是违规词在正文里的实际出现总次数。
	//
	// 刻意与 Total 分开：Total 数的是「违规条目」（每章每词各一条），
	// Occurrences 数的是词的出现次数。两者差距很大——某词在 41 章里各出现
	// 4 次，是 41 条违规但 166 次出现。合成一个数会让人严重低估工作量。
	Occurrences int
}

// Total 返回违规条目数（每章每词计一条），不是词出现次数——见 Occurrences。
func (r SweepResult) Total() int {
	n := 0
	for _, c := range r.Chapters {
		n += len(c.Violations)
	}
	return n
}

// AffectedChapters 返回有违规的章号升序列表，供 /rework 直接取用。
func (r SweepResult) AffectedChapters() []int {
	out := make([]int, 0, len(r.Chapters))
	for _, c := range r.Chapters {
		out = append(out, c.Chapter)
	}
	return out
}

// RuleCount 是当前快照中参与机械检测的规则条数（禁用词 + 疲劳词）。
func ruleCount(s rules.Structured) int {
	return len(s.BannedTerms()) + len(s.FatigueWords)
}

// Sweep 用当前规则快照回扫全书已完成章节。
//
// 同时跑 Lint（markdown 残留、拉丁字母碎片）与 Check（用户结构化规则）：前者与用户
// 规则无关，是产品底线，同样值得回扫一次——写完 100 章才发现正文里有 ** 加粗，
// 导出 txt 会裸露符号，而 commit 期的提示早已滚过去了。
//
// rules 为 nil 时回落到 store 里的快照；快照缺失时按 system_defaults 扫描，
// 保证"快照还没初始化"不会让用户得到一个虚假的全清结果。
func Sweep(st *store.Store, rulesSnap *rules.Snapshot) (SweepResult, error) {
	var result SweepResult

	snap := rulesSnap
	if snap == nil {
		cur, err := st.UserRules.Load()
		if err != nil {
			return result, err
		}
		if cur == nil {
			def := rules.BuildSnapshot([]rules.Candidate{rules.SystemDefaults()})
			cur = &def
		}
		snap = cur
	}
	result.Status = string(snap.Status)
	result.RuleCount = ruleCount(snap.Structured)

	progress, err := st.Progress.Load()
	if err != nil {
		return result, err
	}
	if progress == nil || len(progress.CompletedChapters) == 0 {
		return result, nil
	}

	chapters := append([]int(nil), progress.CompletedChapters...)
	sort.Ints(chapters)

	// 逐章单独取，不走 LoadCompleted：单章记录损坏时该章记为扫描失败并继续，
	// 不让一章坏数据掩盖全书其余章节的结论。
	for _, ch := range chapters {
		record, err := st.ChapterRecords.Load(ch)
		if err != nil || record == nil {
			continue
		}
		result.Scanned++
		vs := rules.Lint(record.Content)
		vs = append(vs, rules.Check(record.Content, snap.Structured)...)
		if len(vs) == 0 {
			continue
		}
		result.Chapters = append(result.Chapters, ChapterViolation{
			Chapter:    ch,
			Title:      record.Facts.Title,
			Violations: vs,
		})
	}
	for _, c := range result.Chapters {
		for _, v := range c.Violations {
			if n, ok := v.Actual.(int); ok {
				result.Occurrences += n
			}
		}
	}
	return result, nil
}
