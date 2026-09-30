package flow

import (
	"strings"
	"testing"

	"github.com/voocel/ainovel-cli/internal/domain"
)

func writingWithPass(cursor, end int) State {
	return State{Progress: &domain.Progress{
		Phase:             domain.PhaseWriting,
		CurrentChapter:    end + 1,
		CompletedChapters: []int{1, 2, 3, 4, 5, 6, 7, 8},
		Flow:              domain.FlowWriting,
		ReworkPass: &domain.ReworkPass{
			StartChapter: 1,
			EndChapter:   end,
			Cursor:       cursor,
		},
	}}
}

func TestRoute_ReworkPassDispatchesEditorForCursor(t *testing.T) {
	got := Route(writingWithPass(3, 8))
	if got == nil {
		t.Fatal("pass 进行中必须派发，不得返回 nil")
	}
	if got.Agent != "editor" {
		t.Fatalf("应派发 editor，实际 %q", got.Agent)
	}
	if got.Chapter != 3 {
		t.Fatalf("Chapter 应为游标 3，实际 %d", got.Chapter)
	}
	if !strings.Contains(got.Task, "scope=chapter") || !strings.Contains(got.Task, "chapter=3") {
		t.Fatalf("任务串必须约束为 scope=chapter/chapter=3：%s", got.Task)
	}
	// 返工语境必须点明与常规抽检不同，否则 editor 仍会按保守标准放过
	if !strings.Contains(got.Task, "返工") {
		t.Fatalf("任务串应说明这是返工语境：%s", got.Task)
	}
	// 应引用前向连续性约束
	if !strings.Contains(got.Task, "forward_continuity") {
		t.Fatalf("任务串应引用 forward_continuity 约束：%s", got.Task)
	}
}

// 位置约束一：待改章必须先改完才评下一章，不能交错。
func TestRoute_ReworkPassYieldsToPendingRewrites(t *testing.T) {
	s := writingWithPass(3, 8)
	s.Progress.PendingRewrites = []int{3}
	got := Route(s)
	if got == nil {
		t.Fatal("队列非空时应派发，不得返回 nil")
	}
	if got.Agent != "writer" || got.Chapter != 3 {
		t.Fatalf("队列非空时必须先改第 3 章，实际 agent=%q chapter=%d", got.Agent, got.Chapter)
	}
}

// 位置约束二：聚合刷新不能把 pass 饿死。
func TestRoute_ReworkPassBeatsAggregateRefresh(t *testing.T) {
	s := writingWithPass(3, 8)
	s.AggregateRefresh = &AggregateRefresh{Kind: AggregateArcSummary}
	got := Route(s)
	if got == nil || got.Agent != "editor" || got.Chapter != 3 {
		t.Fatalf("pass 应优先于聚合刷新，实际 %+v", got)
	}
	if strings.Contains(got.Task, "save_arc_summary") {
		t.Fatalf("pass 期间不应派发聚合工件：%s", got.Task)
	}
}

// 位置约束三：pass 期间不得续写下一章。
func TestRoute_ReworkPassSuppressesNextChapterWrite(t *testing.T) {
	got := Route(writingWithPass(3, 8))
	if strings.Contains(got.Task, "第 9 章") {
		t.Fatalf("pass 期间不应写新章节：%s", got.Task)
	}
}

// 游标越过终点 → 分支自然落空，自动恢复续写。
func TestRoute_ReworkPassDoneFallsThroughToWriter(t *testing.T) {
	s := writingWithPass(9, 8) // cursor > end
	got := Route(s)
	if got == nil {
		t.Fatal("pass 完成后应继续路由")
	}
	if got.Agent != "writer" {
		t.Fatalf("pass 完成后应回到 writer 续写，实际 %q", got.Agent)
	}
	if got.Chapter != 9 {
		t.Fatalf("应续写第 9 章，实际 %d", got.Chapter)
	}
}

func TestRoute_NoReworkPassUnaffected(t *testing.T) {
	s := State{Progress: &domain.Progress{
		Phase:             domain.PhaseWriting,
		CurrentChapter:    4,
		CompletedChapters: []int{1, 2, 3},
		Flow:              domain.FlowWriting,
	}}
	got := Route(s)
	if got == nil || got.Agent != "writer" {
		t.Fatalf("无 pass 时行为应不变，实际 %+v", got)
	}
}
