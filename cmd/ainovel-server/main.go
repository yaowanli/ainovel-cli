// Command ainovel-server 用一个进程管理多本 AI 小说，对外提供 REST + SSE 控制面。
//
// 与上游 TUI / headless 的关系：三者共用同一套 host.Host 内核，本命令只是又一个
// 入口（entry）。因为 host.Host 的事实层是文件系统、控制面是显式方法
// （StartPrepared / Resume / Steer / Continue / SetAdvanceMode / AdvanceOneChapter /
// Abort / Snapshot）、事件面是 Channels（Events / Stream / Done），
// 所以单进程内并发跑 N 本书不需要改动引擎，只需要：
//
//  1. 每本书一个独立 OutputDir（绝对路径）——store 与 flock 租约都按目录隔离；
//  2. 每本书一个独立 host.Host + 一个专职事件泵 goroutine（Host 事件通道容量有限
//     且是"丢最旧"语义，泵必须始终就绪，不能让慢客户端反压到 Host）；
//  3. 配置按项目目录显式解析（bootstrap.LoadConfigFor），不依赖进程 cwd；
//  4. 用户规则目录按项目目录注入（host.WithUserRulesOptions），同理不依赖 cwd。
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/voocel/ainovel-cli/internal/bootstrap"
	"github.com/voocel/ainovel-cli/internal/rules"
)

func main() {
	var (
		addr      = flag.String("addr", "127.0.0.1:8787", "HTTP 监听地址")
		workspace = flag.String("workspace", "workspace", "项目根目录，每本书一个子目录")
		baseDir   = flag.String("base", ".", "所有项目共享的配置基底目录（其 ./.ainovel/config.json）")
		openAPI   = flag.Bool("openapi", true, "允许跨域访问 API（本地调试用）")
		verbose   = flag.Bool("v", false, "输出调试日志")
	)
	flag.Parse()

	level := slog.LevelInfo
	if *verbose {
		level = slog.LevelDebug
	}
	// 进程级 logger 写 stderr。注意：上游 host 的 WithFileLog 会替换进程默认 logger，
	// 多项目并发时不能逐本开启（会互相抢 stderr/文件句柄），因此本入口一律不开启，
	// 每本的可观测性由事件流 + store 落盘承担。
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level})))

	base, err := filepath.Abs(*baseDir)
	if err != nil {
		fatal("解析 base 目录失败: %v", err)
	}

	ws, err := filepath.Abs(*workspace)
	if err != nil {
		fatal("解析 workspace 失败: %v", err)
	}
	if err := os.MkdirAll(ws, 0o755); err != nil {
		fatal("创建 workspace 失败: %v", err)
	}
	rules.EnsureHomeRulesDir()

	slog.Info("共享配置基底目录", "base", base)

	reg, err := NewRegistry(ws, base)
	if err != nil {
		fatal("加载挂载表失败: %v", err)
	}
	defer reg.CloseAll()

	srv := &http.Server{
		Addr:              *addr,
		Handler:           reg.Handler(*openAPI),
		ReadHeaderTimeout: 10 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	errCh := make(chan error, 1)
	go func() {
		slog.Info("ainovel-server 启动", "addr", *addr, "workspace", ws)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	select {
	case err := <-errCh:
		fatal("HTTP 服务退出: %v", err)
	case <-ctx.Done():
		slog.Info("收到退出信号，正在关闭")
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		slog.Warn("HTTP 关闭超时", "err", err)
	}
}

func fatal(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	if p := bootstrap.WriteStartupError(fmt.Sprintf(format, args...)); p != "" {
		fmt.Fprintf(os.Stderr, "（详细错误已记录到 %s）\n", p)
	}
	os.Exit(1)
}

// writeJSON 统一 JSON 响应。DTO 已是可序列化结构，编码失败属于编程错误。
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	if v == nil {
		return
	}
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Warn("JSON 编码失败", "err", err)
	}
}

func writeErr(w http.ResponseWriter, status int, err error) {
	writeJSON(w, status, map[string]string{"error": err.Error()})
}

// decodeBody 解析请求体；空体视为 {}，让所有 POST 端点都能无体调用。
func decodeBody(r *http.Request, v any) error {
	dec := json.NewDecoder(http.MaxBytesReader(nil, r.Body, 1<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		if errors.Is(err, os.ErrClosed) || err.Error() == "EOF" {
			return nil
		}
		if strings.Contains(err.Error(), "EOF") {
			return nil
		}
		return fmt.Errorf("请求体解析失败: %w", err)
	}
	return nil
}
