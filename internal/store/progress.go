package store

import (
	"fmt"
	"os"
	"slices"
	"time"

	"github.com/voocel/ainovel-cli/internal/domain"
	"github.com/voocel/ainovel-cli/internal/errs"
)

// ProgressStore 管理创作进度状态。
type ProgressStore struct {
	io *IO
	// reworkHook 只在 StartReworkPass/ApplyReviewOutcome 的锁外调用，读写无需额外加锁：
	// 挂载发生在 Host 构造期，早于任何返工 pass。
	reworkHook ReworkHook
}

func NewProgressStore(io *IO) *ProgressStore { return &ProgressStore{io: io} }

// Load 读取 meta/progress.json。不存在时返回 nil。
func (s *ProgressStore) Load() (*domain.Progress, error) {
	s.io.mu.RLock()
	defer s.io.mu.RUnlock()
	return s.loadUnlocked()
}

func (s *ProgressStore) loadUnlocked() (*domain.Progress, error) {
	var p domain.Progress
	if err := s.io.ReadJSONUnlocked("meta/progress.json", &p); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	return &p, nil
}

// Save 保存进度。
func (s *ProgressStore) Save(p *domain.Progress) error {
	s.io.mu.Lock()
	defer s.io.mu.Unlock()
	return s.saveUnlocked(p)
}

func (s *ProgressStore) saveUnlocked(p *domain.Progress) error {
	return s.io.WriteJSONUnlocked("meta/progress.json", p)
}

// Init 创建初始进度。
func (s *ProgressStore) Init(totalChapters int) error {
	return s.Save(&domain.Progress{
		Phase:         domain.PhaseInit,
		TotalChapters: totalChapters,
	})
}

// SetTotalChapters 更新大纲容量：非分层模式为详细章数，分层模式为内部估算。
func (s *ProgressStore) SetTotalChapters(n int) error {
	return s.io.WithWriteLock(func() error {
		p, err := s.loadUnlocked()
		if err != nil {
			return err
		}
		if p == nil {
			p = &domain.Progress{}
		}
		p.TotalChapters = n
		return s.saveUnlocked(p)
	})
}

// UpdatePhase 更新创作阶段。
func (s *ProgressStore) UpdatePhase(phase domain.Phase) error {
	return s.io.WithWriteLock(func() error {
		p, err := s.loadUnlocked()
		if err != nil {
			return err
		}
		if p == nil {
			p = &domain.Progress{}
		}
		if err := domain.ValidatePhaseTransition(p.Phase, phase); err != nil {
			return err
		}
		p.Phase = phase
		return s.saveUnlocked(p)
	})
}

// AdvancePhase 将创作阶段至少推进到 phase；已经到达更后阶段时保持不变。
// 适用于可重复保存的阶段工件，避免修订旧工件被误判为阶段回退。
func (s *ProgressStore) AdvancePhase(phase domain.Phase) error {
	return s.io.WithWriteLock(func() error {
		p, err := s.loadUnlocked()
		if err != nil {
			return err
		}
		if p == nil {
			p = &domain.Progress{}
		}
		if domain.CanTransitionPhase(phase, p.Phase) {
			return nil
		}
		if err := domain.ValidatePhaseTransition(p.Phase, phase); err != nil {
			return err
		}
		p.Phase = phase
		return s.saveUnlocked(p)
	})
}

// StartChapter 标记某章进入写作中状态。它不能承担阶段迁移职责；调用方必须先由
// foundation/import 流程把 Progress 明确推进到 writing，避免错误派单绕过规划阶段。
func (s *ProgressStore) StartChapter(chapter int) error {
	if chapter <= 0 {
		return fmt.Errorf("chapter must be > 0")
	}
	return s.io.WithWriteLock(func() error {
		p, err := s.loadUnlocked()
		if err != nil {
			return err
		}
		if p == nil {
			return fmt.Errorf("progress 未初始化: %w", errs.ErrToolPrecondition)
		}
		if p.Phase != domain.PhaseWriting {
			return fmt.Errorf("章节写作仅允许在 writing 阶段（当前 phase=%s）: %w", p.Phase, errs.ErrToolPrecondition)
		}
		if p.Flow != domain.FlowRewriting && p.Flow != domain.FlowPolishing {
			p.Flow = domain.FlowWriting
		}
		if p.CurrentChapter < chapter {
			p.CurrentChapter = chapter
		}
		p.InProgressChapter = chapter
		p.CompletedScenes = nil
		return s.saveUnlocked(p)
	})
}

