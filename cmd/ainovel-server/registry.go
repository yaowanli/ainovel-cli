package main

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/voocel/ainovel-cli/assets"
	"github.com/voocel/ainovel-cli/internal/bootstrap"
	"github.com/voocel/ainovel-cli/internal/domain"
	"github.com/voocel/ainovel-cli/internal/host"
	"github.com/voocel/ainovel-cli/internal/rules"
	storepkg "github.com/voocel/ainovel-cli/internal/store"
)

// Registry 管理 workspace 下的全部项目，进程内并发驱动多本小说。
type Registry struct {
	root string
	// base 是所有项目共享的配置基底目录（通常是服务端 cwd）。
	// 凭证与默认模型写在这里，各项目只覆盖自己关心的字段。
	base string

	mu       sync.RWMutex
	projects map[string]*Project

	hub *Hub // 全局事件总线（项目列表页订阅这一路即可）
}

func NewRegistry(root, base string) *Registry {
	return &Registry{root: root, base: base, projects: map[string]*Project{}, hub: NewHub()}
}

// Workspace 返回项目根目录。
func (r *Registry) Workspace() string { return r.root }

// attach 把项目登记进内存表：已存在则复用既有实例（Host / 事件泵 / 运行状态都挂在
// 实例上），否则新建并登记。list / get / create 全部经过它——否则同一目录会在每次
// 请求时造出一个新 Project，丢掉运行状态，还会与真正的 Host 抢同一把 flock 租约。
func (r *Registry) attach(p *Project) *Project {
	r.mu.Lock()
	defer r.mu.Unlock()
	if exist, ok := r.projects[p.ID]; ok {
		return exist
	}
	p.global = r.hub
	r.projects[p.ID] = p
	return p
}

// lookup 只查内存表，不触发磁盘扫描。
func (r *Registry) lookup(id string) (*Project, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	p, ok := r.projects[id]
	return p, ok
}

