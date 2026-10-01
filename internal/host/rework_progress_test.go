package host

import (
	"strings"
	"testing"
	"time"

	"github.com/voocel/ainovel-cli/internal/domain"
	storepkg "github.com/voocel/ainovel-cli/internal/store"
)

// 复现线上死锁：Esc 暂停后 /rework --yes，队列建好但引擎永不启动，
// 5 分钟零事件、游标不动；再输 /rework 又被 Active() 挡回「请先 /rework stop」，
// 作者只能重启应用。守卫必须在落盘之前就判定并拉起引擎。
func TestReworkEngineNeedResumesFromPaused(t *testing.T) {
	h := &Host{lifecycle: lifecyclePaused}
	resume, err := h.reworkEngineNeed()
	if err != nil {
		t.Fatalf("暂停态应允许开始返工: %v", err)
	}
	if !resume {
		t.Error("暂停态必须要求自动恢复，否则队列无消费者")
	}
}

func TestReworkEngineNeedNoResumeWhenRunning(t *testing.T) {
	h := &Host{lifecycle: lifecycleRunning}
	resume, err := h.reworkEngineNeed()
	if err != nil || resume {
		t.Fatalf("运行态不应重复拉起引擎: resume=%v err=%v", resume, err)
	}
}

func TestReworkEngineNeedBlocksCocreateAndExclusive(t *testing.T) {
	cocreating := &Host{lifecycle: lifecyclePaused, cocreating: true}
	if _, err := cocreating.reworkEngineNeed(); err == nil {
		t.Error("阶段共创中应拒绝开返工")
	}
	excl := &Host{lifecycle: lifecyclePaused, exclusive: "导入"}
	if _, err := excl.reworkEngineNeed(); err == nil || !strings.Contains(err.Error(), "导入") {
		t.Errorf("独占作业中应拒绝并指明占用方，实际 %v", err)
	}
}

func TestReworkEngineNeedBlocksCompleted(t *testing.T) {
	h := &Host{lifecycle: lifecycleCompleted}
	if _, err := h.reworkEngineNeed(); err == nil || !strings.Contains(err.Error(), "完结") {
		t.Errorf("已完结应拒绝并提示 /reopen，实际 %v", err)
	}
}

// 守卫失败时绝不能把 pass 写进盘：写了就又成了无消费者的空中转，
// 且 /rework 随即被 Active() 挡住，作者再也开不了也停不干净。
func TestStartReworkPassWritesNothingWhenGuardRejects(t *testing.T) {
	st := storepkg.NewStore(t.TempDir())
	if err := st.Progress.Init(10); err != nil {
		t.Fatal(err)
	}
	for _, ch := range []int{1, 2, 3} {
		if err := st.Progress.MarkChapterComplete(ch, 2000, "", ""); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.Progress.UpdatePhase(domain.PhaseWriting); err != nil {
		t.Fatal(err)
	}
	h := &Host{store: st, lifecycle: lifecyclePaused, cocreating: true}

	if _, err := h.StartReworkPass(1, 3); err == nil {
		t.Fatal("共创中应拒绝开启返工")
	}
	p, err := st.Progress.Load()
	if err != nil {
		t.Fatal(err)
	}
	if p.ReworkPass != nil {
		t.Fatalf("守卫拒绝后不得留下 pass，实际 %+v", p.ReworkPass)
	}
}

// 每章一条进度，且带累计计数——长 pass 必须自带可读进度。
func TestEmitReworkProgressReportsCumulativeCount(t *testing.T) {
	h := &Host{events: make(chan Event, 16)}
	pass := &domain.ReworkPass{StartChapter: 1, EndChapter: 100, Cursor: 4, Reviewed: 3, Skipped: 2, Rewritten: []int{3}}
	h.emitReworkProgress(domain.ReworkProgress{Pass: pass, Reviewed: 3, Rewrote: true})

	ev := <-h.events
	for _, want := range []string{"3/100", "第 3 章", "已判返工", "已评审 3", "通过 2"} {
		if !strings.Contains(ev.Summary, want) {
			t.Errorf("进度播报缺 %q，实际 %q", want, ev.Summary)
		}
	}
}

// 收尾总结要与 advance_gate 的「返工队列已排空」区分：后者是一次性暂停信号，
// 可能永不触发；作者需要知道 pass 真的跑完了。
func TestEmitReworkProgressFinalSummaryOnDone(t *testing.T) {
	h := &Host{events: make(chan Event, 16)}
	pass := &domain.ReworkPass{
		StartChapter: 1, EndChapter: 3, Cursor: 4, Reviewed: 3, Skipped: 2,
		Rewritten: []int{2},
	}
	h.emitReworkProgress(domain.ReworkProgress{Pass: pass, Reviewed: 3, Done: true})

	ev := <-h.events
	for _, want := range []string{"逐章返工完成", "共 3 章", "已评审 3", "已返工 1", "实际返工章：2"} {
		if !strings.Contains(ev.Summary, want) {
			t.Errorf("收尾总结缺 %q，实际 %q", want, ev.Summary)
		}
	}
}

func TestEmitReworkProgressPassThroughVerdict(t *testing.T) {
	h := &Host{events: make(chan Event, 16)}
	pass := &domain.ReworkPass{StartChapter: 1, EndChapter: 5, Cursor: 2, Reviewed: 1, Skipped: 1}
	h.emitReworkProgress(domain.ReworkProgress{Pass: pass, Reviewed: 1})
	if ev := <-h.events; !strings.Contains(ev.Summary, "评审通过") {
		t.Errorf("未返工章应说明评审通过，实际 %q", ev.Summary)
	}
}

// 无 pass 时不得发空事件。
func TestEmitReworkProgressIgnoresNilPass(t *testing.T) {
	h := &Host{events: make(chan Event, 4)}
	h.emitReworkProgress(domain.ReworkProgress{})
	select {
	case ev := <-h.events:
		t.Fatalf("不应发事件，实际 %q", ev.Summary)
	case <-time.After(100 * time.Millisecond):
	}
}

// idle（本次会话尚未 Resume）同样没有消费者，必须一并拉起，
// 否则「启动后直接开返工」又是一次静默空转。
func TestReworkEngineNeedResumesFromIdle(t *testing.T) {
	h := &Host{lifecycle: lifecycleIdle}
	resume, err := h.reworkEngineNeed()
	if err != nil || !resume {
		t.Fatalf("idle 态应要求启动引擎: resume=%v err=%v", resume, err)
	}
}
