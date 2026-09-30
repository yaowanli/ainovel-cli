package domain

import (
	"strings"
	"testing"
)

func longItem(n int) string { return strings.Repeat("字", n) }

func TestValidateStyleSkillsRejectsNilAndBadVersion(t *testing.T) {
	if err := ValidateStyleSkills(nil); err == nil {
		t.Fatal("nil must fail")
	}
	if err := ValidateStyleSkills(&StyleSkills{Version: "other"}); err == nil {
		t.Fatal("bad version must fail")
	}
	if err := ValidateStyleSkills(&StyleSkills{Version: StyleSkillsVersion}); err != nil {
		t.Fatalf("empty skills must pass: %v", err)
	}
}

func TestValidateStyleSkillsBackfillsFingerprint(t *testing.T) {
	skills := &StyleSkills{
		Version: StyleSkillsVersion,
		Corpus: StyleSkillsCorpus{Sources: []StyleSkillsSource{
			{RelativePath: "a.txt", SHA256: "abc"},
			{RelativePath: "b.txt", SHA256: "def", Fingerprint: "keep-me"},
		}},
	}
	if err := ValidateStyleSkills(skills); err != nil {
		t.Fatal(err)
	}
	if got := skills.Corpus.Sources[0].Fingerprint; got != "a.txt:abc" {
		t.Fatalf("fingerprint = %q", got)
	}
	if got := skills.Corpus.Sources[1].Fingerprint; got != "keep-me" {
		t.Fatalf("existing fingerprint must be kept, got %q", got)
	}
}

func TestValidateStyleSkillsRequiresPathAndHash(t *testing.T) {
	skills := &StyleSkills{Version: StyleSkillsVersion, Corpus: StyleSkillsCorpus{
		Sources: []StyleSkillsSource{{RelativePath: "a.txt"}},
	}}
	if err := ValidateStyleSkills(skills); err == nil {
		t.Fatal("missing sha256 must fail")
	}
}

func TestCompactStyleSkillsCapsItemsAndDrops(t *testing.T) {
	items := []string{}
	for i := 0; i < 9; i++ {
		items = append(items, "规则"+string(rune('A'+i)))
	}
	items = append(items, longItem(51), "  ", "规则A")

	skills := &StyleSkills{
		Version: StyleSkillsVersion,
		Prose:   ProseSkill{NarrativeVoice: items},
		Dialogue: DialogueSkill{
			SpeechTags:     items,
			FormsOfAddress: []string{"称谓甲"},
			ModalParticles: nil,
			VerbalTics:     []string{"口头禅甲"},
			LineLength:     []string{"短促"},
			Dialect:        []string{"方言甲"},
		},
		Taboos: []string{"禁止照搬"},
	}
	out := CompactStyleSkills(skills)

	assertCap := func(name string, got []string) {
		if len(got) != maxStyleSkillItems {
			t.Fatalf("%s: len = %d, want %d (%v)", name, len(got), maxStyleSkillItems, got)
		}
	}
	assertCap("narrative_voice", out.Prose.NarrativeVoice)
	assertCap("speech_tags", out.Dialogue.SpeechTags)

	for _, item := range out.Prose.NarrativeVoice {
		if len([]rune(item)) > maxStyleSkillItemRunes {
			t.Fatalf("over-long item survived: %q", item)
		}
		if strings.TrimSpace(item) != item || item == "" {
			t.Fatalf("blank item survived: %q", item)
		}
	}

	if out.Dialogue.FormsOfAddress[0] != "称谓甲" || out.Dialogue.VerbalTics[0] != "口头禅甲" {
		t.Fatalf("single-item fields lost: %+v", out.Dialogue)
	}
	if out.Dialogue.ModalParticles != nil {
		t.Fatalf("empty field must stay nil, got %v", out.Dialogue.ModalParticles)
	}
	if out.Taboos[0] != "禁止照搬" {
		t.Fatalf("taboos = %v", out.Taboos)
	}
}

func TestCompactStyleSkillsDoesNotMutateInput(t *testing.T) {
	items := []string{"甲", "乙", "丙", "丁", "戊", "己"}
	skills := &StyleSkills{Version: StyleSkillsVersion, Prose: ProseSkill{NarrativeVoice: items}}
	_ = CompactStyleSkills(skills)
	if len(skills.Prose.NarrativeVoice) != 6 {
		t.Fatalf("input mutated: %v", skills.Prose.NarrativeVoice)
	}
}

func TestCompactStyleSkillsCapsSourcesAndStripsVolatileFields(t *testing.T) {
	var sources []StyleSkillsSource
	for i := 0; i < 8; i++ {
		sources = append(sources, StyleSkillsSource{
			RelativePath: "f" + string(rune('a'+i)) + ".txt",
			SHA256:       "h",
			SizeBytes:    999,
			ModTime:      "2026-01-01T00:00:00Z",
		})
	}
	skills := &StyleSkills{Version: StyleSkillsVersion, Corpus: StyleSkillsCorpus{Sources: sources}}
	out := CompactStyleSkills(skills)

	if len(out.Corpus.Sources) != maxStyleSkillItems {
		t.Fatalf("sources = %d, want %d", len(out.Corpus.Sources), maxStyleSkillItems)
	}
	for _, s := range out.Corpus.Sources {
		if s.SizeBytes != 0 || s.ModTime != "" {
			t.Fatalf("volatile fields must be stripped: %+v", s)
		}
	}
	if sources[0].SizeBytes != 999 {
		t.Fatal("input sources mutated")
	}
}

func TestCompactStyleSkillsNil(t *testing.T) {
	if CompactStyleSkills(nil) != nil {
		t.Fatal("nil must compact to nil")
	}
}
