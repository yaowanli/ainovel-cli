// Package userrules 的时代术语候选生成与裁决。
//
// 要解决的问题：历史题材的时代错误是**成族**的——用户禁掉「沈相公」，全书还有
// 130+ 处裸「相公」；禁掉「相公」，还有「媳妇」「大人」「大哥」「掌柜」。
// 逐个补是打地鼠，因为 forbidden_phrases 表达的是「词」而不是「类」。
//
// 但自动生成直接进 forbidden_phrases 是危险的：那张表命中即 SeverityError，
// 且 editor.md 强制「至少一条 issue + verdict 升级」，等于让系统把模型的偏见
// 当作权威去改写正确正文。成本不对称——风格规则错了损失文风，时代规则错了
// 损失的正是要修的事实正确性。
//
// 所以本文件实现的是「自动提议 + 人工裁决」：
//
//	生成 → meta/era_proposals.json（Decision=pending，不参与任何检查）
//	采纳 → 确定性写入 user_rules.term_corrections（此后走机械拦截）
//	否决 → 标记 rejected，留档不再提示
//
// 采纳走确定性写入而非再次 LLM 归一化：用户已逐条看过内容，再让模型改一遍
// 只会引入偏差。这也是本文件唯一的写路径。
package userrules

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/voocel/agentcore"
	"github.com/voocel/agentcore/schema"
	"github.com/voocel/ainovel-cli/internal/llmcontract"
	"github.com/voocel/ainovel-cli/internal/llmretry"
	"github.com/voocel/ainovel-cli/internal/rules"
	"github.com/voocel/ainovel-cli/internal/store"
)

// eraProposeMaxTokens 与归一化同量级：JSON 本身很小，预算主要留给推理模型的思考。
const eraProposeMaxTokens = 8192

var eraProposeContract = llmcontract.Contract{
	Name:        "userrules_era_propose",
	Description: "为指定朝代/时期生成时代称谓与禁忌候选表",
	Schema: schema.Object(
		schema.Property("proposals", schema.Array("时代术语候选", schema.Object(
			schema.Property("banned", schema.String("该时代不应出现的称谓或术语")).Required(),
			schema.Property("use", schema.Array("该时代对应的正确写法", schema.String("写法"))).Required(),
			schema.Property("note", schema.String("一句话理由:该词属于哪个时代")).Required(),
			schema.Property("confidence", schema.String("把握程度: high 或 low")).Required(),
		))).Required(),
	),
}

// eraProposePrompt 只产出候选，且要求模型自陈把握。
//
// 两处刻意的措辞：一是「不是禁令我，只是候选，最终由作者裁决」，把权威还给用户；
// 二是要求对跨时代争议词自报 low —— 该时代本来就有歧义的词（如「大人」在汉可指
// 父母长辈、在明清是敬称），让模型自己标出来，人工复核时优先看这些，比事后发现
// 改坏了正文便宜得多。
const eraProposePrompt = `你为一部历史题材小说生成「时代术语候选表」。

【你的角色】
你只是提供候选，不是下禁令。作者会逐条审阅后才决定是否采纳。你的输出会被人工
裁决后才可能生效——所以宁可少列几条有把握的，也不要列一堆模糊的。

【要求】
1. 只列「称谓、官称、礼制词汇」这类会因时代而出戏的词。不要列现代词汇（那些与朝代无关）。
2. banned 必须是能字面精确匹配的词或短语，不要写正则或描述。
3. use 必须是该时代真实通行的写法，按最常用优先。use 为空的条目不要给。
4. note 一句话说明该词属于哪个时代、为什么本时代不适用。
5. 跨时代有争议、或你不确信的词，confidence 填 low；确信的高频错误填 high。
6. 不要为了凑数列举：一朝代的称谓体系就那么多，宁缺毋滥。

【不要做的事】
- 不要输出 Markdown、说明文字或注释，只输出 JSON。
- 不要重复同一条。`

type eraProposeOutput struct {
	Proposals []eraProposeItem `json:"proposals"`
}

type eraProposeItem struct {
	Banned     string   `json:"banned"`
	Use        []string `json:"use"`
	Note       string   `json:"note"`
	Confidence string   `json:"confidence"`
}