// list 扫描 workspace 下的子目录，并上内存表中已登记的项目。
func (r *Registry) list() []*Project {
	entries, err := os.ReadDir(r.root)
	if err != nil {
		slog.Warn("扫描 workspace 失败", "root", r.root, "err", err)
	}
	ids := map[string]bool{}
	for _, e := range entries {
		if e.IsDir() && !strings.HasPrefix(e.Name(), ".") {
			ids[e.Name()] = true
		}
	}
	r.mu.RLock()
	for id := range r.projects {
		ids[id] = true
	}
	r.mu.RUnlock()

	out := make([]*Project, 0, len(ids))
	for id := range ids {
		out = append(out, r.attach(NewProject(id, filepath.Join(r.root, id), r.base)))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// get 返回指定项目；磁盘与内存都没有时报错。
func (r *Registry) get(id string) (*Project, error) {
	if p, ok := r.lookup(id); ok {
		return p, nil
	}
	for _, p := range r.list() {
		if p.ID == id {
			return p, nil
		}
	}
	return nil, fmt.Errorf("项目 %q 不存在", id)
}

// create 新建项目目录并初始化项目级配置（骨架继承当前有效配置）。
func (r *Registry) create(id string, overrides map[string]any) (*Project, error) {
	id = strings.TrimSpace(id)
	if err := validateID(id); err != nil {
		return nil, err
	}
	dir := filepath.Join(r.root, id)
	if _, err := os.Stat(dir); err == nil {
		return nil, fmt.Errorf("项目 %q 已存在", id)
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Join(dir, ".ainovel"), 0o755); err != nil {
		return nil, err
	}

	// 骨架继承"全局 + 服务端 baseDir"的有效配置（拿到 provider 凭证），再叠加本项目
	// 覆盖。只写 provider/model/style 等选择项——凭证留在服务端配置层，
	// 避免每本书的目录里都躺一份 API Key。
	base, err := bootstrap.LoadConfigForLayers(r.base, "")
	if err != nil {
		return nil, err
	}
	skeleton := map[string]any{}
	if base.Provider != "" {
		skeleton["provider"] = base.Provider
	}
	if base.ModelName != "" {
		skeleton["model"] = base.ModelName
	}
	if base.ReasoningEffort != "" {
		skeleton["reasoning_effort"] = base.ReasoningEffort
	}
	if base.Style != "" {
		skeleton["style"] = base.Style
	}
	if base.ContextWindow > 0 {
		skeleton["context_window"] = base.ContextWindow
	}
	for k, v := range overrides {
		if v == nil {
			continue
		}
		skeleton[k] = v
	}
	if err := writeJSONFile(filepath.Join(dir, ".ainovel", "config.json"), skeleton); err != nil {
		return nil, err
	}
	return r.attach(NewProject(id, dir, r.base)), nil
}

func validateID(id string) error {
	if id == "" {
		return errors.New("项目 id 不能为空")
	}
	if len(id) > 64 {
		return errors.New("项目 id 过长（上限 64）")
	}
	for _, r := range id {
		ok := r == '-' || r == '_' ||
			(r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')
		if !ok {
			return fmt.Errorf("项目 id 只能包含字母、数字、- 和 _（非法字符 %q）", r)
		}
	}
	return nil
}

// CloseAll 关闭全部已打开项目（释放 flock 租约）。
func (r *Registry) CloseAll() {
	r.mu.Lock()
	ps := make([]*Project, 0, len(r.projects))
	for _, p := range r.projects {
		ps = append(ps, p)
	}
	r.projects = map[string]*Project{}
	r.mu.Unlock()
	for _, p := range ps {
		p.Close()
	}
}

// ── Project ──

// runState 是项目在 Server 视角下的生命周期，与 host 内部 lifecycle 解耦。
type runState string

const (
	stateClosed  runState = "closed"  // 未在进程内打开
	stateIdle    runState = "idle"    // 已打开，未运行
	stateRunning runState = "running" // 引擎运行中
	statePaused  runState = "paused"  // 运行过，当前停机等待继续
	stateDone    runState = "done"    // 本轮已结束（完本或停止）
)

// Project 是"一本小说"的服务端封装：目录 + 配置 + 可选的 host.Host + 事件总线。
type Project struct {
	ID   string
	Dir  string
	base string // 共享配置基底目录

	mu      sync.Mutex // 串行化控制面调用（start/steer/next/abort/model 切换）
	host    *host.Host
	cfg     bootstrap.Config
	hub     *Hub
	global  *Hub // 全局总线，供项目列表页订阅
	state   runState
	lastErr string
	opened  time.Time
	pumped  bool
}

func NewProject(id, dir, base string) *Project {
	return &Project{ID: id, Dir: dir, base: base, state: stateClosed, hub: NewHub()}
}

// Hub 暴露本项目的事件总线。
func (p *Project) Hub() *Hub { return p.hub }

// OutputDir 是本书的 store 根目录（与 TUI 语义一致：<项目目录>/output/novel）。
func (p *Project) OutputDir() string { return filepath.Join(p.Dir, "output", "novel") }

// open 打开项目：解析配置 → 加载文风资产 → 构造 Host。幂等。
//
// 不使用 host.WithFileLog：上游该选项会替换进程级 slog 默认 logger，多项目并发时
// 后开的书会把先开的书的日志抢走，且 Close 时恢复的是过期 logger。每本的可观测性
// 由事件流（→ SSE）与 store 落盘承担。
func (p *Project) open() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.openLocked()
}

func (p *Project) openLocked() error {
	if p.host != nil {
		return nil
	}
	cfg, err := bootstrap.LoadConfigForLayers(p.base, p.Dir)
	if err != nil {
		return err
	}
	cfg.OutputDir = p.OutputDir()
	cfg.FillDefaults()
	if err := os.MkdirAll(cfg.OutputDir, 0o755); err != nil {
		return fmt.Errorf("创建小说目录失败: %w", err)
	}
	bundle := assets.Load(cfg.Style, assets.DefaultLoadOptions(cfg.OutputDir))

	// 规则来源按项目目录注入，不依赖进程 cwd（一个进程 N 本书时 cwd 只有一个）。
	ruleOpts := rules.LoadOptions{
		HomeRulesDir:    rules.DefaultHomeRulesDir(),
		ProjectRulesDir: rules.DefaultProjectRulesDir(p.Dir),
	}
	h, err := host.New(cfg, bundle, host.WithUserRulesOptions(ruleOpts))
	if err != nil {
		p.lastErr = err.Error()
		return err
	}
	p.host = h
	p.cfg = cfg
	p.opened = time.Now()
	if !p.pumped {
		p.pumped = true
		go p.pump(h)
	}
	p.publishState()
	slog.Info("项目已打开", "id", p.ID, "dir", p.Dir, "output", cfg.OutputDir,
		"provider", cfg.Provider, "model", cfg.ModelName)
	return nil
}

func (p *Project) Close() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.host == nil {
		return
	}
	p.host.Close()
	p.host = nil
	p.state = stateClosed
	p.publishState()
}

// withHost 打开项目并在控制面锁内执行 fn。
func (p *Project) withHost(fn func(h *host.Host) error) error {
	_, err := p.withHostValue(func(h *host.Host) (any, error) { return nil, fn(h) })
	return err
}

// withHostValue 同 withHost，但允许 fn 返回业务结果。
func (p *Project) withHostValue(fn func(h *host.Host) (any, error)) (any, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := p.openLocked(); err != nil {
		return nil, err
	}
	return fn(p.host)
}

// pump 是本项目唯一的事件消费者：Host 的 Events/Stream 通道是"丢最旧"语义，
// 必须有人一直就绪；本泵再把事件扇出给各 SSE 订阅者（订阅者慢只丢自己的数据）。
func (p *Project) pump(h *host.Host) {
	events, stream, done := h.Events(), h.Stream(), h.Done()
	for events != nil || stream != nil || done != nil {
		select {
		case ev, ok := <-events:
			if !ok {
				events = nil
				continue
			}
			p.hub.Publish(Message{Type: "event", Data: NewEventPayload(ev)})
		case delta, ok := <-stream:
			if !ok {
				stream = nil
				continue
			}
			if delta == host.StreamClearSentinel {
				p.hub.Publish(Message{Type: "clear"})
				continue
			}
			p.hub.Publish(Message{Type: "delta", Data: delta})
		case _, ok := <-done:
			if !ok {
				done = nil
				continue
			}
			p.markRunEnded()
		}
	}
}

func (p *Project) markRunEnded() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.state == stateRunning {
		p.state = stateDone
	}
	p.publishState()
}

