package userrules

import (
	"strings"
	"testing"

	"github.com/voocel/ainovel-cli/internal/rules"
)

func toCand(t *testing.T, o normalizerOutput) rules.Candidate {
	t.Helper()
	c, err := o.toCandidate("test")
	if err != nil {
		t.Fatalf("toCandidate: %v", err)
	}
	return c
}

// 「别用相公，改用郎君」这类明确给了替代写法的，应进 term_corrections 而非裸禁用。
func TestToCandidateTermCorrection(t *testing.T) {
	c := toCand(t, normalizerOutput{
		Structured: normalizerStructured{
			TermCorrections: []termEntry{
				{Banned: "  沈相公 ", Use: []string{" 郎君 ", "", "先生"}, Note: "  明清才通行  "},
			},
		},
	})
	tcs := c.Structured.TermCorrections
	if len(tcs) != 1 {
		t.Fatalf("应提取 1 条对照，实际 %d", len(tcs))
	}
	if tcs[0].Banned != "沈相公" || tcs[0].Note != "明清才通行" {
		t.Errorf("未去空白: %+v", tcs[0])
	}
	if strings.Join(tcs[0].Use, ",") != "郎君,先生" {
		t.Errorf("Use 未去空白去空: %v", tcs[0].Use)
	}
}

// 同一词若两侧都出现，以 term_corrections 为准——否则 checker 会为它报两条
// （一条裸 forbidden_phrases 无替代项、一条带替代项），并把"没有替代写法"
// 这条错误信息发给 writer。
func TestToCandidateTermCorrectionWinsOverForbiddenPhrase(t *testing.T) {
	c := toCand(t, normalizerOutput{
		Structured: normalizerStructured{
			ForbiddenPhrases: []string{"相公", "毫无", "相公"},
			TermCorrections:  []termEntry{{Banned: "相公", Use: []string{"郎君"}}},
		},
	})
	if len(c.Structured.ForbiddenPhrases) != 1 || c.Structured.ForbiddenPhrases[0] != "毫无" {
		t.Errorf("已被对照表覆盖的词应移出禁用短语: %v", c.Structured.ForbiddenPhrases)
	}
	if len(c.Structured.TermCorrections) != 1 {
		t.Errorf("对照表应保留: %+v", c.Structured.TermCorrections)
	}
}

func TestToCandidateRejectsEmptyBanned(t *testing.T) {
	_, err := normalizerOutput{
		Structured: normalizerStructured{TermCorrections: []termEntry{{Banned: "   "}}},
	}.toCandidate("test")
	if err == nil {
		t.Fatal("空 banned 应报错让模型修正")
	}
}

// 只禁用、用户没给替代写法时不得编造 use——那会让 writer 照着一个臆造的
// 替代词去改，而历史称谓猜错的概率很高。
func TestToCandidateKeepsBareBanWithoutInventedUse(t *testing.T) {
	c := toCand(t, normalizerOutput{
		Structured: normalizerStructured{
			ForbiddenPhrases: []string{"媳妇"},
		},
	})
	if len(c.Structured.TermCorrections) != 0 {
		t.Fatalf("无替代写法时不应产生对照项: %+v", c.Structured.TermCorrections)
	}
	if len(c.Structured.ForbiddenPhrases) != 1 || c.Structured.ForbiddenPhrases[0] != "媳妇" {
		t.Errorf("应保留为裸禁用: %v", c.Structured.ForbiddenPhrases)
	}
}

func TestToCandidateDedupsTermCorrections(t *testing.T) {
	c := toCand(t, normalizerOutput{
		Structured: normalizerStructured{
			TermCorrections: []termEntry{
				{Banned: "相公", Use: []string{"郎君"}},
				{Banned: "相公", Use: []string{"君"}},
			},
		},
	})
	if len(c.Structured.TermCorrections) != 1 {
		t.Fatalf("同词应去重: %+v", c.Structured.TermCorrections)
	}
	if c.Structured.TermCorrections[0].Use[0] != "郎君" {
		t.Errorf("同词保留首条: %+v", c.Structured.TermCorrections[0])
	}
}

// 归一化提示词必须写明这条边界，否则模型会自行补全它"知道"的替代写法。
func TestNormalizerPromptForbidsInventingAlternatives(t *testing.T) {
	for _, want := range []string{
		"不得自行补充你知道的其它写法",
		"不要臆造 use",
		"不要自行编造该朝代的正确称谓",
		"term_corrections",
	} {
		if !strings.Contains(normalizerSystemPrompt, want) {
			t.Errorf("归一化提示词应含 %q", want)
		}
	}
}
