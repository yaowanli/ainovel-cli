package assets

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 时代称谓表必须支持三层覆盖：本表无法预置主角封号、军中互称、虚构门派叫法，
// 没有覆盖能力用户就只能改代码。
func TestEraTerminologyThreeLayerOverride(t *testing.T) {
	home := t.TempDir()
	book := t.TempDir()
	mustWrite := func(dir, body string) {
		if err := os.WriteFile(filepath.Join(dir, "era-terminology.md"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	mustWrite(home, "- **梓潼** → 梓潼（可用）\n")
	mustWrite(book, "- **鹤鸣山** → 鹤鸣山（可用）\n- **相国** → 丞相 / 国相\n")

	got := Load("history", LoadOptions{HomeStyleDir: home, BookStyleDir: book}).References.EraTerminology

	for _, want := range []string{"相公", "梓潼", "鹤鸣山", "相国"} {
		if !strings.Contains(got, want) {
			t.Errorf("三层覆盖后应含 %q", want)
		}
	}
	if strings.Index(got, "梓潼") > strings.Index(got, "鹤鸣山") {
		t.Error("本书覆盖应排在全局覆盖之后")
	}
}

// 无覆盖时必须逐字节等于内置，否则「主题包缺失时静默回落」的老问题会重现。
func TestEraTerminologyNoOverride(t *testing.T) {
	plain := Load("history", LoadOptions{}).References.EraTerminology
	if strings.TrimSpace(plain) == "" {
		t.Fatal("内置 era 表不应为空")
	}
	empty := Load("history", LoadOptions{HomeStyleDir: t.TempDir(), BookStyleDir: t.TempDir()})
	if empty.References.EraTerminology != plain {
		t.Error("空覆盖目录不应改变内置表")
	}
}

// 非 history 题材没有内置 era 表，必须留空而不是回落到 history 的表——
// 让仙侠拿到汉代称谓表比拿不到更糟。
func TestEraTerminologyAbsentForOtherStyles(t *testing.T) {
	for _, style := range []string{"default", "xianxia", "urban", "nonexistent-style"} {
		if got := Load(style, LoadOptions{}).References.EraTerminology; strings.TrimSpace(got) != "" {
			t.Errorf("style=%s 不应带 era 表，实际 %d 字节", style, len(got))
		}
	}
}

// 有覆盖但没有内置表时，覆盖内容仍应生效（用户自带 era 表）。
func TestEraTerminologyOverrideWithoutBuiltin(t *testing.T) {
	book := t.TempDir()
	if err := os.WriteFile(filepath.Join(book, "era-terminology.md"),
		[]byte("- **飞剑** → 御剑 / 剑遁\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := Load("xianxia", LoadOptions{BookStyleDir: book}).References.EraTerminology
	if !strings.Contains(got, "飞剑") {
		t.Errorf("用户自带 era 表应生效，实际 %q", got)
	}
}
