package style

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/voocel/ainovel-cli/internal/domain"
	"github.com/voocel/ainovel-cli/internal/host/corpus"
	"github.com/voocel/ainovel-cli/internal/llmcontract"
)

// Report 是单篇语料的分析结果，只作为 merge 的输入，不落盘
// （仿写画像的 SourceReports 在运行时就是死数据，skill 不重复这个浪费）。
type Report struct {
	RelativePath       string   `json:"relative_path"`
	SHA256             string   `json:"sha256"`
	Fingerprint        string   `json:"fingerprint"`
	Summary            string   `json:"summary"`
	NarrativeVoice     []string `json:"narrative_voice"`
	SentenceRhythm     []string `json:"sentence_rhythm"`
	DescriptionTexture []string `json:"description_texture"`
	Pacing             []string `json:"pacing"`
	SpeechTags         []string `json:"speech_tags"`
	FormsOfAddress     []string `json:"forms_of_address"`
	ModalParticles     []string `json:"modal_particles"`
	VerbalTics         []string `json:"verbal_tics"`
	LineLength         []string `json:"line_length"`
	Dialect            []string `json:"dialect"`
	Taboos             []string `json:"taboos"`
}

// Run 扫描语料并生成或增量更新风格 skill。
func Run(ctx context.Context, deps Deps, opts Options) (<-chan Event, error) {
	if deps.Store == nil || deps.LLM == nil {
		return nil, fmt.Errorf("deps incomplete")
	}
	if strings.TrimSpace(opts.SourceDir) == "" {
		return nil, fmt.Errorf("source dir is required")
	}

	events := make(chan Event, 32)
	go func() {
		defer close(events)
		// emit 优先投递，只有缓冲区满时才用 ctx 兜底：单层 select 在 ctx 已取消且
		// 缓冲区有空间时两个 case 同时就绪，Go 随机选，会把 done/error 这类终态
		// 事件静默丢掉——调用方等不到收尾，面板就一直挂在"运行中"。
		emit := func(stage Stage, current, total int, msg string, err error) {
			ev := Event{Time: time.Now(), Stage: stage, Current: current, Total: total, Message: msg, Err: err}
			select {
			case events <- ev:
				return
			default:
			}
			select {
			case events <- ev:
			case <-ctx.Done():
			}
		}

		emit(StageScan, 0, 0, "扫描参考语料...", nil)
		sources, err := corpus.Scan(opts.SourceDir)
		if err != nil {
			emit(StageError, 0, 0, "扫描语料目录失败", err)
			return
		}
		if len(sources) == 0 {
			emit(StageError, 0, 0, "语料目录中没有可分析的 .txt/.md/.markdown 文件", fmt.Errorf("no style sources"))
			return
		}

		existing, err := deps.Store.StyleSkills.Load()
		if err != nil {
			emit(StageError, 0, len(sources), "读取既有风格 skill 失败", err)
			return
		}
		pending := pendingSources(existing, sources)
		if len(pending) == 0 {
			emit(StageDone, 0, len(sources), "风格 skill 已是最新，未发现新增或变更语料", nil)
			return
		}

		reports := make([]Report, 0, len(pending))
		for i, source := range pending {
			if err := ctx.Err(); err != nil {
				emit(StageError, i, len(pending), "用户取消风格分析", err)
				return
			}
			emit(StageAnalyze, i+1, len(pending), fmt.Sprintf("分析语料 %d/%d：%s", i+1, len(pending), source.RelativePath), nil)
			report, err := AnalyzeSource(ctx, deps.LLM, deps.Prompts.Source, source)
			if err != nil {
				emit(StageError, i+1, len(pending), "语料分析失败", err)
				return
			}
			reports = append(reports, *report)
		}

		emit(StageMerge, len(pending), len(pending), "蒸馏风格 skill...", nil)
		skills, err := MergeSkills(ctx, deps.LLM, deps.Prompts.Merge, existing, reports)
		if err != nil {
			emit(StageError, len(pending), len(pending), "风格 skill 合并失败", err)
			return
		}
		profile := buildSkills(existing, opts.SourceDir, pending, *skills, time.Now())
		if err := deps.Store.StyleSkills.Save(*profile); err != nil {
			emit(StageError, len(pending), len(pending), "保存风格 skill 失败", err)
			return
		}
		emit(StageDone, len(pending), len(pending), fmt.Sprintf("风格 skill 已更新：新增/变更 %d 篇，累计 %d 篇", len(pending), len(profile.Corpus.Sources)), nil)
	}()
	return events, nil
}

