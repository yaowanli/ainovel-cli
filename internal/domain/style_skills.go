package domain

import (
	"encoding/json"
	"fmt"
	"strings"
)

const (
	StyleSkillsVersion = "style_skills.v1"

	// maxStyleSkillItems 与 maxStyleSkillItemRunes 让注入写作上下文的 skill 保持
	// "短且可执行"。刻意比仿写画像更狠（画像给 12 条）：skill 的读者是 writer，
	// 每章都要读一次，条目一多就退化成又一份读不动的画像。
	// 上限对齐 WritingStyleRules 的既有约定（"3-5 条，每条 ≤50 字"）。
	maxStyleSkillItems     = 5
	maxStyleSkillItemRunes = 50
)

// StyleSkills 是从参考语料蒸馏出的写作/对话风格 skill，独立于仿写画像。
// 画像回答"结构与手法怎么搭"，skill 回答"这句话该怎么说出口"。
type StyleSkills struct {
	Version   string            `json:"version"`
	CreatedAt string            `json:"created_at,omitempty"`
	UpdatedAt string            `json:"updated_at,omitempty"`
	Corpus    StyleSkillsCorpus `json:"corpus"`
	Prose     ProseSkill        `json:"prose_skill"`
	Dialogue  DialogueSkill     `json:"dialogue_skill"`
	Taboos    []string          `json:"taboos,omitempty"`
}

type StyleSkillsCorpus struct {
	SourceDir string              `json:"source_dir,omitempty"`
	Sources   []StyleSkillsSource `json:"sources"`
}

type StyleSkillsSource struct {
	RelativePath string `json:"relative_path"`
	SHA256       string `json:"sha256"`
	Fingerprint  string `json:"fingerprint"`
	SizeBytes    int64  `json:"size_bytes,omitempty"`
	ModTime      string `json:"mod_time,omitempty"`
	AnalyzedAt   string `json:"analyzed_at,omitempty"`
}

// ProseSkill 是叙述层的可执行规则。
type ProseSkill struct {
	NarrativeVoice     []string `json:"narrative_voice,omitempty"`
	SentenceRhythm     []string `json:"sentence_rhythm,omitempty"`
	DescriptionTexture []string `json:"description_texture,omitempty"`
	Pacing             []string `json:"pacing,omitempty"`
}

// DialogueSkill 是对话层的可执行规则，六个子项对应需求里点名的维度。
type DialogueSkill struct {
	SpeechTags     []string `json:"speech_tags,omitempty"`      // 对白标签习惯
	FormsOfAddress []string `json:"forms_of_address,omitempty"` // 称谓
	ModalParticles []string `json:"modal_particles,omitempty"`  // 语气词
	VerbalTics     []string `json:"verbal_tics,omitempty"`      // 口头禅（只登记模式）
	LineLength     []string `json:"line_length,omitempty"`      // 台词长度感
	Dialect        []string `json:"dialect,omitempty"`          // 方言
}

func ValidateStyleSkills(s *StyleSkills) error {
	if s == nil {
		return fmt.Errorf("style skills is nil")
	}
	if s.Version != StyleSkillsVersion {
		return fmt.Errorf("unsupported style skills version %q", s.Version)
	}
	for i := range s.Corpus.Sources {
		source := &s.Corpus.Sources[i]
		if source.RelativePath == "" || source.SHA256 == "" {
			return fmt.Errorf("source[%d] requires relative_path and sha256", i)
		}
		if source.Fingerprint == "" {
			source.Fingerprint = SourceFingerprint(source.RelativePath, source.SHA256)
		}
	}
	return nil
}

func MarshalStyleSkills(s StyleSkills) ([]byte, error) {
	if s.Version == "" {
		s.Version = StyleSkillsVersion
	}
	return json.MarshalIndent(s, "", "  ")
}

// CompactStyleSkills 返回注入写作上下文的精简副本。输入不被修改。
func CompactStyleSkills(s *StyleSkills) *StyleSkills {
	if s == nil {
		return nil
	}
	limit := len(s.Corpus.Sources)
	if limit > maxStyleSkillItems {
		limit = maxStyleSkillItems
	}
	out := &StyleSkills{
		Version:   s.Version,
		CreatedAt: s.CreatedAt,
		UpdatedAt: s.UpdatedAt,
		Corpus: StyleSkillsCorpus{
			SourceDir: s.Corpus.SourceDir,
			Sources:   make([]StyleSkillsSource, limit),
		},
		Prose: ProseSkill{
			NarrativeVoice:     compactStyleSkillItems(s.Prose.NarrativeVoice),
			SentenceRhythm:     compactStyleSkillItems(s.Prose.SentenceRhythm),
			DescriptionTexture: compactStyleSkillItems(s.Prose.DescriptionTexture),
			Pacing:             compactStyleSkillItems(s.Prose.Pacing),
		},
		Dialogue: DialogueSkill{
			SpeechTags:     compactStyleSkillItems(s.Dialogue.SpeechTags),
			FormsOfAddress: compactStyleSkillItems(s.Dialogue.FormsOfAddress),
			ModalParticles: compactStyleSkillItems(s.Dialogue.ModalParticles),
			VerbalTics:     compactStyleSkillItems(s.Dialogue.VerbalTics),
			LineLength:     compactStyleSkillItems(s.Dialogue.LineLength),
			Dialect:        compactStyleSkillItems(s.Dialogue.Dialect),
		},
		Taboos: compactStyleSkillItems(s.Taboos),
	}
	// 语料清单只留路径：大小与 mtime 对写作没有意义，却每章都要跟着进上下文。
	for i := 0; i < limit; i++ {
		source := s.Corpus.Sources[i]
		source.SizeBytes = 0
		source.ModTime = ""
		out.Corpus.Sources[i] = source
	}
	return out
}

func compactStyleSkillItems(items []string) []string {
	if len(items) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(items))
	out := make([]string, 0, maxStyleSkillItems)
	for _, item := range items {
		item = strings.TrimSpace(item)
		// 超长条目直接丢弃而不是硬截断：截断会把半句话塞进 skill，模型会当成
		// 完整规则去执行，反而更糟。契约已要求每条 ≤50 字，超长即违约，丢掉它。
		if item == "" || len([]rune(item)) > maxStyleSkillItemRunes {
			continue
		}
		key := strings.ToLower(item)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, item)
		if len(out) == maxStyleSkillItems {
			break
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
