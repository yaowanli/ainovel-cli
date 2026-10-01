package userrules

import (
	"strings"
	"testing"

	"github.com/voocel/ainovel-cli/internal/rules"
	"github.com/voocel/ainovel-cli/internal/store"
)

const sampleTable = `# 时代称谓对照表

## 称谓

- **相公** → 郎君 / 君 / 先生 | 唐指宰相，明清才通行
- **大人** → 长者 / 丈人 | 汉之「大人」指父母长辈
- **科举** → 察举 / 孝廉 | 隋唐始行
- **玻璃** → — | 明清方见
- **太守** → 太守（可用） | 汉郡长官
- **刺史** → 可用 | 汉武帝后置
- **陛下** → 可用 | 汉已有
- **拙荆** → 拙荆（可用） | 六朝已见

## 说明行

- 禁用词 → 该时代写法
- 朝代/时期：东汉末年
`

// 「该时代可用的词」绝不能变成禁用项——在东汉书里禁用「太守/刺史/朝廷/陛下」
// 是灾难性误伤，采纳后会强制改写完全正确的正文。
func TestParseEraTerminologySkipsAllowedWords(t *testing.T) {
	ps := ParseEraTerminology(sampleTable, "东汉末年")
	got := map[string]bool{}
	for _, p := range ps {
		got[p.Banned] = true
	}
	for _, must := range []string{"相公", "大人", "科举", "玻璃"} {
		if !got[must] {
			t.Errorf("应解析出禁用项 %q", must)
		}
	}
	for _, banned := range []string{"太守", "刺史", "陛下", "拙荆", "禁用词", "朝代/时期"} {
		if got[banned] {
			t.Errorf("不该把 %q 当成禁用项（可用词/表头/分节）", banned)
		}
	}
}

// 替代项里的括注必须剥掉，否则 writer 会照着「拙荆（可用）」去写。
func TestParseEraTerminologyCleansAnnotations(t *testing.T) {
	for _, p := range ParseEraTerminology(sampleTable, "东汉末年") {
		for _, u := range p.Use {
			if strings.ContainsAny(u, "（）()") {
				t.Errorf("%s 的替代项 %q 仍带括注", p.Banned, u)
			}
			if u == p.Banned {
				t.Errorf("%s 的替代项等于禁用词", p.Banned)
			}
		}
	}
}

// 「—」表示该时代无对应写法：只禁用、不给替代，writer 不该被误导去猜。
func TestParseEraTerminologyDashMeansNoAlternative(t *testing.T) {
	for _, p := range ParseEraTerminology(sampleTable, "东汉末年") {
		if p.Banned == "玻璃" {
			if len(p.Use) != 0 {
				t.Errorf("破折号条目不应有替代项，实际 %v", p.Use)
			}
			if p.Note == "" {
				t.Error("应有理由说明")
			}
			return
		}
	}
	t.Error("未解析出「玻璃」条目")
}

