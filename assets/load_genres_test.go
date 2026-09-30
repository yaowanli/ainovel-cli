package assets

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
)

// 内置题材包必须真的能被 loadReferences 取到。此前未知题材静默回落，
// 题材包写错了也没人发现——这个测试防止 genre 包变成"目录摆设"。
// 数据驱动：新增题材目录后无需改测试，缺文件会立刻红。
func TestBuiltinGenrePacksAreComplete(t *testing.T) {
	genres := listGenres()
	if len(genres) == 0 {
		t.Fatal("内置题材列表为空")
	}
	for _, style := range genres {
		refs := loadReferences(style, LoadOptions{})
		if strings.TrimSpace(refs.StyleReference) == "" {
			t.Errorf("题材 %q 缺少 style-references.md，load 后为空", style)
		}
		if strings.TrimSpace(refs.ArcTemplates) == "" {
			t.Errorf("题材 %q 缺少 arc-templates.md，load 后为空", style)
		}
	}
}

// 通用题材下这两个字段必须为空：确认它们确实只由题材包驱动，不是别处兜了底。
func TestDefaultStyleHasNoGenreTemplates(t *testing.T) {
	refs := loadReferences("default", LoadOptions{})
	if refs.StyleReference != "" || refs.ArcTemplates != "" {
		t.Fatal("default 题材不应带题材专属模板")
	}
}

// 未知题材必须告警并列出可选项。这是本次修复的核心：写历史的人必须知道
// 自己没拿到历史模板，否则会以为"系统对历史题材支持不好"而默默用通用规则硬写。
func TestUnknownStyleWarnsAndListsAvailableGenres(t *testing.T) {
	var buf bytes.Buffer
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	defer slog.SetDefault(old)

	loadReferences("no-such-genre", LoadOptions{})

	got := buf.String()
	if !strings.Contains(got, "no-such-genre") {
		t.Fatalf("告警未提及未知题材名: %q", got)
	}
	for _, want := range listGenres() {
		if !strings.Contains(got, want) {
			t.Errorf("告警未列出可用题材 %q: %q", want, got)
		}
	}
}

// 每个内置题材包都要同时有 styles/<name>.md，否则 Architect 拿不到风格摘要。
// 这两处资产是分开的，历史上很容易只加一边。
func TestEveryGenreHasMatchingStyleFile(t *testing.T) {
	styles := loadStyles(LoadOptions{})
	for _, g := range listGenres() {
		if strings.TrimSpace(styles[g]) == "" {
			t.Errorf("题材 %q 没有对应的 styles/%s.md", g, g)
		}
	}
}
