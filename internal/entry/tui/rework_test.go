package tui

import (
	"strings"
	"testing"

	"github.com/voocel/ainovel-cli/internal/domain"
)

func TestParseReworkRange(t *testing.T) {
	cases := []struct {
		in        string
		wantStart int
		wantEnd   int
		wantErr   bool
		errSubstr string
	}{
		{in: "50", wantStart: 50, wantEnd: 50},
		{in: "1-112", wantStart: 1, wantEnd: 112},
		{in: " 3 - 9 ", wantStart: 3, wantEnd: 9},
		{in: "7-7", wantStart: 7, wantEnd: 7},
		{in: "", wantErr: true, errSubstr: "缺少"},
		{in: "abc", wantErr: true, errSubstr: "无法解析"},
		{in: "a-b", wantErr: true, errSubstr: "无法解析"},
		{in: "1-", wantErr: true, errSubstr: "无法解析"},
		{in: "-5", wantErr: true, errSubstr: "无法解析"},
	}
	for _, c := range cases {
		start, end, err := parseReworkRange(c.in)
		if c.wantErr {
			if err == nil {
				t.Errorf("%q: 期望报错，实际通过 (%d-%d)", c.in, start, end)
			} else if c.errSubstr != "" && !strings.Contains(err.Error(), c.errSubstr) {
				t.Errorf("%q: 错误信息应含 %q，实际 %q", c.in, c.errSubstr, err.Error())
			}
			continue
		}
		if err != nil {
			t.Errorf("%q: 不应报错，实际 %v", c.in, err)
			continue
		}
		if start != c.wantStart || end != c.wantEnd {
			t.Errorf("%q: 期望 %d-%d，实际 %d-%d", c.in, c.wantStart, c.wantEnd, start, end)
		}
	}
}

// 112 章会刷屏，列表必须压成区间形式。
func TestFormatChapterListCompresses(t *testing.T) {
	got := formatChapterList([]int{1, 2, 3, 7, 12, 13, 14, 100})
	want := "1-3, 7, 12-14, 100"
	if got != want {
		t.Fatalf("期望 %q，实际 %q", want, got)
	}
	if got := formatChapterList(nil); got != "" {
		t.Fatalf("空列表应返回空串，实际 %q", got)
	}
	// 乱序输入也要正确
	if got := formatChapterList([]int{14, 12, 13, 1}); got != "1, 12-14" {
		t.Fatalf("乱序输入应归并为 %q，实际 %q", "1, 12-14", got)
	}
}

func TestFormatReworkStatus(t *testing.T) {
	if got := formatReworkStatus(nil); !strings.Contains(got, "没有返工记录") {
		t.Fatalf("nil 应给引导，实际 %q", got)
	}
	active := &domain.ReworkPass{
		StartChapter: 1, EndChapter: 112, Cursor: 5,
		Reviewed: 4, Rewritten: []int{2, 4}, Skipped: 2,
	}
	got := formatReworkStatus(active)
	for _, want := range []string{"进行中", "第 1-112 章", "共 112 章", "游标 5", "已评审 4", "已返工 2", "评审通过 2", "2, 4"} {
		if !strings.Contains(got, want) {
			t.Errorf("状态输出应含 %q，实际 %q", want, got)
		}
	}
	done := &domain.ReworkPass{
		StartChapter: 1, EndChapter: 3, Cursor: 4,
		Reviewed: 3, Rewritten: []int{1}, Skipped: 2,
	}
	if got := formatReworkStatus(done); !strings.Contains(got, "已完成") {
		t.Errorf("游标越界应显示已完成，实际 %q", got)
	}
}

func TestReworkEstimateRewrites(t *testing.T) {
	// 保守 20% 估算，单章至少估 1
	cases := map[int]int{1: 1, 5: 1, 112: 22, 10: 2, 0: 0}
	for total, want := range cases {
		if got := reworkEstimateRewrites(total); got != want {
			t.Errorf("total=%d 期望 %d，实际 %d", total, want, got)
		}
	}
}