// IsChapterCompleted 检查章节是否已提交完成。读取失败显式返回，不能把损坏的
// progress 当成“未完成”后继续覆盖章节。
func (s *ProgressStore) IsChapterCompleted(chapter int) (bool, error) {
	p, err := s.Load()
	if err != nil {
		return false, err
	}
	if p == nil {
		return false, nil
	}
	return slices.Contains(p.CompletedChapters, chapter), nil
}

// MarkChapterComplete 标记章节完成，原子性更新进度。
func (s *ProgressStore) MarkChapterComplete(chapter, wordCount int, hookType, dominantStrand string) error {
	return s.io.WithWriteLock(func() error {
		p, err := s.loadUnlocked()
		if err != nil {
			return err
		}
		if p == nil {
			return fmt.Errorf("progress not initialized, call Init first")
		}
		if p.ChapterWordCounts == nil {
			p.ChapterWordCounts = make(map[int]int)
		}
		if oldWC, ok := p.ChapterWordCounts[chapter]; ok {
			p.TotalWordCount -= oldWC
		}
		p.ChapterWordCounts[chapter] = wordCount
		p.TotalWordCount += wordCount
		if !slices.Contains(p.CompletedChapters, chapter) {
			p.CompletedChapters = append(p.CompletedChapters, chapter)
		}
		if chapter+1 > p.CurrentChapter {
			p.CurrentChapter = chapter + 1
		}
		p.InProgressChapter = 0
		p.CompletedScenes = nil
		if err := domain.ValidatePhaseTransition(p.Phase, domain.PhaseWriting); err != nil {
			return err
		}
		p.Phase = domain.PhaseWriting

		if dominantStrand != "" {
			for len(p.StrandHistory) < chapter-1 {
				p.StrandHistory = append(p.StrandHistory, "")
			}
			if len(p.StrandHistory) < chapter {
				p.StrandHistory = append(p.StrandHistory, dominantStrand)
			} else {
				p.StrandHistory[chapter-1] = dominantStrand
			}
		}
		if hookType != "" {
			for len(p.HookHistory) < chapter-1 {
				p.HookHistory = append(p.HookHistory, "")
			}
			if len(p.HookHistory) < chapter {
				p.HookHistory = append(p.HookHistory, hookType)
			} else {
				p.HookHistory[chapter-1] = hookType
			}
		}

		return s.saveUnlocked(p)
	})
}

// MarkComplete 标记全书创作完成，并清除重开返工标记（完结即不再处于返工态）。
func (s *ProgressStore) MarkComplete() error {
	return s.io.WithWriteLock(func() error {
		p, err := s.loadUnlocked()
		if err != nil {
			return err
		}
		if p == nil {
			p = &domain.Progress{}
		}
		if err := domain.ValidatePhaseTransition(p.Phase, domain.PhaseComplete); err != nil {
			return err
		}
		p.Phase = domain.PhaseComplete
		p.ReopenedFromComplete = false
		return s.saveUnlocked(p)
	})
}

