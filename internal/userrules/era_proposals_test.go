package userrules

import (
	"strings"
	"testing"

	"github.com/voocel/ainovel-cli/internal/rules"
	"github.com/voocel/ainovel-cli/internal/store"
)

// seedProposals 写入一份候选表，模拟已生成但未裁决的状态。
func seedProposals(t *testing.T, st *store.Store, ps ...store.EraProposal) {
	t.Helper()
	if err := st.EraProposals.Save(&store.EraProposalDoc{
		Version: store.EraProposalsVersion, Era: "东汉末年", Source: "auto_generated",
		Proposals: ps,
	}); err != nil {
		t.Fatalf("Save: %v", err)
	}
}

func proposal(banned string, conf string) store.EraProposal {
	return store.EraProposal{
		Banned: banned, Use: []string{"郎君", "先生"}, Note: "明清通行",
		Era: "东汉末年", Source: "auto_generated", Confidence: conf,
		Decision: store.EraDecisionPending,
	}
}

// 本文件的核心不变量：候选永远不参与检查。生成只是提议，不改机械底线。
func TestProposalDoesNotAffectCheckingUntilAdopted(t *testing.T) {
	st := store.NewStore(t.TempDir())
	seedProposals(t, st, proposal("相公", "high"), proposal("媳妇", "low"))

	const text = "「相公，这事要办。」"
	snap := &rules.Snapshot{Status: rules.StatusReady}
	if vs := rules.Check(text, snap.Structured); len(vs) != 0 {
		t.Fatalf("空规则下不应有违规: %+v", vs)
	}
	// 候选文件存在也不改变结论
	if vs := rules.Check(text, snap.Structured); len(vs) != 0 {
		t.Fatalf("候选不得影响检查: %+v", vs)
	}

	// 采纳后才进入生效规则
	if _, err := AdoptProposal(st, "相公"); err != nil {
		t.Fatalf("AdoptProposal: %v", err)
	}
	got, err := st.UserRules.Load()
	if err != nil || got == nil {
		t.Fatalf("Load: %v", err)
	}
	vs := rules.Check(text, got.Structured)
	if len(vs) != 1 || vs[0].Target != "相公" {
		t.Fatalf("采纳后应命中: %+v", vs)
	}
	if len(vs[0].Suggestion) != 2 {
		t.Errorf("应带替代项: %+v", vs[0])
	}
}

func TestAdoptProposalRecordsDecision(t *testing.T) {
	st := store.NewStore(t.TempDir())
	seedProposals(t, st, proposal("相公", "high"))
	if _, err := AdoptProposal(st, "相公"); err != nil {
		t.Fatalf("AdoptProposal: %v", err)
	}
	doc, err := st.EraProposals.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if doc.Proposals[0].Decision != store.EraDecisionAdopted {
		t.Errorf("应标记为 adopted，实际 %q", doc.Proposals[0].Decision)
	}
	if doc.Proposals[0].DecidedAt == nil {
		t.Error("应记录裁决时间")
	}
	if doc.Proposals[0].Adopted == nil || doc.Proposals[0].Adopted.Banned != "相公" {
		t.Errorf("应留档落地内容: %+v", doc.Proposals[0].Adopted)
	}
}

// 幂等：重复采纳报错而非静默重复写入。
func TestAdoptProposalIsIdempotent(t *testing.T) {
	st := store.NewStore(t.TempDir())
	seedProposals(t, st, proposal("相公", "high"))
	if _, err := AdoptProposal(st, "相公"); err != nil {
		t.Fatalf("首次采纳: %v", err)
	}
	if _, err := AdoptProposal(st, "相公"); err == nil {
		t.Fatal("重复采纳应报错")
	}
	snap, _ := st.UserRules.Load()
	if n := len(snap.Structured.TermCorrections); n != 1 {
		t.Fatalf("不应重复写入，实际 %d 条: %+v", n, snap.Structured.TermCorrections)
	}
}

// 词不在候选表里必须报错——绝不静默新建，否则打错一个词就凭空多一条
// 会被强制执行的规则。
func TestAdoptUnknownProposalFails(t *testing.T) {
	st := store.NewStore(t.TempDir())
	seedProposals(t, st, proposal("相公", "high"))
	if _, err := AdoptProposal(st, "媳妇"); err == nil {
		t.Fatal("候选表外的词应报错")
	}
	snap, _ := st.UserRules.Load()
	if snap != nil && len(snap.Structured.TermCorrections) != 0 {
		t.Fatalf("不应写入任何规则: %+v", snap.Structured.TermCorrections)
	}
}

func TestAdoptWithoutProposalsFails(t *testing.T) {
	st := store.NewStore(t.TempDir())
	if _, err := AdoptProposal(st, "相公"); err == nil {
		t.Fatal("未生成候选时采纳应报错")
	}
}

