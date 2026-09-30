package tui

import (
	"context"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/voocel/ainovel-cli/internal/host"
	"github.com/voocel/ainovel-cli/internal/host/sim"
	"github.com/voocel/ainovel-cli/internal/host/style"
)

// adaptSimEvents 把仿写画像事件流转成面板事件流，与风格 skill 汇到同一渲染路径。
func adaptSimEvents(src <-chan sim.Event) <-chan panelEvent {
	out := make(chan panelEvent, cap(src))
	go func() {
		defer close(out)
		for ev := range src {
			out <- panelEvent{
				at: ev.Time, stage: string(ev.Stage), current: ev.Current,
				total: ev.Total, message: ev.Message, err: ev.Err,
			}
		}
	}()
	return out
}

func adaptStyleEvents(src <-chan style.Event) <-chan panelEvent {
	out := make(chan panelEvent, cap(src))
	go func() {
		defer close(out)
		for ev := range src {
			out <- panelEvent{
				at: ev.Time, stage: string(ev.Stage), current: ev.Current,
				total: ev.Total, message: ev.Message, err: ev.Err,
			}
		}
	}()
	return out
}

// listenPanelEvent 从已适配的事件流取一条转成面板消息。msg.ch 保留同一条通道，
// 面板收到非终态事件后据此继续监听。
func listenPanelEvent(reqID int, ch <-chan panelEvent) tea.Cmd {
	return func() tea.Msg {
		ev, ok := <-ch
		if !ok {
			return nil
		}
		return simEventMsg{reqID: reqID, ev: ev, ch: ch}
	}
}

func listenSimulationEvent(reqID int, ch <-chan sim.Event) tea.Cmd {
	return listenPanelEvent(reqID, adaptSimEvents(ch))
}

func listenStyleSkillEvent(reqID int, ch <-chan style.Event) tea.Cmd {
	return listenPanelEvent(reqID, adaptStyleEvents(ch))
}

// startStyleSkills 处理 /style-skills。可选参数是语料目录，留空则由 Host 落到
// 书目录下的 ./simulate（和 /simulate 同一个默认，便于复用已有语料）。
func startStyleSkills(rt *host.Host, reqID int, args []string, width, height int) (*simulationState, tea.Cmd, error) {
	if len(args) > 1 {
		return nil, nil, fmt.Errorf("用法：/style-skills [语料目录]（留空默认 ./simulate）")
	}
	dir := "./simulate"
	if len(args) == 1 {
		dir = strings.TrimSpace(args[0])
		if dir == "" {
			return nil, nil, fmt.Errorf("用法：/style-skills [语料目录]（留空默认 ./simulate）")
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	ch, err := rt.StyleSkills(ctx, dir)
	if err != nil {
		cancel()
		return nil, nil, err
	}
	state := newSimulationState(reqID, "生成风格 skill", dir, width, height, cancel)
	state.frameTitle = "风格 skill"
	state.failText = "风格 skill 处理失败"
	state.doneText = "风格 skill 已就绪，写作时会从 novel_context 读取"
	return state, listenStyleSkillEvent(reqID, ch), nil
}
