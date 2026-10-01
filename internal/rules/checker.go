package rules

import (
	"strings"
)

// Check 对章节正文按结构化规则进行机械检查，返回违规事实列表。
//
// 设计契约：
//   - 仅返事实，不下指令（铁律一）
//   - 不阻断任何调用方流程
//   - severity 按规则类型固定映射（参见 types.go 注释表）
//
// 参数：
//   - text：章节正文（终稿或草稿都可）
//   - s：合并后的结构化规则；IsEmpty 时直接返回 nil。
func Check(text string, s Structured) []Violation {
	if s.IsEmpty() {
		return nil
	}

	var violations []Violation
	violations = appendForbiddenChars(violations, text, s.ForbiddenChars)
	violations = appendForbiddenPhrases(violations, text, s)
	violations = appendFatigueWords(violations, text, s.FatigueWords)
	return violations
}

// forbidden_chars：出现 ≥1 次即 error。
// 同一条规则只产生一条 violation，actual 是出现次数。
func appendForbiddenChars(vs []Violation, text string, list []string) []Violation {
	for _, ch := range list {
		if ch == "" {
			continue
		}
		n := strings.Count(text, ch)
		if n == 0 {
			continue
		}
		vs = append(vs, Violation{
			Rule:     "forbidden_chars",
			Target:   ch,
			Actual:   n,
			Severity: SeverityError,
		})
	}
	return vs
}

// forbidden_phrases：出现 ≥1 次即 error；行为与 forbidden_chars 一致，仅 rule 名区分。
//
// 命中 forbidden_phrases 时顺带查 term_corrections：若该短语有对照项，则 rule 名
// 升级为 term_correction 并附上建议替代项与理由。同一份规则无论写在哪张表里，
// 检测行为一致，差别只在是否携带「那该写什么」。
func appendForbiddenPhrases(vs []Violation, text string, s Structured) []Violation {
	for _, ph := range s.BannedTerms() {
		n := strings.Count(text, ph)
		if n == 0 {
			continue
		}
		v := Violation{
			Rule:     "forbidden_phrases",
			Target:   ph,
			Actual:   n,
			Severity: SeverityError,
		}
		if tc, ok := s.CorrectionFor(ph); ok {
			v.Rule = "term_correction"
			v.Suggestion = append([]string(nil), tc.Use...)
			v.Note = tc.Note
		}
		vs = append(vs, v)
	}
	return vs
}

// fatigue_words：本章出现次数超过阈值才违规，warning 级。
// 不跨章累计——跨章问题后续交诊断。
func appendFatigueWords(vs []Violation, text string, m map[string]int) []Violation {
	for word, limit := range m {
		if word == "" || limit <= 0 {
			continue
		}
		n := strings.Count(text, word)
		if n <= limit {
			continue
		}
		vs = append(vs, Violation{
			Rule:     "fatigue_words",
			Target:   word,
			Limit:    limit,
			Actual:   n,
			Severity: SeverityWarning,
		})
	}
	return vs
}
