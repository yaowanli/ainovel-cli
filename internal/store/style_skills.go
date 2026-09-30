package store

import (
	"os"

	"github.com/voocel/ainovel-cli/internal/domain"
)

type StyleSkillsStore struct{ io *IO }

func NewStyleSkillsStore(io *IO) *StyleSkillsStore { return &StyleSkillsStore{io: io} }

// Load 读取 meta/style_skills.json。不存在时返回 nil, nil——调用方据此跳过注入，
// 与 SimulationStore 同样的"工件可有可无"语义。
func (s *StyleSkillsStore) Load() (*domain.StyleSkills, error) {
	var skills domain.StyleSkills
	if err := s.io.ReadJSON("meta/style_skills.json", &skills); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	if err := domain.ValidateStyleSkills(&skills); err != nil {
		return nil, err
	}
	return &skills, nil
}

func (s *StyleSkillsStore) Save(skills domain.StyleSkills) error {
	if skills.Version == "" {
		skills.Version = domain.StyleSkillsVersion
	}
	if err := domain.ValidateStyleSkills(&skills); err != nil {
		return err
	}
	return s.io.WriteJSON("meta/style_skills.json", skills)
}
