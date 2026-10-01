package store

import (
	"os"
	"time"

	"github.com/voocel/ainovel-cli/internal/rules"
)

// EraProposal 是一条待审的时代术语候选：自动生成、用户裁决，不自动生效。
//
// 为什么与 UserRules 分文件存储：采纳后的对照项命中即 SeverityError，会被
// editor.md 强制升级 verdict 触发改写。一条错误的历史称谓若被系统当作权威自动
// 执行，代价是改写正确正文。所以候选与生效规则必须物理分离，且文件里始终带
// Source=auto_generated 与 Confidence，让"这是机器猜的、还没人看过"成为
// 落盘数据里可见的事实，而不是靠记性。
type EraProposal struct {
	Banned string   `json:"banned"`         // 禁用的词或短语
	Use    []string `json:"use,omitempty"`  // 建议替代，按优先级
	Note   string   `json:"note,omitempty"` // 理由（时代依据）
	Era    string   `json:"era,omitempty"`  // 生成所依据的朝代/时期
	Source string   `json:"source"`         // 恒为 auto_generated

	// Confidence 是模型对自身判断的把握，供人工审阅时优先复核 low 项。
	// 它不参与任何拦截逻辑，仅影响展示排序与提示。
	Confidence string `json:"confidence,omitempty"`

	Decision  string     `json:"decision,omitempty"` // pending / adopted / rejected
	DecidedAt *time.Time `json:"decided_at,omitempty"`

	// Adopted 落地后的实际内容。采纳走确定性写入（不再经 LLM 归一化），
	// 因为用户已逐条看过，此处再让模型改写一遍只会引入偏差。
	Adopted *rules.TermCorrection `json:"adopted,omitempty"`
}

// 裁决状态取值。
const (
	EraDecisionPending  = "pending"
	EraDecisionAdopted  = "adopted"
	EraDecisionRejected = "rejected"
)

// Pending 判断该候选是否仍待裁决。
func (p EraProposal) Pending() bool {
	return p.Decision == "" || p.Decision == EraDecisionPending
}

// EraProposals 管理本书的时代术语候选表（meta/era_proposals.json）。
//
// 与 UserRulesStore 同样走单一 JSON + 进程内锁；候选表体量小（几十条），
// 不需要独立的数据库。
type EraProposalsStore struct{ io *IO }

// EraProposalDoc 是落盘文档。
type EraProposalDoc struct {
	Version     int           `json:"version"`
	Era         string        `json:"era,omitempty"`
	Source      string        `json:"source"`
	GeneratedAt time.Time     `json:"generated_at"`
	Proposals   []EraProposal `json:"proposals"`
}

// EraProposalsVersion 是文档 schema 版本。
const EraProposalsVersion = 1

func NewEraProposalsStore(io *IO) *EraProposalsStore { return &EraProposalsStore{io: io} }

// Load 读取候选表；不存在时返回 nil。
func (s *EraProposalsStore) Load() (*EraProposalDoc, error) {
	s.io.mu.RLock()
	defer s.io.mu.RUnlock()
	var doc EraProposalDoc
	if err := s.io.ReadJSONUnlocked("meta/era_proposals.json", &doc); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	return &doc, nil
}

// Save 覆盖保存候选表。
func (s *EraProposalsStore) Save(doc *EraProposalDoc) error {
	s.io.mu.Lock()
	defer s.io.mu.Unlock()
	return s.io.WriteJSONUnlocked("meta/era_proposals.json", doc)
}
