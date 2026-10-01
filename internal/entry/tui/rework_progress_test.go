package tui

import (
	"strings"
	"testing"

	"github.com/voocel/ainovel-cli/internal/domain"
)

func TestReworkProgressLabel(t *testing.T) {
	cases := []struct {
		name string
		pass *domain.ReworkPass
		want []string
	}{
		{"刚开始", &domain.ReworkPass{StartChapter: 1, EndChapter: 100, Cursor: 1},
			[]string{"进行中", "0/100", "未开始"}},
		{"进行中", &domain.ReworkPass{StartChapter: 1, EndChapter: 100, Cursor: 4, Reviewed: 3, Skipped: 2, Rewritten: []int{3}},
			[]string{"3/100", "返工 1", "通过 2"}},
		{"已完成", &domain.ReworkPass{StartChapter: 1, EndChapter: 6, Cursor: 7, Reviewed: 6, Skipped: 5, Rewritten: []int{2}},
			[]string{"已完成", "6/6"}},
	}
	for _, c := range cases {
		got := reworkProgressLabel(c.pass)
		for _, w := range c.want {
			if !strings.Contains(got, w) {
				t.Errorf("%s: 缺 %q，实际 %q", c.name, w, got)
			}
		}
	}
}
