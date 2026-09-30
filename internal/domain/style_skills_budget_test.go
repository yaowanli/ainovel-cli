package domain

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"testing"
)

// 条数上限同时写在两处，且真正生效的是提示词那处：落盘是全量
// （MarshalStyleSkills），CompactStyleSkills 只作用于注入点。所以只改
// 常量不改提示词就是白改——实跑已因此出现过"10 个字段全被限在 5 条"。
// 两处必须相等，此测试让漂移立刻变红。
func TestStyleSkillItemLimitMatchesMergePrompt(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "assets", "prompts", "style-skill-merge.md"))
	if err != nil {
		t.Fatalf("read merge prompt: %v", err)
	}
	re := regexp.MustCompile(`每个字段最多\s*(\d+)\s*条`)
	m := re.FindSubmatch(raw)
	if m == nil {
		t.Fatal("merge prompt must state the per-field item limit")
	}
	got, err := strconv.Atoi(string(m[1]))
	if err != nil {
		t.Fatalf("parse limit: %v", err)
	}
	if got != maxStyleSkillItems {
		t.Fatalf("per-field limit drift: prompt says %d, maxStyleSkillItems = %d; "+
			"the prompt is what actually caps output, so this is a no-op refactor", got, maxStyleSkillItems)
	}
}

// compact 上限之外还要守住单条长度：超长条目直接丢弃而非硬截断，截断会把
// 半句话塞进 skill，模型会当完整规则执行。
func TestCompactStyleSkillsDropsOverlongInsteadOfTruncating(t *testing.T) {
	item := func(s string) string { return s }
	over := longItem(maxStyleSkillItemRunes + 1)
	exact := longItem(maxStyleSkillItemRunes)
	skills := &StyleSkills{
		Version: StyleSkillsVersion,
		Prose: ProseSkill{
			NarrativeVoice: []string{item(over), item(exact)},
		},
	}
	got := CompactStyleSkills(skills)
	if len(got.Prose.NarrativeVoice) != 1 {
		t.Fatalf("overlong item must be dropped, got %d items", len(got.Prose.NarrativeVoice))
	}
	if got.Prose.NarrativeVoice[0] != exact {
		t.Fatal("the item at the exact limit must survive intact")
	}
	// 输入不得被就地改写：compact 每次注入都会调用，就地截断会逐次腐蚀磁盘内容
	if len(skills.Prose.NarrativeVoice) != 2 {
		t.Fatal("CompactStyleSkills must not mutate its input")
	}
}

// 放宽到 8 条后，注入预算仍需有界：条数 × 长度是每章都要付的上下文成本。
// 这个测试不校验具体数字，只守住"不会悄悄膨胀到读不动"。
func TestCompactStyleSkillsCapsTotalInjectedVolume(t *testing.T) {
	skills := &StyleSkills{Version: StyleSkillsVersion}
	// 每字段塞远超上限的条目，模拟上游产出过量
	for i := 0; i < 40; i++ {
		e := "条目" + strconv.Itoa(i)
		skills.Prose.NarrativeVoice = append(skills.Prose.NarrativeVoice, e)
		skills.Prose.SentenceRhythm = append(skills.Prose.SentenceRhythm, e)
		skills.Dialogue.SpeechTags = append(skills.Dialogue.SpeechTags, e)
	}
	got := CompactStyleSkills(skills)
	total := 0
	for _, field := range [][]string{
		got.Prose.NarrativeVoice, got.Prose.SentenceRhythm,
		got.Dialogue.SpeechTags,
	} {
		if len(field) > maxStyleSkillItems {
			t.Fatalf("field kept %d items, over cap %d", len(field), maxStyleSkillItems)
		}
		total += len(field)
	}
	if total > 3*maxStyleSkillItems {
		t.Fatalf("injected items %d exceed budget", total)
	}
}