// Reopen 把已完结的书重新打开进入返工态：phase complete→writing + 目标章入队 + flow=rewriting，
// 在一次写锁内原子完成。这是 phaseOrder“只前进”约束的唯一豁免出口——故意不走
// ValidatePhaseTransition；回退的合法性收敛在本方法、且受 phase=complete 前置守卫保护，
// 避免误用导致状态机失控。改完队列后 commit_chapter 会自动重新收尾完结。
func (s *ProgressStore) Reopen(chapters []int, reason string) error {
	return s.io.WithWriteLock(func() error {
		p, err := s.loadUnlocked()
		if err != nil {
			return err
		}
		if p == nil {
			return fmt.Errorf("progress 未初始化: %w", errs.ErrToolPrecondition)
		}
		if p.Phase != domain.PhaseComplete {
			return fmt.Errorf("reopen 仅适用于已完结的书（当前 phase=%s）: %w", p.Phase, errs.ErrToolPrecondition)
		}
		normalized, err := normalizePendingRewrites(chapters, p.CompletedChapters)
		if err != nil {
			return err
		}
		p.Phase = domain.PhaseWriting // 唯一合法回退，受上面 complete 前置约束保护
		p.PendingRewrites = normalized
		p.RewriteReason = reason
		p.Flow = domain.FlowRewriting
		p.ReopenedFromComplete = true // 排空后按结构完整重新完结，见 commit_chapter drain 块
		return s.saveUnlocked(p)
	})
}

// ReopenContinue 把已完结的书重开为续写态：仅 phase complete→writing，不入返工队列、
// 不置 ReopenedFromComplete（那是"返工排空后按原结构自动重新完结"的 drain 语义，
// 续写重开恰恰要扩展结构）。与 Reopen 同为 phaseOrder"只前进"约束的豁免出口，
// 同受 phase=complete 前置守卫保护；重开后由卷末路由派发架构师续卷。
func (s *ProgressStore) ReopenContinue() error {
	return s.io.WithWriteLock(func() error {
		p, err := s.loadUnlocked()
		if err != nil {
			return err
		}
		if p == nil {
			return fmt.Errorf("progress 未初始化: %w", errs.ErrToolPrecondition)
		}
		if p.Phase != domain.PhaseComplete {
			return fmt.Errorf("重开仅适用于已完结的书（当前 phase=%s）: %w", p.Phase, errs.ErrToolPrecondition)
		}
		p.Phase = domain.PhaseWriting
		p.ReopenCount++ // 审计 + 保证再完结的 progress digest 与上次不同（见字段注释）
		return s.saveUnlocked(p)
	})
}

// ClearInProgress 清除进度中间状态。
func (s *ProgressStore) ClearInProgress() error {
	return s.io.WithWriteLock(func() error {
		p, err := s.loadUnlocked()
		if err != nil {
			return err
		}
		if p == nil {
			return nil
		}
		p.InProgressChapter = 0
		p.CompletedScenes = nil
		return s.saveUnlocked(p)
	})
}

// UpdateVolumeArc 更新当前卷弧位置。
func (s *ProgressStore) UpdateVolumeArc(volume, arc int) error {
	return s.io.WithWriteLock(func() error {
		p, err := s.loadUnlocked()
		if err != nil {
			return err
		}
		if p == nil {
			return nil
		}
		p.CurrentVolume = volume
		p.CurrentArc = arc
		return s.saveUnlocked(p)
	})
}

// SetLayered 设置分层模式标志。
func (s *ProgressStore) SetLayered(layered bool) error {
	return s.io.WithWriteLock(func() error {
		p, err := s.loadUnlocked()
		if err != nil {
			return err
		}
		if p == nil {
			return nil
		}
		p.Layered = layered
		return s.saveUnlocked(p)
	})
}

// SetFlow 更新当前流程状态。
func (s *ProgressStore) SetFlow(flow domain.FlowState) error {
	return s.io.WithWriteLock(func() error {
		p, err := s.loadUnlocked()
		if err != nil {
			return err
		}
		if p == nil {
			return nil
		}
		if err := domain.ValidateFlowTransition(p.Flow, flow); err != nil {
			return err
		}
		p.Flow = flow
		return s.saveUnlocked(p)
	})
}

// SetPendingRewrites 设置待重写章节队列和原因。
// PendingRewrites 只允许包含已完成章节；未完成章节还没有终稿，不能进入重写/打磨队列。
func (s *ProgressStore) SetPendingRewrites(chapters []int, reason string) error {
	return s.io.WithWriteLock(func() error {
		p, err := s.loadUnlocked()
		if err != nil {
			return err
		}
		if p == nil {
			return nil
		}
		normalized, err := normalizePendingRewrites(chapters, p.CompletedChapters)
		if err != nil {
			return err
		}
		p.PendingRewrites = normalized
		p.RewriteReason = reason
		return s.saveUnlocked(p)
	})
}

