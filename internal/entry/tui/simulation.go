package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/voocel/ainovel-cli/internal/host"
	"github.com/voocel/ainovel-cli/internal/host/sim"
)

// panelEvent 是后台作业面板的归一化事件。仿写画像（sim.Event）与风格 skill
// （style.Event）阶段语义相同但类型不同，面板只认这个结构，两个包各自在边界转换。
type panelEvent struct {
	at      time.Time
	stage   string
	current int
	total   int
	message string
	err     error
}

func (e panelEvent) terminal() bool {
	return e.stage == string(sim.StageDone) || e.stage == string(sim.StageError)
}

type simulationState struct {
	reqID  int
	title  string
	source string
	// frameTitle/failText/doneText 让同一面板能显示"仿写画像"或"风格 skill"，
	// 避免把业务名写死在渲染里。
	frameTitle string
	failText   string
	doneText   string
	stage      string
	current    int
	total      int
	startedAt  time.Time
	finishedAt time.Time
	history    []simulationLine
	err        error
	done       bool
	cancel     context.CancelFunc
	viewport   viewport.Model
}

type simulationLine struct {
	at      time.Time
	stage   string
	current int
	total   int
	message string
	err     error
}

type simEventMsg struct {
	reqID int
	ev    panelEvent
	ch    <-chan panelEvent
}

func (m simEventMsg) terminal() bool { return m.ev.terminal() }

func newSimulationState(reqID int, title, source string, width, height int, cancel context.CancelFunc) *simulationState {
	boxW, boxH := reportModalSize(width, height)
	contentW := paddedModalContentWidth(boxW)
	vp := viewport.New(contentW, boxH-4)
	s := &simulationState{
		reqID:      reqID,
		title:      title,
		source:     source,
		frameTitle: "仿写画像",
		failText:   "仿写画像处理失败",
		doneText:   "仿写画像已就绪，后续 Agent 会从 novel_context 读取",
		stage:      string(sim.StageScan),
		startedAt:  time.Now(),
		cancel:     cancel,
		viewport:   vp,
	}
	s.refresh(contentW)
	return s
}

func (s *simulationState) appendEvent(ev panelEvent, contentW int) {
	s.stage = ev.stage
	s.current = ev.current
	s.total = ev.total
	if ev.err != nil {
		s.err = ev.err
	}
	s.history = append(s.history, simulationLine{
		at: ev.at, stage: ev.stage, current: ev.current, total: ev.total,
		message: ev.message, err: ev.err,
	})
	if ev.terminal() {
		s.done = true
		s.finishedAt = ev.at
	}
	s.refresh(contentW)
}

func (s *simulationState) refresh(contentW int) {
	titleStyle := lipgloss.NewStyle().Foreground(colorAccent).Bold(true)
	dimStyle := lipgloss.NewStyle().Foreground(colorDim)
	mutedStyle := lipgloss.NewStyle().Foreground(colorMuted)
	okStyle := lipgloss.NewStyle().Foreground(colorSuccess)
	errStyle := lipgloss.NewStyle().Foreground(colorError)
	stageStyle := lipgloss.NewStyle().Foreground(colorAccent2)

	var b strings.Builder
	b.WriteString(titleStyle.Render(s.title))
	b.WriteString("\n\n")
	if s.source != "" {
		b.WriteString(dimStyle.Render("来源 "))
		b.WriteString(s.source)
		b.WriteString("\n")
	}
	b.WriteString(dimStyle.Render("开始 "))
	b.WriteString(formatReportTime(s.startedAt))
	if !s.finishedAt.IsZero() {
		b.WriteString(dimStyle.Render("  完成 "))
		b.WriteString(formatReportTime(s.finishedAt))
	}
	b.WriteString("\n\n")

	b.WriteString(mutedStyle.Render("阶段 "))
	b.WriteString(stageStyle.Render(s.stage))
	if s.total > 0 {
		b.WriteString(mutedStyle.Render("  进度 "))
		b.WriteString(fmt.Sprintf("%d/%d", s.current, s.total))
	}
	b.WriteString("\n\n")

	b.WriteString(titleStyle.Render("流程日志"))
	b.WriteString(" ")
	b.WriteString(dimStyle.Render(fmt.Sprintf("(%d 条)", len(s.history))))
	b.WriteString("\n")
	for _, ln := range s.history {
		b.WriteString("\n")
		b.WriteString(dimStyle.Render(ln.at.Format("15:04:05")))
		b.WriteString(" ")
		b.WriteString(stageStyle.Render(ln.stage))
		if ln.total > 0 && ln.current > 0 {
			b.WriteString(mutedStyle.Render(fmt.Sprintf(" %d/%d", ln.current, ln.total)))
		}
		b.WriteString(" ")
		if ln.err != nil {
			b.WriteString(errStyle.Render(ln.message + " - " + ln.err.Error()))
		} else {
			b.WriteString(wrapText(ln.message, contentW))
		}
	}

	b.WriteString("\n\n")
	switch {
	case !s.done:
		b.WriteString(dimStyle.Render("Esc 取消"))
	case s.err != nil:
		b.WriteString(errStyle.Render(s.failText))
		b.WriteString("\n")
		b.WriteString(dimStyle.Render("Esc 关闭面板"))
	default:
		b.WriteString(okStyle.Render(s.doneText))
		b.WriteString("\n")
		b.WriteString(dimStyle.Render("Esc 关闭面板"))
	}

	s.viewport.SetContent(b.String())
	if !s.done {
		s.viewport.GotoBottom()
	}
}