func AnalyzeSource(ctx context.Context, llm LLMChat, systemPrompt string, source corpus.Source) (*Report, error) {
	if strings.TrimSpace(systemPrompt) == "" {
		return nil, fmt.Errorf("source prompt is required")
	}
	report, err := generateStructured(ctx, llm, sourceReportContract, systemPrompt, buildSourceUserPrompt(source), func(report *Report) error {
		if strings.TrimSpace(report.Summary) == "" {
			return fmt.Errorf("summary is required")
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("parse source report %s: %w", source.RelativePath, err)
	}
	report.RelativePath = source.RelativePath
	report.SHA256 = source.SHA256
	report.Fingerprint = source.Fingerprint
	return &report, nil
}

// MergeSkills 蒸馏：多篇报告直接汇总只会得到清单噪音，跨篇去重并收敛为
// 可执行条目才有 skill 语义，所以这一步不能省。
func MergeSkills(ctx context.Context, llm LLMChat, systemPrompt string, existing *domain.StyleSkills, reports []Report) (*domain.StyleSkills, error) {
	if strings.TrimSpace(systemPrompt) == "" {
		return nil, fmt.Errorf("merge prompt is required")
	}
	skills, err := generateStructured[domain.StyleSkills](ctx, llm, skillsContract, systemPrompt, buildMergeUserPrompt(existing, reports), nil)
	if err != nil {
		return nil, fmt.Errorf("parse style skills: %w", err)
	}
	return &skills, nil
}

func generateStructured[T any](ctx context.Context, model LLMChat, contract llmcontract.Contract, systemPrompt, payload string, validate func(*T) error) (T, error) {
	out, err := llmcontract.Execute(ctx, model, llmcontract.Request[T]{
		Contract:     contract,
		SystemPrompt: systemPrompt,
		Payload:      payload,
		Validate:     validate,
		Agent:        "style_skills",
		Hooks: llmcontract.Hooks{
			Resolved: func(res llmcontract.Resolution) {
				slog.Debug("风格 skill 结构化协议选择", "contract", contract.Name,
					"structured_mode", res.Mode, "capability_source", res.Source,
					"provider", res.Provider, "model", res.Model,
					"schema_fingerprint", contract.Fingerprint())
			},
			Correction: func(ev llmcontract.Correction) {
				slog.Warn("风格 skill 输出自愈", "contract", contract.Name, "attempt", ev.Attempt,
					"layer", ev.Layer, "structured_mode", ev.Mode, "err", ev.Err)
			},
		},
	})
	if err != nil {
		return out, fmt.Errorf("structured generation: %w", err)
	}
	return out, nil
}

func pendingSources(existing *domain.StyleSkills, sources []corpus.Source) []corpus.Source {
	if existing == nil {
		return sources
	}
	known := make(map[string]struct{}, len(existing.Corpus.Sources))
	for _, source := range existing.Corpus.Sources {
		known[domain.SourceFingerprint(source.RelativePath, source.SHA256)] = struct{}{}
	}
	var pending []corpus.Source
	for _, source := range sources {
		if _, ok := known[source.Fingerprint]; ok {
			continue
		}
		pending = append(pending, source)
	}
	return pending
}

func buildSkills(
	existing *domain.StyleSkills,
	sourceDir string,
	pending []corpus.Source,
	skills domain.StyleSkills,
	now time.Time,
) *domain.StyleSkills {
	stamp := now.Format(time.RFC3339)
	out := domain.StyleSkills{
		Version:   domain.StyleSkillsVersion,
		CreatedAt: stamp,
		UpdatedAt: stamp,
		Corpus: domain.StyleSkillsCorpus{
			SourceDir: filepath.ToSlash(sourceDir),
		},
		Prose:    skills.Prose,
		Dialogue: skills.Dialogue,
		Taboos:   skills.Taboos,
	}
	if existing != nil {
		out.CreatedAt = existing.CreatedAt
		if out.CreatedAt == "" {
			out.CreatedAt = stamp
		}
		out.Corpus.Sources = append(out.Corpus.Sources, existing.Corpus.Sources...)
	}
	for _, source := range pending {
		out.Corpus.Sources = replaceSourceByPath(out.Corpus.Sources, domain.StyleSkillsSource{
			RelativePath: source.RelativePath,
			SHA256:       source.SHA256,
			Fingerprint:  source.Fingerprint,
			SizeBytes:    source.SizeBytes,
			ModTime:      source.ModTime,
			AnalyzedAt:   stamp,
		})
	}
	sort.Slice(out.Corpus.Sources, func(i, j int) bool {
		if out.Corpus.Sources[i].RelativePath == out.Corpus.Sources[j].RelativePath {
			return out.Corpus.Sources[i].Fingerprint < out.Corpus.Sources[j].Fingerprint
		}
		return out.Corpus.Sources[i].RelativePath < out.Corpus.Sources[j].RelativePath
	})
	return &out
}

func replaceSourceByPath(sources []domain.StyleSkillsSource, next domain.StyleSkillsSource) []domain.StyleSkillsSource {
	out := sources[:0]
	for _, source := range sources {
		if source.RelativePath == next.RelativePath {
			continue
		}
		out = append(out, source)
	}
	return append(out, next)
}

func buildSourceUserPrompt(source corpus.Source) string {
	payload := map[string]any{
		"relative_path": source.RelativePath,
		"sha256":        source.SHA256,
		"size_bytes":    source.SizeBytes,
		"content":       sampleSourceContent(source.Content),
	}
	data, _ := json.MarshalIndent(payload, "", "  ")
	return string(data)
}

func buildMergeUserPrompt(existing *domain.StyleSkills, reports []Report) string {
	payload := map[string]any{
		"existing_skills": domain.CompactStyleSkills(existing),
		"source_reports":  reports,
	}
	data, _ := json.MarshalIndent(payload, "", "  ")
	return string(data)
}
