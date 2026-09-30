package main

import (
	"encoding/json"
	"time"

	"github.com/voocel/ainovel-cli/internal/host"
)

// SnapshotDTO 是项目在总览列表与详情页共用的状态投影。
type SnapshotDTO struct {
	ID              string         `json:"id"`
	Dir             string         `json:"dir"`
	OutputDir       string         `json:"output_dir"`
	State           string         `json:"state"`
	Title           string         `json:"title"`
	Synopsis        string         `json:"synopsis"`
	Phase           string         `json:"phase"`
	Flow            string         `json:"flow"`
	Completed       int            `json:"completed"`
	TotalChapters   int            `json:"total_chapters"`
	InProgress      int            `json:"in_progress"`
	PendingRewrites []int          `json:"pending_rewrites"`
	AdvanceMode     string         `json:"advance_mode"`
	CostUSD         float64        `json:"cost_usd"`
	Opened          time.Time      `json:"opened,omitzero"`
	Error           string         `json:"error,omitempty"`
	Snapshot        *UISnapshotDTO `json:"live,omitempty"`
}

// UISnapshotDTO 映射 host.UISnapshot，只暴露前端用得到的字段。
type UISnapshotDTO struct {
	Title              string                 `json:"title"`
	Synopsis           string                 `json:"synopsis"`
	Provider           string                 `json:"provider"`
	Model              string                 `json:"model"`
	ContextWindow      int                    `json:"context_window"`
	Thinking           string                 `json:"thinking"`
	Style              string                 `json:"style"`
	RuntimeState       string                 `json:"runtime_state"`
	StatusLabel        string                 `json:"status_label"`
	Phase              string                 `json:"phase"`
	Flow               string                 `json:"flow"`
	Completed          int                    `json:"completed"`
	TotalChapters      int                    `json:"total_chapters"`
	CurrentChapter     int                    `json:"current_chapter"`
	InProgress         int                    `json:"in_progress"`
	Words              int                    `json:"words"`
	PendingRewrites    []int                  `json:"pending_rewrites"`
	PendingSteer       string                 `json:"pending_steer"`
	AdvanceMode        string                 `json:"advance_mode"`
	AdvanceHold        bool                   `json:"advance_hold"`
	AdvanceHoldReason  string                 `json:"advance_hold_reason"`
	IsRunning          bool                   `json:"is_running"`
	Agents             []host.AgentSnapshot   `json:"agents"`
	TotalCostUSD       float64                `json:"total_cost_usd"`
	TotalInputTokens   int                    `json:"total_input_tokens"`
	TotalOutputTokens  int                    `json:"total_output_tokens"`
	CacheReadTokens    int                    `json:"cache_read_tokens"`
	BudgetLimitUSD     float64                `json:"budget_limit_usd"`
	CacheCapable       bool                   `json:"cache_capable"`
	MissingUsage       int                    `json:"missing_assistant_usage"`
	Compass            string                 `json:"compass"`
	CurrentVolumeArc   string                 `json:"current_volume_arc"`
	Premise            string                 `json:"premise"`
	LastCommitSummary  string                 `json:"last_commit_summary"`
	LastReviewSummary  string                 `json:"last_review_summary"`
	LastCheckpointName string                 `json:"last_checkpoint_name"`
	Characters         []string               `json:"characters"`
	Outline            []host.OutlineSnapshot `json:"outline"`
	RecentSummaries    []string               `json:"recent_summaries"`
}

// EventDTO 是 SSE 事件负载。ID 用于浏览器按调用生命周期原地更新，
// Running 表示该调用仍在进行中（与 host.Event.Running 语义一致）。
type EventPayload struct {
	ID         string    `json:"id,omitempty"`
	Time       time.Time `json:"time"`
	FinishedAt time.Time `json:"finished_at,omitzero"`
	Finished   bool      `json:"finished"`
	Running    bool      `json:"running"`
	Failed     bool      `json:"failed"`
	Category   string    `json:"category"`
	Agent      string    `json:"agent,omitempty"`
	Level      string    `json:"level,omitempty"`
	Summary    string    `json:"summary"`
	Detail     string    `json:"detail,omitempty"`
	Kind       string    `json:"kind,omitempty"`
	Depth      int       `json:"depth,omitempty"`
	DurationMS int64     `json:"duration_ms,omitempty"`
}

func NewEventPayload(ev host.Event) EventPayload {
	dto := EventPayload{
		ID:         ev.ID,
		Time:       ev.Time,
		FinishedAt: ev.FinishedAt,
		Finished:   !ev.FinishedAt.IsZero(),
		Running:    ev.Running(),
		Failed:     ev.Failed,
		Category:   ev.Category,
		Agent:      ev.Agent,
		Level:      ev.Level,
		Summary:    ev.Summary,
		Detail:     ev.Detail,
		Kind:       ev.Kind,
		Depth:      ev.Depth,
	}
	if ev.Duration > 0 {
		dto.DurationMS = ev.Duration.Milliseconds()
	}
	return dto
}

func jsonIndent(v any) ([]byte, error) {
	return json.MarshalIndent(v, "", "  ")
}
