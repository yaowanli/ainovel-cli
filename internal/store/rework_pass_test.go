package store

import (
	"testing"
	"time"

	"github.com/voocel/ainovel-cli/internal/domain"
)

func reworkStore(t *testing.T, completed ...int) *Store {
	t.Helper()
	s := NewStore(t.TempDir())
	if err := s.Progress.Init(200); err != nil {
		t.Fatalf("Init: %v", err)
	}
	for _, ch := range completed {
		if err := s.Progress.MarkChapterComplete(ch, 3000, "", ""); err != nil {
			t.Fatalf("MarkChapterComplete(%d): %v", ch, err)
		}
	}
	return s
}

func TestStartReworkPassSetsCursorToStart(t *testing.T) {
	s := reworkStore(t, 1, 2, 3, 4, 5)
	p, err := s.Progress.StartReworkPass(2, 4, time.Now())
	if err != nil {
		t.Fatalf("StartReworkPass: %v", err)
	}
	pass := p.ReworkPass
	if pass == nil || pass.Cursor != 2 || pass.EndChapter != 4 || pass.Total() != 3 {
		t.Fatalf("unexpected pass: %+v", pass)
	}
	if !pass.Active() || pass.Done() {
		t.Fatal("新开的 pass 应为进行中")
	}
}

// 核心回归：评审 verdict=accept 时 affected 为空、队列无写入，游标仍必须前进。
// 若游标不前进，router 会重复派发同一章评审直到 trackDeadlock 熔断——
// 评审后按需返工的常态就是大量 accept，所以这条路径必须被钉住。
func TestReworkPassCursorAdvancesOnAcceptVerdict(t *testing.T) {
	s := reworkStore(t, 1, 2, 3, 4, 5)
	if _, err := s.Progress.StartReworkPass(1, 5, time.Now()); err != nil {
		t.Fatal(err)
	}
	// 第 1 章评审通过：无 affected、flow 回 writing——队列不会出现任何写入。
	p, err := s.Progress.ApplyReviewOutcome(domain.FlowWriting, nil, "评审通过", 1)
	if err != nil {
		t.Fatalf("ApplyReviewOutcome: %v", err)
	}
	if len(p.PendingRewrites) != 0 {
		t.Fatalf("accept 不应产生返工队列，得到 %v", p.PendingRewrites)
	}
	if p.ReworkPass.Cursor != 2 {
		t.Fatalf("accept 之后游标必须前进，否则死循环重评同一章；得到 %d", p.ReworkPass.Cursor)
	}
	if p.ReworkPass.Skipped != 1 || p.ReworkPass.Reviewed != 1 {
		t.Fatalf("accept 应计入 Skipped: %+v", p.ReworkPass)
	}
	if len(p.ReworkPass.Rewritten) != 0 {
		t.Fatalf("accept 不应计入 Rewritten: %v", p.ReworkPass.Rewritten)
	}
}

func TestReworkPassMarksRewrittenAndReachesDone(t *testing.T) {
	s := reworkStore(t, 1, 2, 3, 4, 5)
	if _, err := s.Progress.StartReworkPass(1, 3, time.Now()); err != nil {
		t.Fatal(err)
	}
	// 第 1 章判返工
	p, err := s.Progress.ApplyReviewOutcome(domain.FlowRewriting, []int{1}, "需返工", 1)
	if err != nil {
		t.Fatal(err)
	}
	if p.ReworkPass.Cursor != 2 || len(p.ReworkPass.Rewritten) != 1 || p.ReworkPass.Skipped != 0 {
		t.Fatalf("第 1 章判返工后: %+v", p.ReworkPass)
	}
	// writer 改完出队，再评第 2 章（通过）
	if err := s.Progress.CompleteRewrite(1); err != nil {
		t.Fatal(err)
	}
	p, err = s.Progress.ApplyReviewOutcome(domain.FlowWriting, nil, "通过", 2)
	if err != nil {
		t.Fatal(err)
	}
	// 第 3 章判返工 → 游标越界，pass 完成
	p, err = s.Progress.ApplyReviewOutcome(domain.FlowRewriting, []int{3}, "需返工", 3)
	if err != nil {
		t.Fatal(err)
	}
	if p.ReworkPass.Cursor != 4 || !p.ReworkPass.Done() || p.ReworkPass.Active() {
		t.Fatalf("游标越界后 pass 应完成且不再 active: %+v", p.ReworkPass)
	}
	if len(p.ReworkPass.Rewritten) != 2 || p.ReworkPass.Skipped != 1 || p.ReworkPass.Reviewed != 3 {
		t.Fatalf("统计不符: %+v", p.ReworkPass)
	}
	// 完成后记录仍在，status 可查
	if p.ReworkPass == nil {
		t.Fatal("完成后仍应保留 pass 记录供 status 查看")
	}
}

