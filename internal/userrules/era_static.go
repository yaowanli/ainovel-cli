package userrules

import (
	"fmt"
	"regexp"
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

	for _, line := range strings.Split(md, "\n") {
		line = strings.TrimRight(line, " \t\r")
		if strings.TrimSpace(line) == "" {
			continue
		}
		// 分节标题只用于标记后续条目的适用范围
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
		if banned == "" || seen[banned] {
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
		// 「可用」类条目必须跳过，否则会把该时代本来正确的词禁掉——
		// 在东汉书里禁用「太守/刺史/朝廷」是灾难性误伤。
		if skipAllowedEntry(banned, use, rhs, note) {
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
	return out
}

// useAnnotationRe 匹配写替代项时附带的限定语，如「拙荆（可用）」「相公（慎用）」。
var useAnnotationRe = regexp.MustCompile(`[（(][^）)]*[）)]\s*$`)

// skipAllowedEntry 判定一条解析结果是否属于「该时代可用」而非「禁用」。
//
// 三种形态都算可用条目：
//   - 替代项就是禁用词本身（太守 → 太守）
//   - 替代项写着「可用」/「不可用」/「慎用」这类说明而非具体写法
//   - note 以「可用」开头
func skipAllowedEntry(banned string, use []string, rhs, note string) bool {
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
