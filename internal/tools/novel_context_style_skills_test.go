package tools

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/voocel/ainovel-cli/internal/domain"
	"github.com/voocel/ainovel-cli/internal/store"
)

func styleSkillsFixture() domain.StyleSkills {
	return domain.StyleSkills{
		Version: domain.StyleSkillsVersion,
		Corpus: domain.StyleSkillsCorpus{
			Sources: []domain.StyleSkillsSource{{
				RelativePath: "a.txt",
				SHA256:       "sha-a",
				Fingerprint:  domain.SourceFingerprint("a.txt", "sha-a"),
			}},
		},
		Prose: domain.ProseSkill{
			NarrativeVoice: []string{"第三人称限知"},
		},
		Dialogue: domain.DialogueSkill{
			SpeechTags:     []string{"标签偏好说与问"},
			FormsOfAddress: []string{"同辈直呼其名"},
			ModalParticles: []string{"句末少语气词"},
			VerbalTics:     []string{"犹豫用反问句"},
			LineLength:     []string{"单句台词偏短"},
			Dialect:        []string{"口语为主"},
		},
		Taboos: []string{"禁止照搬原文"},
	}
}

func seedStyleSkillsProject(t *testing.T) *store.Store {
	t.Helper()
	st := store.NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	if err := st.StyleSkills.Save(styleSkillsFixture()); err != nil {
		t.Fatal(err)
	}
	if err := st.Outline.SaveOutline([]domain.OutlineEntry{
		{Chapter: 1, Title: "Start", CoreEvent: "Begin"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.Progress.Init(1); err != nil {
		t.Fatal(err)
	}
	return st
}

func readContext(t *testing.T, tool *ContextTool, payload string) map[string]any {
	t.Helper()
	raw, err := tool.Execute(context.Background(), json.RawMessage(payload))
	if err != nil {
		t.Fatalf("Execute %s: %v", payload, err)
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestContextToolInjectsStyleSkills(t *testing.T) {
	st := seedStyleSkillsProject(t)
	tool := newTestContextTool(st, References{}, "default")

	// 架构师路径（无 chapter）→ planning_memory
	assertStyleSkillsInSection(t, readContext(t, tool, `{}`), "planning_memory")
	// 写作路径（chapter>0）→ working_memory
	assertStyleSkillsInSection(t, readContext(t, tool, `{"chapter":1}`), "working_memory")
}

func assertStyleSkillsInSection(t *testing.T, payload map[string]any, section string) {
	t.Helper()
	if _, ok := payload["style_skills"]; ok {
		t.Fatalf("unexpected top-level style_skills")
	}
	sectionMap, ok := payload[section].(map[string]any)
	if !ok {
		t.Fatalf("expected %s", section)
	}
	compact, ok := sectionMap["style_skills"].(map[string]any)
	if !ok {
		t.Fatalf("expected style_skills under %s", section)
	}

	dialogue, ok := compact["dialogue_skill"].(map[string]any)
	if !ok {
		t.Fatal("dialogue_skill missing")
	}
	// 六个对话维度必须都进上下文，否则 writer 无从遵守
	for _, field := range []string{
		"speech_tags", "forms_of_address", "modal_particles",
		"verbal_tics", "line_length", "dialect",
	} {
		items, ok := dialogue[field].([]any)
		if !ok || len(items) == 0 {
			t.Fatalf("dialogue_skill.%s missing or empty: %#v", field, dialogue[field])
		}
	}
	if _, ok := compact["prose_skill"].(map[string]any); !ok {
		t.Fatal("prose_skill missing")
	}
}

// skill 存在而画像不存在时也必须注入——两者是独立工件，不能互相依赖。
func TestContextToolInjectsStyleSkillsWithoutSimulationProfile(t *testing.T) {
	st := seedStyleSkillsProject(t)
	tool := newTestContextTool(st, References{}, "default")

	payload := readContext(t, tool, `{"chapter":1}`)
	section := payload["working_memory"].(map[string]any)
	if _, ok := section["simulation_profile"]; ok {
		t.Fatal("simulation_profile should be absent")
	}
	assertStyleSkillsInSection(t, payload, "working_memory")
}

// 工件不存在时静默跳过，绝不能崩也不能注入半成品。
func TestContextToolOmitsStyleSkillsWhenAbsent(t *testing.T) {
	st := store.NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	if err := st.Progress.Init(1); err != nil {
		t.Fatal(err)
	}
	tool := newTestContextTool(st, References{}, "default")

	payload := readContext(t, tool, `{"chapter":1}`)
	if section, ok := payload["working_memory"].(map[string]any); ok {
		if _, exists := section["style_skills"]; exists {
			t.Fatal("style_skills must be omitted when the artifact is absent")
		}
	}
}
