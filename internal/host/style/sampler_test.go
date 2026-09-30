package style

import (
	"strings"
	"testing"

	"github.com/voocel/ainovel-cli/internal/host/corpus"
)

func TestSampleSourceContentTakesHeadOnly(t *testing.T) {
	long := strings.Repeat("甲", maxSourceRunes*2)
	got := sampleSourceContent(long)
	if n := len([]rune(got)); n != maxSourceRunes {
		t.Fatalf("runes = %d, want %d", n, maxSourceRunes)
	}
	// 只取头部：结尾内容必须已被丢弃
	if !strings.HasSuffix(got, "甲") || strings.Contains(got, "[...truncated...]") {
		t.Fatal("head-only sampling must not append a truncation marker")
	}
}

func TestSampleSourceContentKeepsShortTextIntact(t *testing.T) {
	short := "很短的一段语料"
	if got := sampleSourceContent(short); got != short {
		t.Fatalf("got %q, want %q", got, short)
	}
}

func TestSampleSourceContentExactBoundary(t *testing.T) {
	exact := strings.Repeat("甲", maxSourceRunes)
	if got := sampleSourceContent(exact); got != exact {
		t.Fatal("text at exactly the cap must be returned unchanged")
	}
}

func TestPendingSourcesReturnsAllWhenNoBaseline(t *testing.T) {
	sources := []corpus.Source{{RelativePath: "a.txt", Fingerprint: "a"}}
	if got := pendingSources(nil, sources); len(got) != 1 {
		t.Fatalf("len = %d, want 1", len(got))
	}
}

func TestPendingSourcesSkipsKnownFingerprints(t *testing.T) {
	baseline := existingSkills("a.txt", "sha-a")
	sources := []corpus.Source{
		{RelativePath: "a.txt", Fingerprint: "a.txt:sha-a"},
		{RelativePath: "b.txt", Fingerprint: "b.txt:sha-b"},
	}
	got := pendingSources(baseline, sources)
	if len(got) != 1 || got[0].RelativePath != "b.txt" {
		t.Fatalf("pending = %+v, want only b.txt", got)
	}
}