func TestParseEraTerminologyDedups(t *testing.T) {
	dup := sampleTable + "\n- **相公** → 别的写法 | 重复条目\n"
	ps := ParseEraTerminology(dup, "东汉末年")
	n := 0
	for _, p := range ps {
		if p.Banned == "相公" {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("同词应去重，实际出现 %d 次", n)
	}
}

func TestLoadEraProposalsFromTable(t *testing.T) {
	doc, err := LoadEraProposalsFromTable(EraStaticTable{Content: sampleTable}, "东汉末年")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if doc.Source != "static_table" {
		t.Errorf("来源应为 static_table，实际 %q", doc.Source)
	}
	if doc.Era != "东汉末年" {
		t.Errorf("朝代应记录在案，实际 %q", doc.Era)
	}
	for _, p := range doc.Proposals {
		if p.Source != "static_table" || p.Decision != store.EraDecisionPending {
			t.Errorf("候选应标为 static_table/pending: %+v", p)
		}
	}
	if _, err := LoadEraProposalsFromTable(EraStaticTable{Content: sampleTable}, ""); err == nil {
		t.Error("空朝代应报错")
	}
	if _, err := LoadEraProposalsFromTable(EraStaticTable{}, "汉"); err == nil {
		t.Error("空表应报错，不能静默产出空候选")
	}
	if _, err := LoadEraProposalsFromTable(EraStaticTable{Content: "# 只有标题\n"}, "汉"); err == nil {
		t.Error("解析不出条目时应报错")
	}
}

// 静态表产出候选 → 采纳 → 机械检查命中并带替代项，且不误伤可用词。
func TestStaticTableEndToEnd(t *testing.T) {
	st := store.NewStore(t.TempDir())
	doc, err := LoadEraProposalsFromTable(EraStaticTable{Content: sampleTable}, "东汉末年")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if err := st.EraProposals.Save(doc); err != nil {
		t.Fatalf("Save: %v", err)
	}
	for _, w := range []string{"相公", "科举"} {
		if _, err := AdoptProposal(st, w); err != nil {
			t.Fatalf("采纳 %s: %v", w, err)
		}
	}
	snap, err := st.UserRules.Load()
	if err != nil || snap == nil {
		t.Fatalf("Load: %v", err)
	}
	vs := rules.Check("「相公，此番科举若行，天下震动。」", snap.Structured)
	if len(vs) != 2 {
		t.Fatalf("应检出 2 条，实际 %d: %+v", len(vs), vs)
	}
	for _, v := range vs {
		if len(v.Suggestion) == 0 {
			t.Errorf("%s 应带替代项", v.Target)
		}
	}
	for _, ok := range []string{"太守", "刺史", "陛下"} {
		if n := rules.Check(ok+"在此", snap.Structured); len(n) > 0 {
			t.Errorf("误伤可用词 %q: %+v", ok, n)
		}
	}
}

// 静态表候选同样不进 user_rules，仍需逐条采纳。
func TestStaticTableProposalsNeedAdoption(t *testing.T) {
	st := store.NewStore(t.TempDir())
	doc, _ := LoadEraProposalsFromTable(EraStaticTable{Content: sampleTable}, "东汉末年")
	if err := st.EraProposals.Save(doc); err != nil {
		t.Fatal(err)
	}
	snap, _ := st.UserRules.Load()
	if snap != nil {
		t.Fatalf("仅生成候选时不应写 user_rules: %+v", snap)
	}
	if n := len(rules.Check("相公在此", rules.Structured{})); n != 0 {
		t.Fatalf("未采纳前不应有任何检查命中: %d", n)
	}
}

// 用户覆盖里写「相公（可用）」必须能解封内置的禁用项。解析按词去重取首条，
// 不做两遍扫描的话内置禁用项会赢——用户写了不生效、也不报错，静默失效。
func TestParseEraTerminologyUserOverrideCanUnban(t *testing.T) {
	override := "\n## 本书专用\n\n- **相公** → 相公（可用）\n- **大人** → 大人（可用）\n"
	ps := ParseEraTerminology(sampleTable+override, "东汉末年")
	for _, p := range ps {
		if p.Banned == "相公" || p.Banned == "大人" {
			t.Errorf("%s 应被用户覆盖解封，实际仍为禁用项（use=%v）", p.Banned, p.Use)
		}
	}
	for _, p := range ps {
		if p.Banned == "科举" {
			return // 未被解封的其他禁用项应保留
		}
	}
	t.Error("未被解封的禁用项不应被一并丢弃")
}

// 维护文档常在表文件里带示例。示例行若被当数据收进候选，会凭空产生
// 「相国」这类禁用项并被误采纳。围栏代码块必须跳过。
func TestParseEraTerminologySkipsFencedBlocks(t *testing.T) {
	withDoc := sampleTable + `
## 本书专用词条

格式说明：

` + "```markdown" + `
- **相国** → 丞相 / 国相
- **天工阁** → 天工阁（可用）
` + "```" + `

- **绸缎** → 绢 / 缣
`
	ps := ParseEraTerminology(withDoc, "东汉末年")
	for _, p := range ps {
		switch p.Banned {
		case "相国", "天工阁":
			t.Errorf("围栏内的示例 %q 不应成为候选", p.Banned)
		case "禁用词", "词":
			t.Errorf("文档说明行不应成为候选: %q", p.Banned)
		}
	}
	found := false
	for _, p := range ps {
		if p.Banned == "绸缎" {
			found = true
		}
	}
	if !found {
		t.Error("围栏外的真实条目应正常解析")
	}
}
