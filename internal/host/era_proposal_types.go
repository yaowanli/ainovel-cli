package host

import "time"

// 时代术语候选生成的阶段。
type EraStage string

const (
	EraStageStart EraStage = "start"
	EraStageLLM   EraStage = "llm"
	EraStageDone  EraStage = "done"
	EraStageError EraStage = "error"
)

// EraProposalEvent 是候选生成的流式事件。
//
// 与 style.Event / sim.Event 同构：Host 侧 goroutine 产出，TUI 侧适配成 panelEvent。
// 不复用 TUI 的 panelEvent 是因为 Host 不能依赖 entry 包。
type EraProposalEvent struct {
	Time    time.Time
	Stage   EraStage
	Current int
	Total   int
	Message string
	Err     error
}