// EraGenerator 生成时代术语候选。
type EraGenerator struct {
	model agentcore.ChatModel
	// OnRetry 报告 provider 侧重试（限流、5xx 等）。
	//
	// 必须暴露：llmretry 对可重试错误是「退避后持续重试，直到成功或 ctx 结束」，
	// 无上限。单次生成卡十几分钟多数不是模型在思考，而是被限流后反复重试——
	// 而此前这里没接 hook，重试完全不可见，用户只能对着一个不动的进度条猜。
	OnRetry func(attempt int, delay time.Duration, err error)
}

// NewEraGenerator 构造生成器。model 应为能力较强的模型（通常 models.Default），
// 不必跟随写作的弱模型。
func NewEraGenerator(model agentcore.ChatModel) *EraGenerator {
	return &EraGenerator{model: model}
}

// WithRetryReporter 挂上重试回调（Host 侧接到面板事件流）。
func (g *EraGenerator) WithRetryReporter(f func(attempt int, delay time.Duration, err error)) *EraGenerator {
	if g != nil {
		g.OnRetry = f
	}
	return g
}

// Generate 为 era 生成候选表。era 必须是用户明确给出的朝代或时期
// （如「东汉末年」），不由系统猜测——猜错朝代会产出一整批无关的候选，
// 而用户看不出来那些是猜错的。
//
// 结果按 banned 去重（同词保留首条）后落盘为 pending，不触碰 user_rules。
func (g *EraGenerator) Generate(ctx context.Context, era string) (*store.EraProposalDoc, error) {
	era = strings.TrimSpace(era)
	if era == "" {
		return nil, fmt.Errorf("朝代不能为空")
	}
	if g == nil || g.model == nil {
		return nil, fmt.Errorf("候选生成模型未配置")
	}

	out, err := llmcontract.Execute(ctx, g.model, llmcontract.Request[eraProposeOutput]{
		Contract:     eraProposeContract,
		SystemPrompt: eraProposePrompt,
		Payload:      "朝代/时期：" + era,
		Options:      []agentcore.CallOption{agentcore.WithMaxTokens(eraProposeMaxTokens)},
		Validate: func(o *eraProposeOutput) error {
			if len(o.Proposals) == 0 {
				return fmt.Errorf("proposals 为空：没有可提出的时代术语")
			}
			return nil
		},
		Agent: "rules",
		Hooks: llmcontract.Hooks{
			RequestRetry: func(ev llmretry.Event) {
				if g.OnRetry != nil {
					g.OnRetry(ev.Attempt, ev.Delay, ev.Err)
				}
			},
			Resolved: func(res llmcontract.Resolution) {
				slog.Debug("时代术语候选协议选择", "module", "rules", "era", era,
					"contract", eraProposeContract.Name, "structured_mode", res.Mode,
					"provider", res.Provider, "model", res.Model)
			},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("生成时代术语候选失败: %w", err)
	}

	now := time.Now()
	doc := &store.EraProposalDoc{
		Version:     store.EraProposalsVersion,
		Era:         era,
		Source:      "auto_generated",
		GeneratedAt: now,
	}
	seen := map[string]bool{}
	for _, it := range out.Proposals {
		banned := strings.TrimSpace(it.Banned)
		if banned == "" || seen[banned] {
			continue
		}
		seen[banned] = true
		conf := strings.ToLower(strings.TrimSpace(it.Confidence))
		if conf != "high" {
			conf = "low"
		}
		doc.Proposals = append(doc.Proposals, store.EraProposal{
			Banned:     banned,
			Use:        nonEmpty(it.Use),
			Note:       strings.TrimSpace(it.Note),
			Era:        era,
			Source:     "auto_generated",
			Confidence: conf,
			Decision:   store.EraDecisionPending,
		})
	}
	if len(doc.Proposals) == 0 {
		return nil, fmt.Errorf("候选全部无效：banned 均为空")
	}
	return doc, nil
}

// PendingProposals 返回仍待裁决的候选（low 把握的排在前面，优先人工复核）。
func PendingProposals(doc *store.EraProposalDoc) []store.EraProposal {
	if doc == nil {
		return nil
	}
	var out []store.EraProposal
	for _, p := range doc.Proposals {
		if p.Pending() {
			out = append(out, p)
		}
	}
	// low 优先：这些最可能出错，也就最需要作者亲自过目
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j].Confidence == "low" && out[j-1].Confidence != "low"; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

// AdoptProposal 把一条候选写入生效规则（确定性路径，不经 LLM）。
//
// 幂等：已采纳的再次调用返回 ErrAlreadyAdopted，不重复写入。
// 不存在则返回 ErrProposalNotFound——绝不静默新建，避免打错词就凭空多一条规则。
func AdoptProposal(st *store.Store, banned string) (rules.TermCorrection, error) {
	doc, err := st.EraProposals.Load()
	if err != nil {
		return rules.TermCorrection{}, err
	}
	if doc == nil {
		return rules.TermCorrection{}, fmt.Errorf("尚未生成时代术语候选：/rules propose <朝代>")
	}
	banned = strings.TrimSpace(banned)
	idx := -1
	for i, p := range doc.Proposals {
		if p.Banned == banned {
			idx = i
			break
		}
	}
	if idx < 0 {
		return rules.TermCorrection{}, fmt.Errorf("候选表里没有 %q：/rules proposals 查看", banned)
	}
	if !doc.Proposals[idx].Pending() {
		return rules.TermCorrection{}, fmt.Errorf("%q 已裁决过（%s）", banned, doc.Proposals[idx].Decision)
	}

	tc := rules.TermCorrection{
		Banned: doc.Proposals[idx].Banned,
		Use:    append([]string(nil), doc.Proposals[idx].Use...),
		Note:   doc.Proposals[idx].Note,
	}

	snap, err := loadSnapshotForWrite(st)
	if err != nil {
		return rules.TermCorrection{}, err
	}
	snap.Structured.TermCorrections = rules.MergeTermCorrections(snap.Structured.TermCorrections, []rules.TermCorrection{tc})
	if err := st.UserRules.Save(snap); err != nil {
		return rules.TermCorrection{}, err
	}

	doc.Proposals[idx].Decision = store.EraDecisionAdopted
	now := time.Now()
	doc.Proposals[idx].DecidedAt = &now
	doc.Proposals[idx].Adopted = &tc
	if err := st.EraProposals.Save(doc); err != nil {
		return rules.TermCorrection{}, err
	}
	return tc, nil
}

// RejectProposal 标记否决。已采纳的不可否决——那会留下一条已生效的规则与
// 一条 rejected 的记录，让快照与候选表不一致。
func RejectProposal(st *store.Store, banned string) error {
	doc, err := st.EraProposals.Load()
	if err != nil {
		return err
	}
	if doc == nil {
		return fmt.Errorf("尚未生成时代术语候选：/rules propose <朝代>")
	}
	banned = strings.TrimSpace(banned)
	for i, p := range doc.Proposals {
		if p.Banned != banned {
			continue
		}
		if p.Decision == store.EraDecisionAdopted {
			return fmt.Errorf("%q 已采纳并生效，不能否决；请先用 /steer 移除该对照项", banned)
		}
		now := time.Now()
		doc.Proposals[i].Decision = store.EraDecisionRejected
		doc.Proposals[i].DecidedAt = &now
		return st.EraProposals.Save(doc)
	}
	return fmt.Errorf("候选表里没有 %q：/rules proposals 查看", banned)
}

// loadSnapshotForWrite 读取待改快照，缺失时以 system_defaults 起手。
//
// 绝不在快照缺失时静默丢弃默认机械基线：那会让用户失去全部 fatigue_words 兜底。
func loadSnapshotForWrite(st *store.Store) (*rules.Snapshot, error) {
	cur, err := st.UserRules.Load()
	if err != nil {
		return nil, err
	}
	if cur != nil {
		return cur, nil
	}
	def := rules.BuildSnapshot([]rules.Candidate{rules.SystemDefaults()})
	return &def, nil
}

// SuggestEra 从规则快照的题材字段推出朝代候选，供命令做默认值。
//
// 只做字符串层面的轻量提取且**只作为建议展示**：题材字段是「题材」不是「朝代」，
// 拿不准时宁可不给建议，让用户显式指定。猜错朝代会产出一整批无关候选。
func SuggestEra(snap *rules.Snapshot) string {
	if snap == nil {
		return ""
	}
	g := strings.TrimSpace(snap.Structured.Genre)
	if g == "" {
		return ""
	}
	// 「东汉末年题材」→「东汉末年」；无「题材」后缀则原样返回
	g = strings.TrimSuffix(g, "题材")
	return strings.TrimSpace(g)
}
