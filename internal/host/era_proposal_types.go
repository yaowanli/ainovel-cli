package host

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// 时代术语候选生成的阶段。
type EraStage string

const (
	EraStageStart EraStage = "start"
	EraStageLLM   EraStage = "llm"
	EraStageRetry EraStage = "retry"
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

// errNoProposals 表示模型没有给出任何候选（区别于调用失败）。
var errNoProposals = errors.New("模型未给出任何候选；可换一个更明确的朝代名称重试")

// runWatchdoged 执行 work，并保证在 maxWait 之内返回，无论 work 是否还在跑。
//
// 为什么需要它：context.WithTimeout 只在「取消感知点」生效，而 provider 的在途
// HTTP 请求未必响应 ctx——实测一次候选生成声明 3 分钟超时，实际等了近 17 分钟才
// 返回，中途界面一直显示未到期的超时提示。把「何时告诉用户结束了」与「调用何时
// 真正返回」解耦，超时才对用户真的是个上限。
//
// work 超过 maxWait 未返回时，本函数返回超时错误并对 ctx 发取消（给 work 一个
// 收尾的机会），但**不等待**它。此时 work 的 goroutine 可能仍在后台跑并最终被
// 丢弃——这是有意的取舍：宁可泄漏一次调用，也不能让界面无限期停住。
func runWatchdoged(maxWait time.Duration, work func(ctx context.Context) error) error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	res := make(chan error, 1)
	go func() {
		defer func() {
			// work 内部 panic 不应让整个 TUI 崩掉，按失败处理即可。
			if r := recover(); r != nil {
				res <- fmt.Errorf("候选生成异常：%v", r)
			}
		}()
		res <- work(ctx)
	}()

	timer := time.NewTimer(maxWait)
	defer timer.Stop()
	select {
	case err := <-res:
		return err
	case <-timer.C:
		cancel()
		return fmt.Errorf("超过 %s 仍未返回，已停止等待。\\n"+
			"本次调用可能仍在服务端继续；如需重试请稍候。\\n"+
			"若反复超时，可在 /config 把 reasoning_effort 调低后重试。", maxWait)
	}
}