// ApplyReviewOutcome 原子应用审阅产生的流程状态。审阅语义由上层决定；Store 只负责
// 落盘。reviewedChapter 是本次评审的章节号（非 arc/global 评审时传入，arc/global
// 传 0）：逐章返工 pass 的游标在同一事务内推进。
//
// 游标必须在同一事务内 +1，不能拆成第二次写。若拆开，pass 评到 verdict=accept
// 的章时 affected 为空、PendingRewrites 无写入，引擎收不到任何"这章处理完了"
// 的信号，会重新派发同一章的评审直到 trackDeadlock 熔断。accept 的章不写队列
// 正是评审后按需返工的常态，所以这条路径必须由游标兜住。
func (s *ProgressStore) ApplyReviewOutcome(flow domain.FlowState, chapters []int, reason string, reviewedChapter int) (*domain.Progress, error) {
	var latest *domain.Progress
	var advanced *domain.ReworkProgress
	err := s.io.WithWriteLock(func() error {
		p, err := s.loadUnlocked()
		if err != nil {
			return err
		}
		if p == nil {
			return fmt.Errorf("progress 未初始化: %w", errs.ErrToolPrecondition)
		}
		marked := false
		if len(chapters) > 0 {
			if flow == domain.FlowWriting {
				return fmt.Errorf("返工章节非空时 flow 不能为 writing: %w", errs.ErrToolConflict)
			}
			if err := domain.ValidateFlowTransition(p.Flow, flow); err != nil {
				return err
			}
			normalized, err := normalizePendingRewrites(chapters, p.CompletedChapters)
			if err != nil {
				return err
			}
			p.PendingRewrites = normalized
			p.RewriteReason = reason
			p.Flow = flow
			marked = slices.Contains(normalized, reviewedChapter)
		} else if len(p.PendingRewrites) == 0 {
			if err := domain.ValidateFlowTransition(p.Flow, flow); err != nil {
				return err
			}
			p.Flow = flow
		}
		advanced = advanceReworkPass(p, reviewedChapter, marked)
		if err := s.saveUnlocked(p); err != nil {
			return err
		}
		latest = p
		return nil
	})
	// 钩子在写锁之外触发：回调要发事件、可能被 UI 读取，绝不能持有 progress 锁。
	if advanced != nil && s.reworkHook != nil {
		s.reworkHook(*advanced)
	}
	return latest, err
}

// ReworkHook 是逐章返工游标推进的播报回调，由 Host 挂载以把进度转成用户可见事件。
type ReworkHook func(domain.ReworkProgress)

// SetReworkHook 挂载游标推进回调。必须在 Host 构造后、开始返工前调用。
func (s *ProgressStore) SetReworkHook(h ReworkHook) { s.reworkHook = h }

// advanceReworkPass 在评审落盘的同一事务内推进逐章返工游标。
// 只认"本次评审章 == 当前游标"这一种情况：常规弧/全局评审（非返工 pass 期间
// 发生）传不进正数章号；pass 外的评审即使章号相同也不该推动别人的游标。
func advanceReworkPass(p *domain.Progress, reviewedChapter int, marked bool) *domain.ReworkProgress {
	if p.ReworkPass == nil || reviewedChapter <= 0 {
		return nil
	}
	if p.ReworkPass.Cursor != reviewedChapter {
		return nil
	}
	p.ReworkPass.Cursor++
	p.ReworkPass.Reviewed++
	if marked {
		p.ReworkPass.Rewritten = append(p.ReworkPass.Rewritten, reviewedChapter)
	} else {
		p.ReworkPass.Skipped++
	}
	// 快照要给宿主读，必须深拷贝 Rewritten：p 随后会被后续写入复用。
	snap := *p.ReworkPass
	snap.Rewritten = slices.Clone(p.ReworkPass.Rewritten)
	return &domain.ReworkProgress{
		Pass:     &snap,
		Reviewed: reviewedChapter,
		Rewrote:  marked,
		Done:     snap.Done(),
	}
}

