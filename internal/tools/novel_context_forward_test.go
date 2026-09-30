package tools

import (
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/voocel/ainovel-cli/internal/domain"
	"github.com/voocel/ainovel-cli/internal/store"
)

// reworkContextStore 造一本有 5 章正文与摘要的书写，供返工前向连续性测试。
func reworkContextStore(t *testing.T) *store.Store {
	t.Helper()
	st := store.NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	if err := st.Progress.Save(&domain.Progress{
		Phase:             domain.PhaseWriting,
		CurrentChapter:    6,
		CompletedChapters: []int{1, 2, 3, 4, 5},
		Flow:              domain.FlowWriting,
		TotalChapters:     20,
	}); err != nil {
		t.Fatal(err)
	}
	for _, ch := range []int{1, 2, 3, 4, 5} {
		if err := st.Summaries.SaveSummary(domain.ChapterSummary{
			Chapter: ch,
			Title:   "标题" + string(rune('0'+ch)),
			Summary: "第" + string(rune('0'+ch)) + "章的实际内容摘要",
		}); err != nil {
			t.Fatal(err)
		}
	}
	return st
}

func sortedKeys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func workingMemoryOf(t *testing.T, st *store.Store, chapter int) map[string]any {
	t.Helper()
	tool := newTestContextTool(st, References{}, "default")
	progress, err := st.Progress.Load()
	if err != nil {
		t.Fatal(err)
	}
	state := contextBuildState{chapter: chapter, progress: progress}
	reads := &contextReads{}
	envelope := newChapterContextEnvelope()
	tool.buildChapterWorkingMemory(&envelope, state, reads)
	return envelope.Working
}

func TestForwardContinuityInjectedDuringReworkPass(t *testing.T) {
	st := reworkContextStore(t)
	if _, err := st.Progress.StartReworkPass(1, 3, time.Now()); err != nil {
		t.Fatal(err)
	}
	wm := workingMemoryOf(t, st, 1)
	fc, ok := wm["forward_continuity"].(map[string]any)
	if !ok {
		t.Fatalf("pass 期间返工第 1 章必须注入 forward_continuity，实际 working keys=%v", sortedKeys(wm))
	}
	entries, ok := fc["chapters"].([]string)
	if !ok || len(entries) != 2 {
		t.Fatalf("应注入 N+1、N+2 两章，实际 %v", fc["chapters"])
	}
	if !strings.Contains(entries[0], "第 2 章") || !strings.Contains(entries[1], "第 3 章") {
		t.Fatalf("条目应依次为第 2、3 章：%v", entries)
	}
	if !strings.Contains(entries[0], "标题2") {
		t.Fatalf("条目应带后续章标题：%q", entries[0])
	}
	note, _ := fc["note"].(string)
	if !strings.Contains(note, "已定稿") || !strings.Contains(note, "第 1 章") {
		t.Fatalf("note 应说明后续章不可改、且是针对第 1 章：%q", note)
	}
	locked, _ := fc["locked_chapters"].([]string)
	if len(locked) != 2 || locked[0] != "2" || locked[1] != "3" {
		t.Fatalf("locked_chapters 应为 [2 3]，实际 %v", locked)
	}
}

// 不在 pass 期间时不得注入：正常写作路径不该为无关内容付出上下文。
func TestForwardContinuityAbsentWithoutPass(t *testing.T) {
	st := reworkContextStore(t)
	wm := workingMemoryOf(t, st, 1)
	if _, ok := wm["forward_continuity"]; ok {
		t.Fatal("无 pass 时不得注入 forward_continuity")
	}
}

// pass 范围外的章不得注入：正在写的新章不需要前向约束。
func TestForwardContinuityAbsentOutsidePassRange(t *testing.T) {
	st := reworkContextStore(t)
	if _, err := st.Progress.StartReworkPass(1, 3, time.Now()); err != nil {
		t.Fatal(err)
	}
	wm := workingMemoryOf(t, st, 5)
	if _, ok := wm["forward_continuity"]; ok {
		t.Fatal("pass 范围外的章不应注入 forward_continuity")
	}
}

// pass 完成后不再注入。
func TestForwardContinuityAbsentAfterPassDone(t *testing.T) {
	st := reworkContextStore(t)
	if _, err := st.Progress.StartReworkPass(1, 2, time.Now()); err != nil {
		t.Fatal(err)
	}
	for ch := 1; ch <= 2; ch++ {
		if _, err := st.Progress.ApplyReviewOutcome(domain.FlowWriting, nil, "通过", ch); err != nil {
			t.Fatal(err)
		}
	}
	wm := workingMemoryOf(t, st, 1)
	if _, ok := wm["forward_continuity"]; ok {
		t.Fatal("pass 完成后不应注入 forward_continuity")
	}
}

// progress 为 nil 时不得 panic（核心状态损坏路径）。
func TestForwardContinuityToleratesNilProgress(t *testing.T) {
	envelope := newChapterContextEnvelope()
	tool := newTestContextTool(reworkContextStore(t), References{}, "default")
	// state.progress 为 nil 是真实可达路径（TestContextToolRejectsCorruptCoreState 覆盖）
	tool.buildForwardContinuity(&contextBuildState{chapter: 1}, &envelope)
	if _, ok := envelope.Working["forward_continuity"]; ok {
		t.Fatal("nil progress 下不应注入")
	}
}
