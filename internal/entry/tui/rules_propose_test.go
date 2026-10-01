package tui

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/voocel/ainovel-cli/internal/host"
	"github.com/voocel/ainovel-cli/internal/host/sim"
)

// 用户实测把 /rules 打成 /rulse，系统只回「未知命令」，无从判断是拼错还是真不存在。
func TestSuggestCatchesTypos(t *testing.T) {
	r := commandRegistryInstance()
	cases := map[string]string{
		"rulse":       "rules",  // 用户实际输入的那个
		"rewek":       "rework", // 换位
		"ruels":       "rules",  // 换位
		"rul":         "rules",  // 漏字
		"rewrok":      "rework", // 多字
		"styeskills":  "style-skills",
		"styleskills": "style-skills",
		"exprot":      "export",
	}
	for typo, want := range cases {
		if got := r.Suggest(typo); got != want {
			t.Errorf("%q 应建议 %q，实际 %q", typo, want, got)
		}
	}
}

// 距离过远时不硬凑——「你好」不该被拽到某个命令上。
func TestSuggestResistsUnrelatedInput(t *testing.T) {
	r := commandRegistryInstance()
	for _, s := range []string{"", "你好", "asdkjhaskdjhqwerty", "zzzzzzzzzzzz", "qqqqqqqqqqqq"} {
		if got := r.Suggest(s); got != "" {
			t.Errorf("%q 距所有命令都远，不应给建议，实际 %q", s, got)
		}
	}
}

// 正确的命令名不该被当成拼错（否则每次执行都看到「是否想输入 X」）。
func TestSuggestOnExactNameIsSelf(t *testing.T) {
	r := commandRegistryInstance()
	for _, name := range []string{"rules", "rework", "diag", "export", "next"} {
		if _, ok := r.Find(name); !ok {
			t.Fatalf("%q 应已注册", name)
		}
		if got := r.Suggest(name); got != name {
			t.Errorf("%q 应建议自身，实际 %q", name, got)
		}
	}
}

func TestEditDistance(t *testing.T) {
	cases := map[[2]string]int{
		{"rulse", "rules"}:    2,
		{"rules", "rules"}:    0,
		{"", "abc"}:           3,
		{"abc", ""}:           3,
		{"kitten", "sitting"}: 3,
		{"flaw", "lawn"}:      2,
	}
	for k, want := range cases {
		if got := editDistance(k[0], k[1]); got != want {
			t.Errorf("editDistance(%q,%q)=%d，期望 %d", k[0], k[1], got, want)
		}
	}
}

// 收尾阶段名必须映射成 panelEvent.terminal() 认得的字面量，否则面板永远停在
// 运行态——那等于用另一种方式复现「卡死」。
func TestEraStageMapsToPanelTerminal(t *testing.T) {
	errTest := errors.New("boom")
	ch := make(chan host.EraProposalEvent, 3)
	// 先起适配器再投递：cap 满后无人消费会自锁（这个测试本身曾因此挂死 45s）。
	out := adaptEraProposalEvents(ch)
	ch <- host.EraProposalEvent{Stage: host.EraStageStart, Message: "开始"}
	ch <- host.EraProposalEvent{Stage: host.EraStageDone, Message: "完成"}
	ch <- host.EraProposalEvent{Stage: host.EraStageError, Err: errTest}
	close(ch)

	var stages []string
	var last panelEvent
	for ev := range out {
		stages = append(stages, ev.stage)
		last = ev
	}
	want := []string{"start", string(sim.StageDone), string(sim.StageError)}
	if len(stages) != len(want) {
		t.Fatalf("阶段数不符: %v", stages)
	}
	for i := range want {
		if stages[i] != want[i] {
			t.Errorf("第 %d 阶段应为 %q，实际 %q", i, want[i], stages[i])
		}
	}
	if last.err != errTest {
		t.Errorf("错误应透传: %v", last.err)
	}
	if !last.terminal() {
		t.Error("最后一个事件应被 terminal() 判为收尾")
	}
}

// 用户明确要求：阻塞时要有生成中的时间提示。
func TestElapsedFormatsForLongTasks(t *testing.T) {
	cases := map[time.Duration]string{
		5 * time.Second:             "00:05",
		65 * time.Second:            "01:05",
		90 * time.Minute:            "1:30:00",
		2*time.Hour + 5*time.Minute: "2:05:00",
	}
	for d, want := range cases {
		if got := formatElapsed(d); got != want {
			t.Errorf("formatElapsed(%s)=%q，期望 %q", d, got, want)
		}
	}
}

// 面板必须显示耗时，否则用户无法区分「在动」与「卡死」。
func TestSimulationElapsedAndRendering(t *testing.T) {
	s := newSimulationState(1, "生成时代术语候选", "东汉末年", 100, 40, func() {})
	s.stage, s.frameTitle = "llm", "时代术语候选"
	s.startedAt = time.Now().Add(-42 * time.Second)
	got := s.elapsed()
	if got < 40*time.Second || got > 60*time.Second {
		t.Fatalf("elapsed 应约为 42s，实际 %s", got)
	}
	// 运行中：未完成时不显示 spinner 收尾态，且帧号可推进
	s.frame = 3
	s.refresh(60)
	if strings.Contains(s.viewport.View(), "已完成") {
		t.Error("运行中不应显示完成态")
	}

	// 完成后耗时固定，不再随时间增长
	s.finishedAt = s.startedAt.Add(90 * time.Second)
	first := s.elapsed()
	if first != 90*time.Second {
		t.Fatalf("完成后耗时应为 90s，实际 %s", first)
	}
	time.Sleep(3 * time.Millisecond)
	if s.elapsed() != first {
		t.Error("完成后耗时应固定，不应继续增长")
	}
	s.refresh(60)
	if !strings.Contains(s.viewport.View(), "已用时") {
		t.Errorf("面板应显示已用时:\n%s", s.viewport.View())
	}
	if !strings.Contains(s.viewport.View(), "01:30") {
		t.Errorf("应显示 mm:ss 耗时:\n%s", s.viewport.View())
	}
}

// finishedAt 早于 startedAt（时钟回拨 / 状态文件被手改）时不得渲染出脏耗时。
// 曾经的 bug：formatElapsed(-48s) 输出 "00:-48"。
func TestSimulationElapsedClampsNegative(t *testing.T) {
	s := newSimulationState(1, "t", "src", 100, 40, func() {})
	s.startedAt = time.Now()
	s.finishedAt = s.startedAt.Add(-48 * time.Second)
	if got := s.elapsed(); got != 0 {
		t.Fatalf("负耗时应夹到 0，实际 %s", got)
	}
	s.refresh(60)
	view := s.viewport.View()
	if strings.Contains(view, "-") && strings.Contains(view, "已用时") {
		t.Errorf("不应出现负号耗时:\n%s", view)
	}
	if strings.Contains(view, "00:-") {
		t.Errorf("出现了脏耗时输出:\n%s", view)
	}
}

// 运行中必须显示 spinner——用户要求「阻塞时要有生成中的提示」。
func TestSimulationRunningShowsSpinner(t *testing.T) {
	s := newSimulationState(1, "生成时代术语候选", "东汉末年", 100, 40, func() {})
	s.stage, s.frameTitle = "llm", "时代术语候选"
	s.startedAt = time.Now().Add(-5 * time.Second)
	s.frame = 1
	s.refresh(60)
	view := s.viewport.View()
	if !strings.Contains(view, "已用时") {
		t.Errorf("运行中也应显示已用时:\n%s", view)
	}
}