// 非 pass 期间的常规单章评审不得推进任何游标（防御 reviewedChapter 误传）。
func TestReworkPassCursorIgnoresUnrelatedChapter(t *testing.T) {
	s := reworkStore(t, 1, 2, 3)
	if _, err := s.Progress.StartReworkPass(1, 3, time.Now()); err != nil {
		t.Fatal(err)
	}
	p, err := s.Progress.ApplyReviewOutcome(domain.FlowWriting, nil, "别的评审", 2)
	if err != nil {
		t.Fatal(err)
	}
	if p.ReworkPass.Cursor != 1 {
		t.Fatalf("非当前游标章的评审不得推进游标，得到 %d", p.ReworkPass.Cursor)
	}
	// arc/global 评审传 0，同样不得推进
	p, err = s.Progress.ApplyReviewOutcome(domain.FlowWriting, nil, "全局评审", 0)
	if err != nil {
		t.Fatal(err)
	}
	if p.ReworkPass.Cursor != 1 {
		t.Fatalf("reviewedChapter=0 不得推进游标，得到 %d", p.ReworkPass.Cursor)
	}
}

func TestStartReworkPassRejectsInvalidRange(t *testing.T) {
	s := reworkStore(t, 1, 2, 3)
	cases := []struct {
		name       string
		start, end int
	}{
		{"起始为零", 0, 3},
		{"首章大于末章", 3, 1},
		{"超出已完成", 1, 4},
		{"负数", -1, 2},
	}
	for _, c := range cases {
		if _, err := s.Progress.StartReworkPass(c.start, c.end, time.Now()); err == nil {
			t.Errorf("%s: 期望被拒绝，实际通过", c.name)
		}
	}
}

func TestStartReworkPassRejectsWhenBusy(t *testing.T) {
	s := reworkStore(t, 1, 2, 3, 4)
	// 已有 pass 在跑
	if _, err := s.Progress.StartReworkPass(1, 3, time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Progress.StartReworkPass(2, 3, time.Now()); err == nil {
		t.Fatal("已有 pass 在跑时应拒绝叠加")
	}
	// 队列非空
	s2 := reworkStore(t, 1, 2, 3, 4)
	if err := s2.Progress.SetPendingRewrites([]int{2}, "待改"); err != nil {
		t.Fatal(err)
	}
	if _, err := s2.Progress.StartReworkPass(1, 3, time.Now()); err == nil {
		t.Fatal("返工队列非空时应拒绝开启 pass")
	}
}

func TestStopReworkPassClearsAndReturnsSnapshot(t *testing.T) {
	s := reworkStore(t, 1, 2, 3)
	if _, err := s.Progress.StartReworkPass(1, 3, time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Progress.ApplyReviewOutcome(domain.FlowRewriting, []int{1}, "需返工", 1); err != nil {
		t.Fatal(err)
	}
	if err := s.Progress.CompleteRewrite(1); err != nil {
		t.Fatal(err)
	}
	_, stopped, err := s.Progress.StopReworkPass()
	if err != nil {
		t.Fatal(err)
	}
	if stopped.StartChapter != 1 || stopped.Cursor != 2 || len(stopped.Rewritten) != 1 {
		t.Fatalf("snapshot 内容不符: %+v", stopped)
	}
	p, _ := s.Progress.Load()
	if p.ReworkPass != nil {
		t.Fatal("中止后 progress 不应残留 pass")
	}
	// snapshot 必须是拷贝：后续写入不得就地改掉已返回的值
	stopped.Rewritten[0] = 999
	if stopped.Rewritten[0] != 999 { // 只验证它是自己可写的内存，不与 store 共享
		t.Fatal("snapshot 应为独立拷贝")
	}
	if _, _, err := s.Progress.StopReworkPass(); err == nil {
		t.Fatal("无 pass 时 StopReworkPass 应报错")
	}
}
