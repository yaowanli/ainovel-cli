package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/voocel/ainovel-cli/internal/host"
	"github.com/voocel/ainovel-cli/internal/host/sim"
	"github.com/voocel/ainovel-cli/internal/host/style"
)

func TestStyleSkillsCommandIsRegisteredAndNeedsIdle(t *testing.T) {
	registry := commandRegistryInstance()
	spec, ok := registry.Find("style-skills")
	if !ok {
		t.Fatal("expected /style-skills command to be registered")
	}
	if !spec.NeedsIdle {
		t.Fatal("/style-skills should require idle state")
	}
	if spec.Usage != "/style-skills [语料目录]" {
		t.Fatalf("usage = %q", spec.Usage)
	}
	if !hasPaletteItem(builtinCommandItems(), "style-skills") {
		t.Fatal("expected style-skills in palette")
	}
}

func TestStyleSkillsCommandIsBlockedWhileRunning(t *testing.T) {
	m := Model{snapshot: host.UISnapshot{IsRunning: true}, eventIndex: map[string]int{}}
	next, _ := m.handleSlashCommand(slashCommand{name: "style-skills"})
	got := next.(Model)
	if len(got.events) != 1 || got.events[0].Category != "ERROR" {
		t.Fatalf("expected NeedsIdle to emit one error, got %+v", got.events)
	}
	if got.simulator != nil {
		t.Fatal("style-skills modal should not start while runtime is running")
	}
}

func TestSimulateUsageMessageExplainsNoArgs(t *testing.T) {
	// 原措辞只在多打了参数时出现，却只写"用法：/simulate"，等于在最不该解释的
	// 时候给出一句没有信息量的提示。
	_, _, err := startSimulate(nil, 0, []string{"多余的参数"}, 0, 0)
	if err == nil {
		t.Fatal("expected usage error")
	}
	if !strings.Contains(err.Error(), "不接参数") {
		t.Fatalf("usage error should explain the command takes no args: %v", err)
	}
}

func TestStyleSkillsRejectsTooManyArgs(t *testing.T) {
	for _, args := range [][]string{{"a", "b"}, {"x", "y", "z"}} {
		if _, _, err := startStyleSkills(nil, 0, args, 0, 0); err == nil {
			t.Fatalf("args %v must be rejected", args)
		}
	}
	if _, _, err := startStyleSkills(nil, 0, []string{"   "}, 0, 0); err == nil {
		t.Fatal("blank dir must be rejected")
	}
}

func TestPanelEventTerminalOnlyOnDoneAndError(t *testing.T) {
	if (panelEvent{stage: string(sim.StageScan)}).terminal() {
		t.Fatal("scan is not terminal")
	}
	if (panelEvent{stage: string(sim.StageAnalyze)}).terminal() {
		t.Fatal("analyze is not terminal")
	}
	if (panelEvent{stage: string(style.StageMerge)}).terminal() {
		t.Fatal("merge is not terminal")
	}
	if !(panelEvent{stage: string(sim.StageDone)}).terminal() {
		t.Fatal("done must be terminal")
	}
	if !(panelEvent{stage: string(style.StageError)}).terminal() {
		t.Fatal("error must be terminal")
	}
}

func TestAdaptStyleEventsForwardsAllFields(t *testing.T) {
	src := make(chan style.Event, 2)
	src <- style.Event{
		Time: time.Now(), Stage: style.StageAnalyze, Current: 1, Total: 3,
		Message: "分析语料 1/3", Err: nil,
	}
	src <- style.Event{Time: time.Now(), Stage: style.StageDone, Message: "完成"}
	close(src)

	var got []panelEvent
	for ev := range adaptStyleEvents(src) {
		got = append(got, ev)
	}
	if len(got) != 2 {
		t.Fatalf("forwarded %d events, want 2", len(got))
	}
	if got[0].stage != string(style.StageAnalyze) || got[0].current != 1 || got[0].total != 3 {
		t.Fatalf("first event = %+v", got[0])
	}
	if got[0].message != "分析语料 1/3" {
		t.Fatalf("message = %q", got[0].message)
	}
	if !got[1].terminal() {
		t.Fatal("done event must be terminal after adaptation")
	}
}

func TestAdaptSimEventsForwardsAllFields(t *testing.T) {
	src := make(chan sim.Event, 1)
	src <- sim.Event{Time: time.Now(), Stage: sim.StageMerge, Message: "合并画像"}
	close(src)

	var got []panelEvent
	for ev := range adaptSimEvents(src) {
		got = append(got, ev)
	}
	if len(got) != 1 || got[0].stage != string(sim.StageMerge) || got[0].message != "合并画像" {
		t.Fatalf("adapted = %+v", got)
	}
}

// skill 面板必须显示自己的名字，不能沿用"仿写画像"文案。
func TestStyleSkillsPanelUsesOwnLabels(t *testing.T) {
	s := newSimulationState(1, "生成风格 skill", "./simulate", 80, 24, nil)
	s.frameTitle = "风格 skill"
	s.failText = "风格 skill 处理失败"
	s.doneText = "风格 skill 已就绪"

	view := s.viewport.View()
	if !strings.Contains(view, "风格 skill") {
		t.Fatalf("panel should mention style skills: %q", view)
	}
	if strings.Contains(view, "仿写画像") {
		t.Fatalf("panel must not mention the simulation profile: %q", view)
	}
}
