package host

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/voocel/ainovel-cli/internal/store"
)

// 核心回归：work 永不返回时，runWatchdoged 仍必须在 maxWait 附近返回。
//
// 这是线上真实故障：声明 3 分钟超时，实际等了近 17 分钟——因为
// context.WithTimeout 只在取消感知点生效，而在途 provider 请求不响应 ctx。
// 没有看门狗时，用户会对着一个早已过期的超时提示继续干等。
func TestRunWatchdogedReturnsWhileWorkStuck(t *testing.T) {
	release := make(chan struct{})
	defer close(release)

	start := time.Now()
	err := runWatchdoged(250*time.Millisecond, func(ctx context.Context) error {
		<-release // 彻底忽略 ctx，就像卡住的 HTTP 请求
		return nil
	})
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("work 未返回时应给出超时错误")
	}
	if elapsed > 3*time.Second {
		t.Fatalf("应在超时附近返回，实际耗时 %s", elapsed)
	}
	msg := err.Error()
	for _, want := range []string{"停止等待", "reasoning_effort", "/config"} {
		if !strings.Contains(msg, want) {
			t.Errorf("超时说明应含 %q（用户要知道下一步做什么），实际 %q", want, msg)
		}
	}
}

// work 正常返回时必须原样透传结果，不能被超时逻辑改写。
func TestRunWatchdogedPassesThroughResult(t *testing.T) {
	sentinel := errors.New("provider 拒绝")
	err := runWatchdoged(5*time.Second, func(ctx context.Context) error {
		return sentinel
	})
	if !errors.Is(err, sentinel) {
		t.Fatalf("应透传原始错误，实际 %v", err)
	}
}

// work 返回 nil 时不应被误判为超时。
func TestRunWatchdogedNilIsSuccess(t *testing.T) {
	if err := runWatchdoged(5*time.Second, func(ctx context.Context) error { return nil }); err != nil {
		t.Fatalf("work 成功时不应报错，实际 %v", err)
	}
}

// 快于超时的调用必须真的等它跑完——看门狗不能变成「到点就跑」。
func TestRunWatchdogedWaitsForFastWork(t *testing.T) {
	done := false
	err := runWatchdoged(5*time.Second, func(ctx context.Context) error {
		time.Sleep(120 * time.Millisecond)
		done = true
		return nil
	})
	if err != nil {
		t.Fatalf("不应超时: %v", err)
	}
	if !done {
		t.Error("应等 work 执行完再返回")
	}
}

// work 内部 panic 不应掀翻 TUI。
func TestRunWatchdogedRecoversPanic(t *testing.T) {
	err := runWatchdoged(5*time.Second, func(ctx context.Context) error {
		panic("provider 炸了")
	})
	if err == nil {
		t.Fatal("panic 应转为错误返回")
	}
	if !strings.Contains(err.Error(), "异常") {
		t.Errorf("错误应说明是异常，实际 %q", err.Error())
	}
}

// 超时后 work 收到取消信号（给收尾机会），虽然本实现不等待它。
func TestRunWatchdogedCancelsWorkOnTimeout(t *testing.T) {
	cancelled := make(chan struct{})
	_ = runWatchdoged(200*time.Millisecond, func(ctx context.Context) error {
		<-ctx.Done()
		close(cancelled)
		return ctx.Err()
	})
	select {
	case <-cancelled:
	case <-time.After(2 * time.Second):
		t.Error("超时时未向 work 发出取消")
	}
}

// 超时后不得重复发终态：面板若先收到 error 又被迟到的 done 覆盖，
// 用户会看到自相矛盾的结论。
func TestEraProposeEmitsSingleTerminalEvent(t *testing.T) {
	old := eraProposeTimeout
	eraProposeTimeout = 200 * time.Millisecond
	t.Cleanup(func() { eraProposeTimeout = old })

	h := &Host{
		store:  store.NewStore(t.TempDir()),
		engine: &engine{},
		events: make(chan Event, 32),
	}
	ch, err := h.GenerateEraProposalsAsync(context.Background(), "东汉末年")
	if err != nil {
		t.Fatalf("启动失败: %v", err)
	}
	terminals := 0
	timeout := time.After(5 * time.Second)
loop:
	for {
		select {
		case ev, ok := <-ch:
			if !ok {
				break loop
			}
			if ev.Stage == EraStageDone || ev.Stage == EraStageError {
				terminals++
			}
		case <-timeout:
			break loop
		}
	}
	if terminals != 1 {
		t.Fatalf("终态事件应恰好 1 条，实际 %d", terminals)
	}
}

// 超时后必须释放独占槽，否则后续命令会被「进行中」挡住。
func TestEraProposeReleasesExclusiveAfterTimeout(t *testing.T) {
	old := eraProposeTimeout
	eraProposeTimeout = 150 * time.Millisecond
	t.Cleanup(func() { eraProposeTimeout = old })

	h := &Host{
		store:  store.NewStore(t.TempDir()),
		engine: &engine{},
		events: make(chan Event, 32),
	}
	ch, err := h.GenerateEraProposalsAsync(context.Background(), "东汉末年")
	if err != nil {
		t.Fatalf("启动失败: %v", err)
	}
	for range ch { // 等通道关闭
	}
	h.mu.Lock()
	busy := h.exclusive
	h.mu.Unlock()
	if busy != "" {
		t.Errorf("超时后独占槽应释放，实际仍被 %q 占用", busy)
	}
}