// 快照缺失时以 system_defaults 起手，不能丢掉机械基线。
func TestAdoptKeepsSystemDefaultsWhenSnapshotMissing(t *testing.T) {
	st := store.NewStore(t.TempDir())
	seedProposals(t, st, proposal("相公", "high"))
	if _, err := AdoptProposal(st, "相公"); err != nil {
		t.Fatalf("AdoptProposal: %v", err)
	}
	snap, _ := st.UserRules.Load()
	if len(snap.Structured.FatigueWords) == 0 || len(snap.Structured.ForbiddenPhrases) == 0 {
		t.Errorf("采纳不应丢掉 system_defaults 机械基线: %+v", snap.Structured)
	}
	if len(snap.Structured.TermCorrections) != 1 {
		t.Errorf("应写入对照项: %+v", snap.Structured.TermCorrections)
	}
}

// 已生效的规则不能靠 reject 撤销——那会让快照与候选表不一致。
func TestRejectAdoptedProposalRefused(t *testing.T) {
	st := store.NewStore(t.TempDir())
	seedProposals(t, st, proposal("相公", "high"))
	if _, err := AdoptProposal(st, "相公"); err != nil {
		t.Fatalf("AdoptProposal: %v", err)
	}
	if err := RejectProposal(st, "相公"); err == nil {
		t.Fatal("已采纳的候选不应能被否决")
	}
	snap, _ := st.UserRules.Load()
	if len(snap.Structured.TermCorrections) != 1 {
		t.Errorf("规则应仍在: %+v", snap.Structured.TermCorrections)
	}
}

func TestRejectProposalMarksDecision(t *testing.T) {
	st := store.NewStore(t.TempDir())
	seedProposals(t, st, proposal("媳妇", "low"))
	if err := RejectProposal(st, "媳妇"); err != nil {
		t.Fatalf("RejectProposal: %v", err)
	}
	doc, _ := st.EraProposals.Load()
	if doc.Proposals[0].Decision != store.EraDecisionRejected {
		t.Errorf("应标记 rejected，实际 %q", doc.Proposals[0].Decision)
	}
	if n := len(PendingProposals(doc)); n != 0 {
		t.Errorf("已否决的不应出现在待裁决里: %d", n)
	}
}

// low 把握排在最前——这些最可能出错，也最需要作者亲自过目。
func TestPendingProposalsLowFirst(t *testing.T) {
	doc := &store.EraProposalDoc{Proposals: []store.EraProposal{
		proposal("a", "high"), proposal("b", "low"), proposal("c", "high"), proposal("d", "low"),
	}}
	got := PendingProposals(doc)
	if len(got) != 4 {
		t.Fatalf("应 4 条，实际 %d", len(got))
	}
	if got[0].Banned != "b" || got[1].Banned != "d" {
		t.Errorf("low 应排在前，实际 %s %s", got[0].Banned, got[1].Banned)
	}
	// high 之间保持原有相对顺序（稳定）
	if got[2].Banned != "a" || got[3].Banned != "c" {
		t.Errorf("high 之间应保序，实际 %s %s", got[2].Banned, got[3].Banned)
	}
	if PendingProposals(nil) != nil {
		t.Error("nil 应返回 nil")
	}
}

// 候选表从不出现在 user_rules 里——物理分离是可审计性的前提。
func TestProposalsNeverLandInUserRules(t *testing.T) {
	st := store.NewStore(t.TempDir())
	seedProposals(t, st, proposal("相公", "high"))
	if _, err := st.UserRules.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	snap, err := st.UserRules.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if snap != nil {
		t.Fatalf("仅生成候选时 user_rules 不应被创建: %+v", snap)
	}
}

// SuggestEra 只做轻量提取，且不猜——题材宽泛时宁可不给建议。
func TestSuggestEra(t *testing.T) {
	cases := map[string]string{
		"东汉末年题材": "东汉末年",
		"历史":     "历史",
		"":       "",
	}
	for genre, want := range cases {
		snap := &rules.Snapshot{Structured: rules.Structured{Genre: genre}}
		if got := SuggestEra(snap); got != want {
			t.Errorf("题材 %q 建议 %q，实际 %q", genre, want, got)
		}
	}
	if got := SuggestEra(nil); got != "" {
		t.Errorf("nil 应返回空串，实际 %q", got)
	}
}

// 生成提示词必须把权威交回作者，并要求自陈把握。
func TestEraProposePromptDisclaimsAuthority(t *testing.T) {
	for _, want := range []string{
		"只是提供候选",
		"作者会逐条审阅",
		"confidence",
		"low",
		"不要为了凑数",
	} {
		if !strings.Contains(eraProposePrompt, want) {
			t.Errorf("候选生成提示词应含 %q", want)
		}
	}
}