func (p *Project) setErr(err error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if err != nil {
		p.lastErr = err.Error()
		p.hub.Publish(Message{Type: "error", Data: map[string]string{"error": p.lastErr}})
		if p.global != nil {
			p.global.Publish(Message{Type: "error", Data: map[string]string{"error": p.lastErr}})
		}
		return
	}
	p.lastErr = ""
}

// publishState 在持锁状态下广播最新快照，供 SSE 客户端刷新总览。
func (p *Project) publishState() {
	snap := p.snapshotLocked()
	p.hub.Publish(Message{Type: "snapshot", Data: snap})
	if p.global != nil {
		p.global.Publish(Message{Type: "snapshot", Data: snap})
	}
}

// snapshotLocked 汇总项目状态。host 未打开时只读 store 里的静态事实（离线可用）。
func (p *Project) snapshotLocked() SnapshotDTO {
	dto := SnapshotDTO{
		ID:        p.ID,
		Dir:       p.Dir,
		OutputDir: p.OutputDir(),
		State:     string(p.state),
		Opened:    p.opened,
		Error:     p.lastErr,
	}

	st := storepkg.NewStore(p.OutputDir())
	if prog, err := st.Progress.Load(); err == nil && prog != nil {
		dto.Phase = string(prog.Phase)
		dto.Flow = string(prog.Flow)
		dto.Completed = len(prog.CompletedChapters)
		dto.InProgress = prog.InProgressChapter
		dto.PendingRewrites = prog.PendingRewrites
	}
	if book, err := st.Book.Load(); err == nil && book != nil {
		dto.Title = book.Title
		dto.Synopsis = book.Synopsis
	}
	if meta, err := st.RunMeta.Load(); err == nil && meta != nil {
		dto.AdvanceMode = string(meta.AdvanceMode)
	}

	if p.host == nil {
		return dto
	}

	ui := p.host.Snapshot()
	dto.Snapshot = &UISnapshotDTO{
		Title:              ui.BookTitle,
		Synopsis:           ui.Synopsis,
		Provider:           ui.Provider,
		Model:              ui.ModelName,
		ContextWindow:      ui.ModelContextWindow,
		Thinking:           ui.ThinkingLevel,
		Style:              ui.Style,
		RuntimeState:       ui.RuntimeState,
		StatusLabel:        ui.StatusLabel,
		Phase:              ui.Phase,
		Flow:               ui.Flow,
		Completed:          ui.CompletedCount,
		TotalChapters:      ui.TotalChapters,
		CurrentChapter:     ui.CurrentChapter,
		InProgress:         ui.InProgressChapter,
		Words:              ui.TotalWordCount,
		PendingRewrites:    ui.PendingRewrites,
		PendingSteer:       ui.PendingSteer,
		AdvanceMode:        ui.AdvanceMode,
		AdvanceHold:        ui.HasAdvanceHold,
		AdvanceHoldReason:  ui.AdvanceHoldReason,
		IsRunning:          ui.IsRunning,
		Agents:             ui.Agents,
		TotalCostUSD:       ui.TotalCostUSD,
		TotalInputTokens:   ui.TotalInputTokens,
		TotalOutputTokens:  ui.TotalOutputTokens,
		CacheReadTokens:    ui.TotalCacheReadTokens,
		BudgetLimitUSD:     ui.BudgetLimitUSD,
		CacheCapable:       ui.OverallCacheCapable,
		MissingUsage:       ui.MissingAssistantUsage,
		Compass:            ui.CompassDirection,
		CurrentVolumeArc:   ui.CurrentVolumeArc,
		Premise:            ui.Premise,
		LastCommitSummary:  ui.LastCommitSummary,
		LastReviewSummary:  ui.LastReviewSummary,
		LastCheckpointName: ui.LastCheckpointName,
		Characters:         ui.Characters,
		Outline:            ui.Outline,
		RecentSummaries:    ui.RecentSummaries,
	}
	if ui.Phase != "" {
		dto.Phase = ui.Phase
	}
	if ui.Flow != "" {
		dto.Flow = ui.Flow
	}
	if ui.BookTitle != "" {
		dto.Title = ui.BookTitle
	}
	if ui.AdvanceMode != "" {
		dto.AdvanceMode = ui.AdvanceMode
	}
	if ui.CompletedCount > 0 {
		dto.Completed = ui.CompletedCount
	}
	dto.CostUSD = ui.TotalCostUSD
	dto.TotalChapters = ui.TotalChapters
	return dto
}

