package userrules

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/voocel/ainovel-cli/internal/store"
)

// 静态时代称谓表的解析。
//
// 为什么不走 LLM：这是知识检索型任务，模型不可靠且极慢（实测 medium 推理强度下
// 思考 5 分钟仍不产出，或思考烧完预算导致输出被截断）。而表的容错率是零——一条
// 错的时代词条会被系统当作权威强制改写正确正文。静态表可审阅、可版本管理、
// 零延迟、零费用，且与既有的 genre pack 资产体系一致。

// eraTermLine 匹配形如：
//
//   - **相公** → 郎君 / 君 / 先生 | 唐指宰相，明清才通行……
//
// 允许 use 为空（写作「—」或留空），此时只禁用不提供替代。
var eraTermLine = regexp.MustCompile(`^\s*[-*]\s*(?:\*\*)?([^＊]+?)(?:\*\*)?\s*→\s*(.+)$`)

// ParseEraTerminology 解析静态时代称谓表。
//
// 只收「禁用词 → 写法」这一类条目。表里还有「该朝代可用」「制度与观念」等
// 分节，那些不是禁用项，解析时按 note 语义区分：替代项以「—」或含「不可用」
// 开头的视为"仅禁用、无替代"。
func ParseEraTerminology(md, era string) []store.EraProposal {
	var out []store.EraProposal
	seen := map[string]bool{}
	// allowed 记录哪些词曾被标为「该时代可用」。用户覆盖可能想解封内置禁用项
	// （写「相公（可用）」），而解析按词去重取首条——若不做两遍扫描，内置的
	// 禁用项会赢，用户看到的是静默失效：写了不生效，也不报错。
	allowed := map[string]bool{}
	inFence := false

	for _, line := range strings.Split(md, "\n") {
		line = strings.TrimRight(line, " \t\r")
		if strings.TrimSpace(line) == "" {
			continue
		}
		// 跳过围栏代码块。维护文档里的示例（`- **相国** → 丞相`）若被当数据收进
		// 候选，会凭空产生「相国」禁用项并被用户误采纳——表文件混进文档就出这种事。
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			inFence = !inFence
			continue
		}
		if inFence {
			continue
		}
		m := eraTermLine.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		banned := strings.TrimSpace(m[1])
		// 表头与分节里的「禁用词 → 该时代写法」说明行不是条目
		if banned == "" || banned == "禁用词" || strings.Contains(banned, "该时代") {
			continue
		}
		banned = strings.Trim(banned, "*_` ")
		if banned == "" {
			continue
		}

		rhs := strings.TrimSpace(m[2])
		var use []string
		var note string
		if i := strings.Index(rhs, "|"); i >= 0 {
			note = strings.TrimSpace(rhs[i+1:])
			rhs = strings.TrimSpace(rhs[:i])
		}
		rhs = strings.Trim(rhs, "。 ")
		// 「—」表示该时代无对应写法，只禁用不替代
		if rhs != "" && rhs != "—" && rhs != "-" && !strings.HasPrefix(rhs, "不可用") {
			for _, part := range strings.Split(rhs, "/") {
				if p := cleanUseItem(part); p != "" {
					use = append(use, p)
				}
			}
		}
		// 「可用」类条目不在此跳过，统一留给第二遍——解封声明可能出现在禁用项
		// 之后，顺序不能决定结果。投票必须在去重之前做，否则用户覆盖里的解封
		// 声明会被 seen 挡掉，永远投不上票。
		if isAllowedEntry(banned, use, rhs, note) {
			allowed[banned] = true
		}
		if seen[banned] {
			continue
		}
		seen[banned] = true
		out = append(out, store.EraProposal{
			Banned:     banned,
			Use:        use,
			Note:       note,
			Era:        era,
			Source:     "static_table",
			Confidence: "high",
			Decision:   store.EraDecisionPending,
		})
	}
	// 第二遍：剔除「该时代可用」的词。可用声明出现在禁用项之后也能解封它——
	// 否则用户覆盖里写「相公（可用）」会被内置禁用项静默压过，写了不生效也不报错。
	out = slices.DeleteFunc(out, func(p store.EraProposal) bool { return allowed[p.Banned] })
	return out
}

// useAnnotationRe 匹配写替代项时附带的限定语，如「拙荆（可用）」「相公（慎用）」。
var useAnnotationRe = regexp.MustCompile(`[（(][^）)]*[）)]\s*$`)

// isAllowedEntry 判定一条解析结果是否属于「该时代可用」而非「禁用」。
//
// 三种形态都算可用条目：
//   - 替代项就是禁用词本身（太守 → 太守）
//   - 替代项写着「可用」/「不可用」/「慎用」这类说明而非具体写法
//   - note 以「可用」开头
func isAllowedEntry(banned string, use []string, rhs, note string) bool {
	if strings.HasPrefix(strings.TrimSpace(rhs), "可用") ||
		strings.HasPrefix(strings.TrimSpace(rhs), "不可用") ||
		strings.HasPrefix(strings.TrimSpace(rhs), "慎用") {
		return true
	}
	if strings.HasPrefix(note, "可用") || strings.HasPrefix(note, "不可用") {
		return true
	}
	for _, u := range use {
		if u == banned {
			return true
		}
	}
	return false
}

// cleanUseItem 去掉写替代项时附带的括注，只留可机械使用的写法。
func cleanUseItem(s string) string {
	s = strings.TrimSpace(s)
	s = useAnnotationRe.ReplaceAllString(s, "")
	return strings.TrimSpace(s)
}

// EraStaticTable 返回内置的静态时代称谓表内容（由 assets 注入，避免 rules
// 反向依赖 assets 包）。
type EraStaticTable struct {
	Content string
	// Available 列出该表覆盖的朝代，供命令提示与校验。
	Available []string
}

// LoadEraProposalsFromTable 把静态表转成候选文档（pending，不触碰 user_rules）。
//
// 与 LLM 生成路径共用同一份 EraProposal 结构与同一套裁决流程（/rules adopt），
// 因此「静态表」与「模型提议」对用户是同一种东西，区别只在来源标注
// （static_table vs auto_generated）与可信度。
func LoadEraProposalsFromTable(table EraStaticTable, era string) (*store.EraProposalDoc, error) {
	era = strings.TrimSpace(era)
	if era == "" {
		return nil, fmt.Errorf("朝代不能为空")
	}
	if strings.TrimSpace(table.Content) == "" {
		return nil, fmt.Errorf("内置时代称谓表缺失")
	}
	ps := ParseEraTerminology(table.Content, era)
	if len(ps) == 0 {
		return nil, fmt.Errorf("未能从静态表解析出任何条目")
	}
	return &store.EraProposalDoc{
		Version:     store.EraProposalsVersion,
		Era:         era,
		Source:      "static_table",
		GeneratedAt: time.Now(),
		Proposals:   ps,
	}, nil
}
