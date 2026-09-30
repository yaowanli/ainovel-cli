package style

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/voocel/agentcore"
	"github.com/voocel/ainovel-cli/internal/domain"
	"github.com/voocel/ainovel-cli/internal/host/corpus"
	"github.com/voocel/ainovel-cli/internal/store"
)

type scriptedLLM struct {
	responses []string
	calls     atomic.Int32
}

func (s *scriptedLLM) Generate(_ context.Context, _ []agentcore.Message, _ []agentcore.ToolSpec, _ ...agentcore.CallOption) (*agentcore.LLMResponse, error) {
	idx := int(s.calls.Add(1)) - 1
	if idx >= len(s.responses) {
		return nil, fmt.Errorf("scriptedLLM exhausted at call %d", idx+1)
	}
	return &agentcore.LLMResponse{
		Message: agentcore.Message{
			Role:      agentcore.RoleAssistant,
			Content:   []agentcore.ContentBlock{agentcore.TextBlock(s.responses[idx])},
			Timestamp: time.Now(),
		},
	}, nil
}

func validSourceReportJSON(summary string) string {
	return `{
		"summary": "` + summary + `",
		"narrative_voice": ["第三人称限知，叙述距离偏近"],
		"sentence_rhythm": ["短句为主，长句仅用于转折"],
		"description_texture": ["动作与神态交替，几乎不写心理"],
		"pacing": ["场景推进快，章末留钩"],
		"speech_tags": ["标签动词集中在说/问，答句常无标签"],
		"forms_of_address": ["同辈直呼其名，长辈用敬称"],
		"modal_particles": ["句末语气词少用，疑问用吗收尾"],
		"verbal_tics": ["以反问句承载犹豫，句式固定"],
		"line_length": ["单句台词多在十字以内"],
		"dialect": ["口语化程度高，书面语少"],
		"taboos": ["禁止照搬原文长句"]
	}`
}

func validSkillsJSON() string {
	return `{
		"prose_skill": {
			"narrative_voice": ["第三人称限知，贴人物视角"],
			"sentence_rhythm": ["短句为主，转折处才用长句"],
			"description_texture": ["以动作神态代替心理描写"],
			"pacing": ["推进快，章末留钩"]
		},
		"dialogue_skill": {
			"speech_tags": ["标签偏好说与问，答句裸对白"],
			"forms_of_address": ["同辈直呼其名"],
			"modal_particles": ["句末少语气词"],
			"verbal_tics": ["犹豫用反问句表达"],
			"line_length": ["单句台词偏短，十字内"],
			"dialect": ["口语为主，书面语少"]
		},
		"taboos": ["禁止照搬原文表达与人物设定"]
	}`
}

func drain(t *testing.T, events <-chan Event) Event {
	t.Helper()
	var last Event
	for ev := range events {
		if ev.Err != nil {
			t.Fatalf("style run errored: %v (stage %s: %s)", ev.Err, ev.Stage, ev.Message)
		}
		last = ev
	}
	return last
}

func newStore(t *testing.T, dir string) *store.Store {
	t.Helper()
	st := store.NewStore(filepath.Join(dir, "output", "novel"))
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	return st
}

func existingSkills(relPath, sha string) *domain.StyleSkills {
	return &domain.StyleSkills{
		Version: domain.StyleSkillsVersion,
		Corpus: domain.StyleSkillsCorpus{
			Sources: []domain.StyleSkillsSource{{RelativePath: relPath, SHA256: sha}},
		},
	}
}