// Snapshot 返回项目状态快照。
func (p *Project) Snapshot() SnapshotDTO {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.snapshotLocked()
}

// ── 控制面操作 ──

// Start 以一句话需求开新书并立即启动 Engine。
func (p *Project) Start(prompt string) error {
	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		return errors.New("创作需求不能为空")
	}
	return p.withHost(func(h *host.Host) error {
		if err := h.PrepareUserRules(prompt); err != nil {
			return err
		}
		if err := h.StartPrepared(prompt); err != nil {
			return err
		}
		p.state = stateRunning
		p.publishState()
		return nil
	})
}

// Resume 从最近 checkpoint 恢复。
func (p *Project) Resume() error {
	return p.withHost(func(h *host.Host) error {
		label, err := h.Resume()
		if err != nil {
			return err
		}
		if label == "" {
			return errors.New("没有可恢复的会话")
		}
		p.state = stateRunning
		p.publishState()
		return nil
	})
}

// Steer 提交实时干预（运行中可用，不需要暂停）。
func (p *Project) Steer(text string) error {
	text = strings.TrimSpace(text)
	if text == "" {
		return errors.New("干预内容不能为空")
	}
	return p.withHost(func(h *host.Host) error {
		if err := h.Steer(text); err != nil {
			return err
		}
		p.state = stateRunning
		p.publishState()
		return nil
	})
}

// Continue 停机后输入继续指令。
func (p *Project) Continue(text string) error {
	text = strings.TrimSpace(text)
	if text == "" {
		return errors.New("继续指令不能为空")
	}
	return p.withHost(func(h *host.Host) error {
		if err := h.Continue(text); err != nil {
			return err
		}
		p.state = stateRunning
		p.publishState()
		return nil
	})
}

// Abort 手动暂停当前引擎循环。
func (p *Project) Abort() error {
	return p.withHost(func(h *host.Host) error {
		if !h.Abort() {
			return errors.New("当前没有正在运行的创作")
		}
		p.state = statePaused
		p.publishState()
		return nil
	})
}

// SetAdvanceMode 切换自动推进 / 逐章验收。
func (p *Project) SetAdvanceMode(review bool) error {
	mode := domain.ChapterAdvanceAuto
	if review {
		mode = domain.ChapterAdvanceReview
	}
	return p.withHost(func(h *host.Host) error { return h.SetAdvanceMode(mode) })
}

// Next 逐章验收模式下放行一章。
func (p *Project) Next() error {
	return p.withHost(func(h *host.Host) error {
		if err := h.AdvanceOneChapter(); err != nil {
			return err
		}
		p.state = stateRunning
		p.publishState()
		return nil
	})
}

// SetModel 切换角色使用的 provider / model。
func (p *Project) SetModel(role, provider, model string) error {
	return p.withHost(func(h *host.Host) error { return h.SwitchModel(role, provider, model) })
}

// SetThinking 调整角色推理强度。
func (p *Project) SetThinking(role, level string) error {
	return p.withHost(func(h *host.Host) error { return h.SetRoleThinking(role, level) })
}

// Chapter 读取已完成章节正文。
func (p *Project) Chapter(n int) (string, error) {
	if n <= 0 {
		return "", errors.New("章节号必须为正整数")
	}
	rec, err := storepkg.NewStore(p.OutputDir()).ChapterRecords.Load(n)
	if err != nil {
		return "", err
	}
	if rec == nil {
		return "", fmt.Errorf("第 %d 章尚无已接纳正文", n)
	}
	return rec.Content, nil
}

// SyncCheck 列出被手工改动过、需要 /sync 接纳的章节。
func (p *Project) SyncCheck() ([]int, error) {
	v, err := p.withHostValue(func(h *host.Host) (any, error) { return h.CheckChapterRevisions() })
	if err != nil {
		return nil, err
	}
	chapters, _ := v.([]int)
	return chapters, nil
}

func writeJSONFile(path string, v any) error {
	data, err := jsonIndent(v)
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