// StartReworkPass 开启逐章返工 pass，范围 [start, end]。
//
// 四道校验都是为了不把作者带进一个语义错乱的中间态：
//   - phase 必须是 writing：规划期没有"已写章节"可返工
//   - 范围必须落在已完成章节内：不能返工尚未写出的章
//   - 已有 pass 在跑时不许叠加：两个游标会互相推错章号
//   - PendingRewrites 必须已排空：队列非空时开 pass，游标推进与队列出队
//     两种进度会交错，无法判断某章处于"已评审待改"还是"改完待确认"
func (s *ProgressStore) StartReworkPass(start, end int, now time.Time) (*domain.Progress, error) {
	if start <= 0 || end < start {
		return nil, fmt.Errorf("返工范围非法：%d-%d，应为正数且首章不大于末章: %w", start, end, errs.ErrToolArgs)
	}
	var latest *domain.Progress
	err := s.io.WithWriteLock(func() error {
		p, err := s.loadUnlocked()
		if err != nil {
			return err
		}
		if p == nil {
			return fmt.Errorf("progress 未初始化: %w", errs.ErrToolPrecondition)
		}
		if p.Phase != domain.PhaseWriting {
			return fmt.Errorf("仅写作阶段可开启逐章返工，当前 phase=%s: %w", p.Phase, errs.ErrToolPrecondition)
		}
		if p.ReworkPass.Active() {
			return fmt.Errorf("已有返工 pass 在跑（第 %d-%d 章，游标 %d），请先 /rework stop: %w",
				p.ReworkPass.StartChapter, p.ReworkPass.EndChapter, p.ReworkPass.Cursor, errs.ErrToolConflict)
		}
		if len(p.PendingRewrites) > 0 {
			return fmt.Errorf("返工队列还有 %d 章（%v）待处理，请先跑空队列再开启 pass: %w",
				len(p.PendingRewrites), p.PendingRewrites, errs.ErrToolConflict)
		}
		maxDone := p.LatestCompleted()
		if end > maxDone {
			return fmt.Errorf("第 %d 章尚未完成，最多可返工到第 %d 章: %w", end, maxDone, errs.ErrToolPrecondition)
		}
		p.ReworkPass = &domain.ReworkPass{
			StartChapter: start,
			EndChapter:   end,
			Cursor:       start,
			StartedAt:    now.Format(time.RFC3339),
		}
		if err := s.saveUnlocked(p); err != nil {
			return err
		}
		latest = p
		return nil
	})
	return latest, err
}

// StopReworkPass 中止 pass。游标停在当前章；已进 PendingRewrites 的章不受影响，
// 仍会按既有队列跑完。停完返回被中止的 pass 供调用方展示。
func (s *ProgressStore) StopReworkPass() (*domain.Progress, *domain.ReworkPass, error) {
	var (
		latest  *domain.Progress
		stopped *domain.ReworkPass
	)
	err := s.io.WithWriteLock(func() error {
		p, err := s.loadUnlocked()
		if err != nil {
			return err
		}
		if p == nil {
			return fmt.Errorf("progress 未初始化: %w", errs.ErrToolPrecondition)
		}
		if p.ReworkPass == nil {
			return fmt.Errorf("当前没有进行中的返工 pass: %w", errs.ErrToolPrecondition)
		}
		// 拷一份再置空：调用方要在返回后读范围与计数，直接返回同一指针会被
		// 后续任何一次 progress 写入就地改掉。
		snapshot := *p.ReworkPass
		snapshot.Rewritten = slices.Clone(p.ReworkPass.Rewritten)
		p.ReworkPass = nil
		if err := s.saveUnlocked(p); err != nil {
			return err
		}
		latest = p
		stopped = &snapshot
		return nil
	})
	return latest, stopped, err
}