func renderSimulationModal(width, height int, s *simulationState) string {
	if s == nil {
		return ""
	}
	boxW, boxH := reportModalSize(width, height)
	contentW := paddedModalContentWidth(boxW)
	if s.viewport.Width != contentW {
		s.viewport.Width = contentW
		s.refresh(contentW)
	}
	if s.viewport.Height != boxH-4 {
		s.viewport.Height = boxH - 4
	}
	hint := "  ↑↓ 滚动 · Esc 取消/关闭"
	modal := renderPaddedModalFrame(boxW, boxH, s.frameTitle, hint, strings.Split(s.viewport.View(), "\n"))
	return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, modal)
}

func (m Model) handleSimulationKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.simulator == nil {
		return m, nil
	}
	switch msg.Type {
	case tea.KeyEsc:
		if !m.simulator.done && m.simulator.cancel != nil {
			m.simulator.cancel()
			return m, nil
		}
		m.simulator = nil
		return m, m.textarea.Focus()
	case tea.KeyUp:
		m.simulator.viewport.ScrollUp(1)
	case tea.KeyDown:
		m.simulator.viewport.ScrollDown(1)
	case tea.KeyPgUp:
		m.simulator.viewport.HalfPageUp()
	case tea.KeyPgDown:
		m.simulator.viewport.HalfPageDown()
	}
	return m, nil
}

func startSimulate(rt *host.Host, reqID int, args []string, width, height int) (*simulationState, tea.Cmd, error) {
	if len(args) > 0 {
		// 明确说清"不接参数"：原措辞只在多打了参数时出现，等于在最不该解释的
		// 时候给出一句没有信息量的用法。
		return nil, nil, fmt.Errorf("用法：/simulate（不接参数，语料目录固定为 ./simulate）")
	}
	ctx, cancel := context.WithCancel(context.Background())
	ch, err := rt.Simulate(ctx)
	if err != nil {
		cancel()
		return nil, nil, err
	}
	state := newSimulationState(reqID, "生成仿写画像", "./simulate", width, height, cancel)
	return state, listenSimulationEvent(reqID, ch), nil
}

func startImportSimulation(rt *host.Host, reqID int, args []string, width, height int) (*simulationState, tea.Cmd, error) {
	if len(args) != 1 {
		return nil, nil, fmt.Errorf("用法：/importsim <profile.json>")
	}
	ctx, cancel := context.WithCancel(context.Background())
	ch, err := rt.ImportSimulationProfile(ctx, args[0])
	if err != nil {
		cancel()
		return nil, nil, err
	}
	state := newSimulationState(reqID, "导入仿写画像", args[0], width, height, cancel)
	return state, listenSimulationEvent(reqID, ch), nil
}
