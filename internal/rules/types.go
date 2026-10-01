// Package rules 实现用户偏好的输入层（Policy）：把各来源的写作规则归一化、合并成
// 本书快照（见 snapshot.go），运行时由 novel_context 注入、commit_chapter 机械检查。
//
// Rule 是第四类事实，跟 Progress / Checkpoint / Artifact 并列，但性质相反：
// 前三类是系统输出，Rule 是用户意图的持久化输入。
//
// 设计约束（不可妥协）：
//   - 工具只返事实，不返指令（Violation 是事实，由 editor 决定是否触发重写）
//   - 不引入新的 verdict 路径（复用 PendingRewrites）
//   - 不引入严格度字段（severity 由规则类型固定映射，editor 自主语义裁定）
//   - 不动 Flow Router（rule 不参与路由）
package rules

import "strings"

// SourceKind 标记规则文件来源，仅用于生成来源标签（如 global:my-style.md）。
type SourceKind int

const (
	// SourceGlobal — 用户全局偏好（~/.ainovel/rules/ 目录下所有 .md，按文件名字典序合并），跨书复用。
	SourceGlobal SourceKind = iota
	// SourceProject — 本书规则（./.ainovel/rules/ 目录下所有 .md，按文件名字典序合并），优先级最高。
	SourceProject
)

// String 返回来源的可读名称，用于来源标签前缀。
func (k SourceKind) String() string {
	switch k {
	case SourceGlobal:
		return "global"
	case SourceProject:
		return "project"
	default:
		return "unknown"
	}
}

// TermCorrection 是「禁用词 → 该用什么」的对照项。
//
// 为什么需要它：forbidden_phrases 只能表达「不许写 X」，表达不了「那该写 Y」。
// 后果是 writer 删掉禁词后只能猜替代写法，而术语类错误（尤其历史题材的时代称谓）
// 猜错的概率很高——删掉「沈相公」不等于就知道该写「郎君/先生/茂才」。
// 本类型让「禁用」与「替代」在同一条规则里闭合，checker 命中时把 Use 一并
// 作为事实交给 editor/writer，不必由模型自行推断正确写法。
//
// 数据来源约束：只接受用户采纳的条目。自动生成的候选表（见提案层设计）必须先经
// 用户审阅才能进入本结构——本结构命中即 SeverityError，会被 editor.md 强制升级
// verdict，一条错误条目会被系统当作权威去改写正确正文，成本不对称。
type TermCorrection struct {
	Banned string   `json:"banned"`         // 禁用的词或短语（字面精确匹配）
	Use    []string `json:"use,omitempty"`  // 建议替代，按优先级排列
	Note   string   `json:"note,omitempty"` // 理由（时代/语境/禁忌来源），供 editor 判断适用性
}

// BannedTerms 返回本表与 forbidden_phrases 的并集，供 checker 一次性检测。
// 去重且保持稳定顺序：forbidden_phrases 在前，term_corrections 补齐其余，
// 使同一份规则无论走哪条路径检测结果一致。
func (s Structured) BannedTerms() []string {
	seen := make(map[string]bool, len(s.ForbiddenPhrases)+len(s.TermCorrections))
	out := make([]string, 0, len(s.ForbiddenPhrases)+len(s.TermCorrections))
	for _, ph := range s.ForbiddenPhrases {
		if ph = strings.TrimSpace(ph); ph == "" || seen[ph] {
			continue
		}
		seen[ph] = true
		out = append(out, ph)
	}
	for _, tc := range s.TermCorrections {
		b := strings.TrimSpace(tc.Banned)
		if b == "" || seen[b] {
			continue
		}
		seen[b] = true
		out = append(out, b)
	}
	return out
}

// CorrectionFor 返回 banned 对应的对照项；无对照时返回 nil（裸禁用词合法）。
func (s Structured) CorrectionFor(banned string) (TermCorrection, bool) {
	for _, tc := range s.TermCorrections {
		if tc.Banned == banned {
			return tc, true
		}
	}
	return TermCorrection{}, false
}

// Structured 装载机械可检的结构化规则字段（归一化各来源后的候选/合并结果）。
// 章节字数刻意不在此列：多长算一章是叙事完整性问题，属语义裁量（writer/editor），
// 数字化成机械硬线会诱导模型为跨线注水——字数意愿走 preferences 自然语言通道。
type Structured struct {
	Genre            string           `json:"genre,omitempty"`
	ForbiddenChars   []string         `json:"forbidden_chars,omitempty"`
	ForbiddenPhrases []string         `json:"forbidden_phrases,omitempty"`
	TermCorrections  []TermCorrection `json:"term_corrections,omitempty"`
	FatigueWords     map[string]int   `json:"fatigue_words,omitempty"`
}

// IsEmpty 用于判定是否完全没有结构化规则；checker 可据此跳过。
func (s Structured) IsEmpty() bool {
	return s.Genre == "" &&
		len(s.ForbiddenChars) == 0 &&
		len(s.ForbiddenPhrases) == 0 &&
		len(s.TermCorrections) == 0 &&
		len(s.FatigueWords) == 0
}

// Severity 标记 Violation 的严重等级。
// 固定映射（用户不可配置）：
//
//	forbidden_chars 出现             -> Error
//	forbidden_phrases 出现           -> Error
//	fatigue_words 超阈值             -> Warning
type Severity string

const (
	SeverityWarning Severity = "warning"
	SeverityError   Severity = "error"
)

// Violation 是 checker 的输出：本章违反了某条机械规则的事实陈述。
//
// 注意：commit_chapter 把 violations 透传到返回 JSON，不阻断 commit；
// editor 在审阅时把这些事实映射到现有七维（aesthetic/pacing/character/consistency），
// 由 LLM 自主决定是否升级 verdict 触发 polish/rewrite。
type Violation struct {
	Rule       string   `json:"rule"`                 // forbidden_chars / forbidden_phrases / term_correction / fatigue_words
	Target     string   `json:"target,omitempty"`     // 具体违规对象（哪个词/字符）
	Limit      any      `json:"limit,omitempty"`      // 阈值；fatigue_words=int / forbidden_*=空
	Actual     any      `json:"actual"`               // 实际值：出现次数
	Severity   Severity `json:"severity"`             // error / warning
	Suggestion []string `json:"suggestion,omitempty"` // 建议替代项；仅 term_corrections 命中时非空
	Note       string   `json:"note,omitempty"`       // 规则理由；仅 term_corrections 命中时非空
}