// ValidatePendingRewrites 校验章节列表是否可进入返工队列，不修改状态。
func (s *ProgressStore) ValidatePendingRewrites(chapters []int) error {
	s.io.mu.RLock()
	defer s.io.mu.RUnlock()

	p, err := s.loadUnlocked()
	if err != nil {
		return err
	}
	if p == nil {
		_, err := normalizePendingRewrites(chapters, nil)
		return err
	}
	_, err = normalizePendingRewrites(chapters, p.CompletedChapters)
	return err
}

// CompleteRewrite 从待重写队列中移除已完成的章节。
func (s *ProgressStore) CompleteRewrite(chapter int) error {
	return s.io.WithWriteLock(func() error {
		p, err := s.loadUnlocked()
		if err != nil {
			return err
		}
		if p == nil {
			return nil
		}
		var remaining []int
		for _, ch := range p.PendingRewrites {
			if ch != chapter {
				remaining = append(remaining, ch)
			}
		}
		p.PendingRewrites = remaining
		if len(remaining) == 0 {
			if err := domain.ValidateFlowTransition(p.Flow, domain.FlowWriting); err != nil {
				return err
			}
			p.Flow = domain.FlowWriting
			p.RewriteReason = ""
		}
		return s.saveUnlocked(p)
	})
}

// ClearPendingRewrites 强制清空重写队列。
func (s *ProgressStore) ClearPendingRewrites() error {
	return s.io.WithWriteLock(func() error {
		p, err := s.loadUnlocked()
		if err != nil {
			return err
		}
		if p == nil {
			return nil
		}
		p.PendingRewrites = nil
		p.RewriteReason = ""
		if err := domain.ValidateFlowTransition(p.Flow, domain.FlowWriting); err != nil {
			return err
		}
		p.Flow = domain.FlowWriting
		return s.saveUnlocked(p)
	})
}

// ValidateChapterWork 校验当前章节是否允许被规划或提交。
// Writer 只能在 writing 阶段工作；打磨/重写流程下，只允许处理 PendingRewrites
// 中的章节。阶段约束在 Store 边界再守一次，避免错误的 Arbiter 派单绕过 Router。
func (s *ProgressStore) ValidateChapterWork(chapter int) error {
	p, err := s.Load()
	if err != nil {
		return err
	}
	if p == nil {
		return fmt.Errorf("progress 未初始化: %w", errs.ErrToolPrecondition)
	}
	if p.Phase != domain.PhaseWriting {
		return fmt.Errorf("章节写作仅允许在 writing 阶段（当前 phase=%s）: %w", p.Phase, errs.ErrToolPrecondition)
	}
	if p.Flow != domain.FlowRewriting && p.Flow != domain.FlowPolishing {
		return nil
	}
	if _, err := normalizePendingRewrites(p.PendingRewrites, p.CompletedChapters); err != nil {
		return err
	}
	if slices.Contains(p.PendingRewrites, chapter) {
		return nil
	}

	verb := "重写"
	if p.Flow == domain.FlowPolishing {
		verb = "打磨"
	}
	return fmt.Errorf("第 %d 章不在待%s队列中，当前队列：%v。请先处理队列内章节，再动新章节: %w", chapter, verb, p.PendingRewrites, errs.ErrToolConflict)
}

func normalizePendingRewrites(chapters, completed []int) ([]int, error) {
	if len(chapters) == 0 {
		return nil, nil
	}
	completedSet := make(map[int]struct{}, len(completed))
	for _, ch := range completed {
		completedSet[ch] = struct{}{}
	}

	seen := make(map[int]struct{}, len(chapters))
	normalized := make([]int, 0, len(chapters))
	var invalid []int
	for _, ch := range chapters {
		if ch <= 0 {
			invalid = append(invalid, ch)
			continue
		}
		if _, ok := completedSet[ch]; !ok {
			invalid = append(invalid, ch)
			continue
		}
		if _, ok := seen[ch]; ok {
			continue
		}
		seen[ch] = struct{}{}
		normalized = append(normalized, ch)
	}
	if len(invalid) > 0 {
		return nil, fmt.Errorf("pending_rewrites 只能包含已完成章节，非法章节：%v，completed_chapters=%v: %w", invalid, completed, errs.ErrToolPrecondition)
	}
	return normalized, nil
}
