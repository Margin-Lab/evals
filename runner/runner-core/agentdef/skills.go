package agentdef

import (
	"fmt"
	"os"
	"regexp"
	"slices"
	"strings"

	"github.com/marginlab/margin-eval/runner/runner-core/testassets"

	"gopkg.in/yaml.v3"
)

var skillNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

type skillFrontmatter struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
}

func LoadSkillSpecFromDir(root string) (SkillSpec, error) {
	root = strings.TrimSpace(root)
	if root == "" {
		return SkillSpec{}, fmt.Errorf("skill path is required")
	}
	info, err := os.Stat(root)
	if err != nil {
		return SkillSpec{}, fmt.Errorf("stat skill path: %w", err)
	}
	if !info.IsDir() {
		return SkillSpec{}, fmt.Errorf("skill path must be a directory")
	}
	pkg, err := testassets.PackDir(root)
	if err != nil {
		return SkillSpec{}, fmt.Errorf("package skill dir: %w", err)
	}
	skill, err := ValidateAndNormalizeSkillSpec(SkillSpec{Package: pkg})
	if err != nil {
		return SkillSpec{}, err
	}
	return skill, nil
}

func ValidateAndNormalizeSkillSpec(skill SkillSpec) (SkillSpec, error) {
	if err := testassets.ValidateDescriptor(skill.Package, testassets.DefaultMaxArchiveBytes); err != nil {
		return SkillSpec{}, fmt.Errorf("package: %w", err)
	}
	body, err := ReadPackageFile(skill.Package, "SKILL.md")
	if err != nil {
		return SkillSpec{}, fmt.Errorf("SKILL.md: %w", err)
	}
	frontmatter, err := parseSkillFrontmatter(body)
	if err != nil {
		return SkillSpec{}, fmt.Errorf("SKILL.md: %w", err)
	}
	if provided := strings.TrimSpace(skill.Name); provided != "" && provided != frontmatter.Name {
		return SkillSpec{}, fmt.Errorf("name %q must match SKILL.md frontmatter name %q", provided, frontmatter.Name)
	}
	if provided := strings.TrimSpace(skill.Description); provided != "" && provided != frontmatter.Description {
		return SkillSpec{}, fmt.Errorf("description must match SKILL.md frontmatter description")
	}
	return SkillSpec{
		Name:        frontmatter.Name,
		Description: frontmatter.Description,
		Package:     skill.Package,
	}, nil
}

func ValidateAndNormalizeSkillSpecs(skills []SkillSpec) ([]SkillSpec, error) {
	if len(skills) == 0 {
		return nil, nil
	}
	normalized := make([]SkillSpec, 0, len(skills))
	seen := make(map[string]struct{}, len(skills))
	for idx := range skills {
		skill, err := ValidateAndNormalizeSkillSpec(skills[idx])
		if err != nil {
			return nil, fmt.Errorf("skills[%d]: %w", idx, err)
		}
		if _, exists := seen[skill.Name]; exists {
			return nil, fmt.Errorf("skills[%d]: duplicate skill name %q", idx, skill.Name)
		}
		seen[skill.Name] = struct{}{}
		normalized = append(normalized, skill)
	}
	slices.SortFunc(normalized, func(a, b SkillSpec) int {
		return strings.Compare(a.Name, b.Name)
	})
	return normalized, nil
}

func parseSkillFrontmatter(body []byte) (skillFrontmatter, error) {
	text := strings.ReplaceAll(string(body), "\r\n", "\n")
	text = strings.TrimPrefix(text, "\uFEFF")
	lines := strings.Split(text, "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		return skillFrontmatter{}, fmt.Errorf("frontmatter must start with ---")
	}
	end := -1
	for idx := 1; idx < len(lines); idx++ {
		if strings.TrimSpace(lines[idx]) == "---" {
			end = idx
			break
		}
	}
	if end < 0 {
		return skillFrontmatter{}, fmt.Errorf("frontmatter must end with ---")
	}
	var meta skillFrontmatter
	if err := yaml.Unmarshal([]byte(strings.Join(lines[1:end], "\n")), &meta); err != nil {
		return skillFrontmatter{}, fmt.Errorf("invalid frontmatter: %w", err)
	}
	meta.Name = strings.TrimSpace(meta.Name)
	meta.Description = strings.TrimSpace(meta.Description)
	if meta.Name == "" {
		return skillFrontmatter{}, fmt.Errorf("frontmatter name is required")
	}
	if !skillNamePattern.MatchString(meta.Name) {
		return skillFrontmatter{}, fmt.Errorf("frontmatter name %q must match %s", meta.Name, skillNamePattern.String())
	}
	if meta.Description == "" {
		return skillFrontmatter{}, fmt.Errorf("frontmatter description is required")
	}
	return meta, nil
}
