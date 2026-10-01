// /rules propose 的异步执行。
//
// 为什么不能同步调：LLM 调用动辄几十秒，若在 Bubble Tea 的 Update 循环里同步执行，
// 整个 UI（含 Ctrl+C）都会失去响应——用户看到的就是「卡死」。/style-skills 与
// /import 早就是异步的，propose 起初漏了这层，是回归。
//
// 本文件按它们的既有模式实现：Host 起 goroutine 干活并流式吐 panelEvent，TUI 只
// 消费事件。面板带 spinner 与实时已用时，用户随时能看到「在动、动了多久」。
package tui

import (
	"context"
	"fmt"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/voocel/ainovel-cli/internal/host"
	"github.com/voocel/ainovel-cli/internal/host/sim"
)

// eraProposeStage 是候选生成的阶段名。收尾阶段复用 sim 的 done/error 字面量，
// 让 panelEvent.terminal() 能正确判定（它比对的是 sim.StageDone/StageError）。
const (
	eraStageStart = "start"
	eraStageLLM   = "llm"
)

// adaptEraProposalEvents 把 Host 的 EraProposalEvent 转成面板事件。
//
// 阶段名映射到 sim.StageDone/StageError 是刻意的：panelEvent.terminal() 正是拿
// 这两个字面量判收尾，若直传 "done"/"error" 之外的字面量，面板会永远停在运行态，
// 用户就又看到「卡死」了。
func adaptEraProposalEvents(src <-chan host.EraProposalEvent) <-chan panelEvent {
	out := make(chan panelEvent, cap(src))
	go func() {
		defer close(out)
		for ev := range src {
			stage := string(ev.Stage)
			switch ev.Stage {
			case host.EraStageDone:
				stage = string(sim.StageDone)
			case host.EraStageError:
				stage = string(sim.StageError)
			}
			out <- panelEvent{
				at: ev.Time, stage: stage, current: ev.Current,
				total: ev.Total, message: ev.Message, err: ev.Err,
			}
		}
	}()
	return out
}

// startEraPropose 启动候选生成并返回面板与事件监听命令。
func startEraPropose(rt *host.Host, reqID int, era string, width, height int) (*simulationState, tea.Cmd, error) {
	if era == "" {
		return nil, nil, fmt.Errorf("朝代不能为空")
	}
	ctx, cancel := context.WithCancel(context.Background())
	ch, err := rt.GenerateEraProposalsAsync(ctx, era)
	if err != nil {
		cancel()
		return nil, nil, err
	}
	state := newSimulationState(reqID, "生成时代术语候选", era, width, height, cancel)
	state.frameTitle = "时代术语候选"
	state.failText = "时代术语候选生成失败"
	state.doneText = "候选已就绪，需逐条审阅后才会生效"
	state.stage = eraStageStart
	return state, listenPanelEvent(reqID, adaptEraProposalEvents(ch)), nil
}
