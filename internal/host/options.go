package host

import (
	"log/slog"

	"github.com/voocel/ainovel-cli/internal/rules"
)

type newOptions struct {
	logFile       string
	logAlsoStderr bool
	logAttrs      []slog.Attr
	rulesOptions  *rules.LoadOptions
}

// NewOption 配置 Host 构造过程，运行时资源仍由 Host 持有。
type NewOption func(*newOptions)

// WithFileLog 让 Host 持有一个运行时日志会话。日志只在取得小说目录租约后打开，
// 并在 Host 的所有关闭日志完成后关闭。打开失败时继续使用当前进程 logger，
// 调用方必须通过 FileLogError 显式处理该错误。
func WithFileLog(filename string, alsoStderr bool, attrs ...slog.Attr) NewOption {
	return func(opts *newOptions) {
		opts.logFile = filename
		opts.logAlsoStderr = alsoStderr
		opts.logAttrs = append([]slog.Attr(nil), attrs...)
	}
}

// WithUserRulesOptions 指定用户规则来源目录，替代默认的"当前工作目录"推导。
//
// 默认值 rules.DefaultOptions() 绑定进程 cwd，单项目 CLI 场景正确；但一个进程同时
// 管理多本书时（N 个 Host），cwd 只有一个，必须由调用方按项目目录显式传入，
// 否则所有书都会去读服务端进程 cwd 下的 ./.ainovel/rules/。
func WithUserRulesOptions(opts rules.LoadOptions) NewOption {
	return func(no *newOptions) {
		no.rulesOptions = &opts
	}
}
