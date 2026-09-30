package corpus

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestScanRejectsEmptyAndMissingRoot(t *testing.T) {
	if _, err := Scan("   "); err == nil || !strings.Contains(err.Error(), "source dir is required") {
		t.Fatalf("empty root: %v", err)
	}
	missing := filepath.Join(t.TempDir(), "nope")
	if _, err := Scan(missing); err == nil || !strings.Contains(err.Error(), "corpus directory not found") {
		t.Fatalf("missing root: %v", err)
	}
}

func TestScanRejectsNonDirectoryRoot(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "a.txt")
	write(t, file, "x")
	if _, err := Scan(file); err == nil || !strings.Contains(err.Error(), "not a directory") {
		t.Fatalf("file root: %v", err)
	}
}

func TestScanFiltersExtensionsAndSorts(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "b.txt"), "bbb")
	write(t, filepath.Join(dir, "a.md"), "aaa")
	write(t, filepath.Join(dir, "c.markdown"), "ccc")
	write(t, filepath.Join(dir, "skip.json"), "{}")
	write(t, filepath.Join(dir, "skip.pdf"), "%PDF")
	// 嵌套目录也要被扫到，且相对路径用斜杠
	write(t, filepath.Join(dir, "sub", "d.txt"), "ddd")

	got, err := Scan(dir)
	if err != nil {
		t.Fatal(err)
	}
	var paths []string
	for _, s := range got {
		paths = append(paths, s.RelativePath)
	}
	want := []string{"a.md", "b.txt", "c.markdown", "sub/d.txt"}
	if len(paths) != len(want) {
		t.Fatalf("paths = %v, want %v", paths, want)
	}
	for i := range want {
		if paths[i] != want[i] {
			t.Fatalf("paths = %v, want %v", paths, want)
		}
	}
}

func TestScanComputesStableFingerprint(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "a.txt"), "hello")

	first, err := Scan(dir)
	if err != nil {
		t.Fatal(err)
	}
	second, err := Scan(dir)
	if err != nil {
		t.Fatal(err)
	}
	if first[0].Fingerprint != second[0].Fingerprint {
		t.Fatalf("fingerprint unstable: %q vs %q", first[0].Fingerprint, second[0].Fingerprint)
	}
	if first[0].Fingerprint != Fingerprint("a.txt", first[0].SHA256) {
		t.Fatalf("fingerprint mismatch: %q", first[0].Fingerprint)
	}

	// 内容变了必须换指纹，否则增量更新会漏掉变更的语料
	write(t, filepath.Join(dir, "a.txt"), "hello world")
	changed, err := Scan(dir)
	if err != nil {
		t.Fatal(err)
	}
	if changed[0].Fingerprint == first[0].Fingerprint {
		t.Fatal("fingerprint must change when content changes")
	}
}

func TestScanReportsSizeAndDecodesContent(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "a.txt"), "中文语料")

	got, err := Scan(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got[0].Content != "中文语料" {
		t.Fatalf("content = %q", got[0].Content)
	}
	if got[0].SizeBytes != int64(len("中文语料")) {
		t.Fatalf("size = %d", got[0].SizeBytes)
	}
	if got[0].ModTime == "" {
		t.Fatal("mod time must be set")
	}
}