func writeSource(t *testing.T, dir, name, content string) {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestRunGeneratesSkillsThenSkipsUnchanged(t *testing.T) {
	dir := t.TempDir()
	sourceDir := filepath.Join(dir, "simulate")
	writeSource(t, sourceDir, "a.txt", "开头立人物。\n对话干脆。\n")
	writeSource(t, sourceDir, "nested/b.md", "# B\n\n第二篇。")

	st := newStore(t, dir)
	llm := &scriptedLLM{responses: []string{
		validSourceReportJSON("a tone"),
		validSourceReportJSON("b tone"),
		validSkillsJSON(),
	}}

	events, err := Run(context.Background(), Deps{
		Store: st, LLM: llm, Prompts: Prompts{Source: "source prompt", Merge: "merge prompt"},
	}, Options{SourceDir: sourceDir})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if last := drain(t, events); last.Stage != StageDone {
		t.Fatalf("last stage = %s, want %s", last.Stage, StageDone)
	}
	if got := llm.calls.Load(); got != 3 {
		t.Fatalf("first run LLM calls = %d, want 3", got)
	}

	skills, err := st.StyleSkills.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if skills == nil {
		t.Fatal("skills not persisted")
	}
	if len(skills.Corpus.Sources) != 2 {
		t.Fatalf("sources = %d, want 2", len(skills.Corpus.Sources))
	}
	if skills.Version != "style_skills.v1" {
		t.Fatalf("version = %q", skills.Version)
	}
	// 六个对话维度都必须落盘，否则等于没做这个功能
	for name, items := range map[string][]string{
		"speech_tags":      skills.Dialogue.SpeechTags,
		"forms_of_address": skills.Dialogue.FormsOfAddress,
		"modal_particles":  skills.Dialogue.ModalParticles,
		"verbal_tics":      skills.Dialogue.VerbalTics,
		"line_length":      skills.Dialogue.LineLength,
		"dialect":          skills.Dialogue.Dialect,
	} {
		if len(items) == 0 {
			t.Fatalf("dialogue_skill.%s is empty", name)
		}
	}
	if len(skills.Prose.NarrativeVoice) == 0 {
		t.Fatal("prose_skill.narrative_voice is empty")
	}

	// 源文件未变 → 0 次 LLM 调用
	llm2 := &scriptedLLM{}
	events, err = Run(context.Background(), Deps{
		Store: st, LLM: llm2, Prompts: Prompts{Source: "source prompt", Merge: "merge prompt"},
	}, Options{SourceDir: sourceDir})
	if err != nil {
		t.Fatalf("rerun: %v", err)
	}
	var upToDate bool
	for ev := range events {
		if ev.Err != nil {
			t.Fatalf("rerun errored: %v", ev.Err)
		}
		if strings.Contains(ev.Message, "已是最新") {
			upToDate = true
		}
	}
	if !upToDate {
		t.Fatal("expected up-to-date message")
	}
	if got := llm2.calls.Load(); got != 0 {
		t.Fatalf("unchanged rerun LLM calls = %d, want 0", got)
	}
}

func TestRunIncrementallyAnalyzesChangedSourceOnly(t *testing.T) {
	dir := t.TempDir()
	sourceDir := filepath.Join(dir, "simulate")
	writeSource(t, sourceDir, "a.txt", "原始语料")
	writeSource(t, sourceDir, "b.txt", "另一篇")

	st := newStore(t, dir)
	llm := &scriptedLLM{responses: []string{
		validSourceReportJSON("a"), validSourceReportJSON("b"), validSkillsJSON(),
	}}
	events, err := Run(context.Background(), Deps{
		Store: st, LLM: llm, Prompts: Prompts{Source: "s", Merge: "m"},
	}, Options{SourceDir: sourceDir})
	if err != nil {
		t.Fatal(err)
	}
	drain(t, events)

	// 只改 a.txt：应只重跑 a 的 analyze + 一次 merge = 2 次调用
	writeSource(t, sourceDir, "a.txt", "改过的语料")
	llm2 := &scriptedLLM{responses: []string{validSourceReportJSON("a2"), validSkillsJSON()}}
	events, err = Run(context.Background(), Deps{
		Store: st, LLM: llm2, Prompts: Prompts{Source: "s", Merge: "m"},
	}, Options{SourceDir: sourceDir})
	if err != nil {
		t.Fatal(err)
	}
	drain(t, events)
	if got := llm2.calls.Load(); got != 2 {
		t.Fatalf("incremental LLM calls = %d, want 2", got)
	}
}

func TestRunRejectsIncompleteDeps(t *testing.T) {
	if _, err := Run(context.Background(), Deps{LLM: &scriptedLLM{}}, Options{SourceDir: "/tmp"}); err == nil {
		t.Fatal("missing store must fail")
	}
	if _, err := Run(context.Background(), Deps{Store: &store.Store{}}, Options{SourceDir: "/tmp"}); err == nil {
		t.Fatal("missing llm must fail")
	}
	st := newStore(t, t.TempDir())
	if _, err := Run(context.Background(), Deps{Store: st, LLM: &scriptedLLM{}}, Options{SourceDir: "  "}); err == nil {
		t.Fatal("empty source dir must fail")
	}
}

func TestRunReportsEmptyCorpus(t *testing.T) {
	dir := t.TempDir()
	sourceDir := filepath.Join(dir, "simulate")
	if err := os.MkdirAll(sourceDir, 0o755); err != nil {
		t.Fatal(err)
	}
	st := newStore(t, dir)
	events, err := Run(context.Background(), Deps{
		Store: st, LLM: &scriptedLLM{}, Prompts: Prompts{Source: "s", Merge: "m"},
	}, Options{SourceDir: sourceDir})
	if err != nil {
		t.Fatal(err)
	}
	var failed bool
	for ev := range events {
		if ev.Stage == StageError {
			failed = true
		}
	}
	if !failed {
		t.Fatal("empty corpus must surface an error event")
	}
}

func TestRunReportsMissingCorpus(t *testing.T) {
	st := newStore(t, t.TempDir())
	events, err := Run(context.Background(), Deps{
		Store: st, LLM: &scriptedLLM{}, Prompts: Prompts{Source: "s", Merge: "m"},
	}, Options{SourceDir: filepath.Join(t.TempDir(), "missing")})
	if err != nil {
		t.Fatal(err)
	}
	var failed bool
	for ev := range events {
		if ev.Stage == StageError {
			failed = true
		}
	}
	if !failed {
		t.Fatal("missing corpus must surface an error event")
	}
}

func TestRunHonorsContextCancellation(t *testing.T) {
	dir := t.TempDir()
	sourceDir := filepath.Join(dir, "simulate")
	writeSource(t, sourceDir, "a.txt", "语料")

	st := newStore(t, dir)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	events, err := Run(ctx, Deps{
		Store: st, LLM: &scriptedLLM{responses: []string{validSourceReportJSON("a"), validSkillsJSON()}},
		Prompts: Prompts{Source: "s", Merge: "m"},
	}, Options{SourceDir: sourceDir})
	if err != nil {
		t.Fatal(err)
	}
	var sawError bool
	for ev := range events {
		if ev.Stage == StageError {
			sawError = true
		}
	}
	if !sawError {
		t.Fatal("cancelled run must surface an error event")
	}
}

func TestRunRequiresPrompts(t *testing.T) {
	llm := &scriptedLLM{responses: []string{validSourceReportJSON("a")}}
	if _, err := AnalyzeSource(context.Background(), llm, "  ", corpus.Source{RelativePath: "a.txt"}); err == nil {
		t.Fatal("blank source prompt must fail")
	}
	if _, err := MergeSkills(context.Background(), llm, "", nil, nil); err == nil {
		t.Fatal("blank merge prompt must fail")
	}
}
