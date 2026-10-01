package userrules

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/voocel/ainovel-cli/internal/domain"
	"github.com/voocel/ainovel-cli/internal/rules"
	"github.com/voocel/ainovel-cli/internal/store"
)

// sweepStore 造一本有 n 章正文的书，并给每章落一份接纳记录。
// completed 非空时同步写 progress.CompletedChapters——Sweep 以它为扫描范围。
func sweepStore(t *testing.T, contents map[int]string, completed []int) *store.Store {
	t.Helper()
	st := store.NewStore(t.TempDir())
	total := len(contents)
	if len(completed) > total {
		total = len(completed)
	}
	if err := st.Progress.Init(total); err != nil {
		t.Fatalf("Init: %v", err)
	}
	for ch, content := range contents {
		if _, err := st.ChapterRecords.Accept(ch, domain.ChapterOriginGenerated, content,
			domain.ChapterFacts{Title: "第" + itoa(ch) + "章"}, domain.StyleDelta{}); err != nil {
			t.Fatalf("Accept(%d): %v", ch, err)
		}
	}
	// MarkChapterComplete 才是登记已完成章的正路（同时推进 current_chapter）。
	for _, ch := range completed {
		if err := st.Progress.MarkChapterComplete(ch, 1000, "", ""); err != nil {
			t.Fatalf("MarkChapterComplete(%d): %v", ch, err)
		}
	}
	return st
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

// 核心用例：规则在中途加入时，已落盘的存量章节必须被扫出来。
// 没有这个能力，用户只能手工 grep 才知道哪些章已经违规。
func TestSweepFindsStoredChaptersViolatingLaterRule(t *testing.T) {
	st := sweepStore(t, map[int]string{
		1: "沈砚在灯下读简。",
		2: "「沈相公，这册子谁送来的？」",
		3: "他提笔批了三个字。",
		4: "「相公，一千亩庄田，来年少了两成。」",
	}, []int{1, 2, 3, 4})

	// 规则是"后来"才加的：先扫一次确认当时干净。
	snap := &rules.Snapshot{Version: rules.SnapshotVersion, Status: rules.StatusReady}
	before, err := Sweep(st, snap)
	if err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if len(before.Chapters) != 0 {
		t.Fatalf("加入规则前不应有违规: %+v", before.Chapters)
	}

	// 加规则后再扫。
	snap.Structured.TermCorrections = []rules.TermCorrection{
		{Banned: "沈相公", Use: []string{"郎君", "先生"}, Note: "明清通行，东汉无"},
	}
	after, err := Sweep(st, snap)
	if err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if len(after.Chapters) != 1 || after.Chapters[0].Chapter != 2 {
		t.Fatalf("应只命中第 2 章，实际 %+v", after.Chapters)
	}
	if after.Scanned != 4 {
		t.Errorf("应扫描 4 章，实际 %d", after.Scanned)
	}
	if after.Total() != 1 {
		t.Errorf("应 1 处违规，实际 %d", after.Total())
	}
	v := after.Chapters[0].Violations[0]
	if len(v.Suggestion) != 2 || v.Suggestion[0] != "郎君" {
		t.Errorf("扫描结果应带替代项，供 writer 直接采用: %+v", v)
	}
	if got := after.AffectedChapters(); len(got) != 1 || got[0] != 2 {
		t.Errorf("AffectedChapters 应供 /rework 取用: %v", got)
	}
}

// 裸"相公"是这类错误的典型形态：用户只禁了"沈相公"，全书却散落 130+ 处裸"相公"。
// 加了对照项后应一并命中，而不是只认带姓名的那个词。
func TestSweepCatchesBareTermBeyondTheBannedVariant(t *testing.T) {
	st := sweepStore(t, map[int]string{
		1: "「相公，契书按不按？」",
	}, []int{1})
	snap := &rules.Snapshot{Status: rules.StatusReady, Structured: rules.Structured{
		TermCorrections: []rules.TermCorrection{{Banned: "相公", Use: []string{"郎君", "君"}}},
	}}
	got, err := Sweep(st, snap)
	if err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if len(got.Chapters) != 1 {
		t.Fatalf("应命中裸「相公」: %+v", got.Chapters)
	}
	if n := got.Chapters[0].Violations[0].Actual; n != 1 {
		t.Errorf("出现次数应为 1，实际 %v", n)
	}
}

// 扫描是纯读取：不得改写正文、不得动 review、不得进 PendingRewrites。
func TestSweepIsReadOnly(t *testing.T) {
	st := sweepStore(t, map[int]string{1: "「沈相公。」"}, []int{1})
	snap := &rules.Snapshot{Status: rules.StatusReady, Structured: rules.Structured{
		TermCorrections: []rules.TermCorrection{{Banned: "沈相公", Use: []string{"郎君"}}},
	}}
	if _, err := Sweep(st, snap); err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	rec, err := st.ChapterRecords.Load(1)
	if err != nil || rec == nil {
		t.Fatalf("扫描后章节记录应仍在: %v", err)
	}
	if rec.Content != "「沈相公。」" {
		t.Errorf("扫描不得改写正文，实际 %q", rec.Content)
	}
	p, err := st.Progress.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if p != nil && len(p.PendingRewrites) != 0 {
		t.Errorf("扫描不得自行入队（改写须走 editor 裁定）: %v", p.PendingRewrites)
	}
	if p != nil && p.ReworkPass != nil && p.ReworkPass.Active() {
		t.Error("扫描不得自行开启返工 pass")
	}
}

// Lint 也要回扫：正文里的 ** 加粗在导出 txt 时会裸露符号，而 commit 期的提示早滚过去了。
func TestSweepAlsoRunsProductLint(t *testing.T) {
	st := sweepStore(t, map[int]string{1: "沈砚**顿住了**。"}, []int{1})
	got, err := Sweep(st, &rules.Snapshot{Status: rules.StatusReady, Structured: rules.Structured{
		ForbiddenPhrases: []string{"毫无"},
	}})
	if err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if len(got.Chapters) != 1 {
		t.Fatalf("markdown 残留应被回扫命中: %+v", got.Chapters)
	}
	found := false
	for _, v := range got.Chapters[0].Violations {
		if v.Rule == "markdown_residue" {
			found = true
		}
	}
	if !found {
		t.Errorf("未见 markdown_residue: %+v", got.Chapters[0].Violations)
	}
}

// 一章记录损坏时该章跳过，其余章仍要出结论——不能让一章坏数据掩盖全书。
func TestSweepSkipsCorruptChapterRecord(t *testing.T) {
	dir := t.TempDir()
	st := store.NewStore(dir)
	if err := st.Progress.Init(2); err != nil {
		t.Fatalf("Init: %v", err)
	}
	for ch := 1; ch <= 2; ch++ {
		if _, err := st.ChapterRecords.Accept(ch, domain.ChapterOriginGenerated, "「相公。」",
			domain.ChapterFacts{Title: "第" + itoa(ch) + "章"}, domain.StyleDelta{}); err != nil {
			t.Fatalf("Accept(%d): %v", ch, err)
		}
		if err := st.Progress.MarkChapterComplete(ch, 1000, "", ""); err != nil {
			t.Fatalf("MarkChapterComplete(%d): %v", ch, err)
		}
	}
	// 直接把第 2 章的记录文件写成非法 JSON，模拟落盘损坏。
	bad := filepath.Join(dir, store.ChapterRecordPath(2))
	if err := os.WriteFile(bad, []byte("{ 这不是 JSON"), 0o644); err != nil {
		t.Fatalf("写入损坏文件: %v", err)
	}

	got, err := Sweep(st, &rules.Snapshot{Status: rules.StatusReady, Structured: rules.Structured{
		TermCorrections: []rules.TermCorrection{{Banned: "相公"}},
	}})
	if err != nil {
		t.Fatalf("单章损坏不应让整体失败: %v", err)
	}
	if got.Scanned != 1 {
		t.Errorf("应只扫描 1 章（损坏章跳过），实际 %d", got.Scanned)
	}
	if len(got.Chapters) != 1 || got.Chapters[0].Chapter != 1 {
		t.Fatalf("应仍报出第 1 章的违规: %+v", got.Chapters)
	}
}

func TestSweepNoChapters(t *testing.T) {
	st := sweepStore(t, map[int]string{1: "正文"}, nil)
	got, err := Sweep(st, nil)
	if err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if got.Scanned != 0 || len(got.Chapters) != 0 {
		t.Errorf("无完成章时应为空结果: %+v", got)
	}
}

// 快照缺失时回落到 system_defaults，而不是给出一个虚假的"全清"结论。
func TestSweepFallsBackToSystemDefaults(t *testing.T) {
	st := sweepStore(t, map[int]string{1: "他若有所思，某种程度上。"}, []int{1})
	got, err := Sweep(st, nil)
	if err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if got.RuleCount == 0 {
		t.Error("回落快照仍应带 system_defaults 规则数")
	}
	if len(got.Chapters) == 0 {
		t.Errorf("system_defaults 的禁用短语应生效: %+v", got)
	}
}

// 结果按章号升序：供 /rework 直接粘贴，必须稳定有序。
func TestSweepResultSortedByChapter(t *testing.T) {
	c := map[int]string{}
	completed := []int{}
	for i := 10; i >= 1; i-- {
		c[i] = "「相公。」"
		completed = append(completed, i)
	}
	st := sweepStore(t, c, completed)
	got, err := Sweep(st, &rules.Snapshot{Status: rules.StatusReady, Structured: rules.Structured{
		TermCorrections: []rules.TermCorrection{{Banned: "相公"}},
	}})
	if err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if len(got.Chapters) != 10 {
		t.Fatalf("应 10 章命中，实际 %d", len(got.Chapters))
	}
	for i, cv := range got.Chapters {
		if cv.Chapter != i+1 {
			t.Fatalf("第 %d 项章号为 %d，应升序", i, cv.Chapter)
		}
	}
}
