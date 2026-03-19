package testfixture

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/marginlab/margin-eval/runner/runner-core/agentdef"
	"github.com/marginlab/margin-eval/runner/runner-core/runbundle"
)

const IntegrationInstructionSkillName = "reply-with-keyword"

func IntegrationInstructionPrompt() string {
	return `Use the "reply-with-keyword" skill and the project root instructions file to determine both configured keywords. Respond with the root instructions keyword, then a single space, then the skill keyword, then exit.`
}

type IntegrationInstructionKeywords struct {
	Root  string
	Skill string
}

func (k IntegrationInstructionKeywords) ExpectedResponse() string {
	return k.Root + " " + k.Skill
}

func IntegrationInstructionKeywordsForAgent(agentName string) IntegrationInstructionKeywords {
	switch agentName {
	case "codex":
		return IntegrationInstructionKeywords{Root: "CodexArbor", Skill: "LanternSignal"}
	case "claude-code":
		return IntegrationInstructionKeywords{Root: "ClaudeBeacon", Skill: "HarborCipher"}
	case "opencode":
		return IntegrationInstructionKeywords{Root: "OpencodeCinder", Skill: "ZephyrMarker"}
	default:
		return IntegrationInstructionKeywords{Root: "FixtureSignal", Skill: "FixtureCipher"}
	}
}

func RepoOwnedAgentWithInstructionFixtures(name, configName, version string) (runbundle.Agent, string, error) {
	agent := RepoOwnedAgentWithConfigVersion(name, configName, version)
	keywords := IntegrationInstructionKeywordsForAgent(name)
	withInstructions, err := WithInstructionFixtures(agent, keywords)
	if err != nil {
		return runbundle.Agent{}, "", err
	}
	return withInstructions, keywords.ExpectedResponse(), nil
}

func WithInstructionFixtures(agent runbundle.Agent, keywords IntegrationInstructionKeywords) (runbundle.Agent, error) {
	clone := cloneAgent(agent)
	skill, err := integrationInstructionSkill(keywords.Skill)
	if err != nil {
		return runbundle.Agent{}, err
	}
	clone.Config.Skills = append(clone.Config.Skills, skill)
	clone.Config.AgentsMD = &agentdef.AgentsMDSpec{
		Content: fmt.Sprintf("The root instructions keyword for this run is %q.\nWhen asked to report both configured keywords, output the root instructions keyword first.\n", keywords.Root),
	}
	return clone, nil
}

func integrationInstructionSkill(keyword string) (agentdef.SkillSpec, error) {
	root, err := os.MkdirTemp("", "marginlab-it-skill-*")
	if err != nil {
		return agentdef.SkillSpec{}, fmt.Errorf("create temp skill dir: %w", err)
	}
	defer os.RemoveAll(root)

	body := fmt.Sprintf(`---
name: %s
description: Use when asked to determine the configured skill keyword and combine it with the root instructions keyword in the final response.
---

When asked to report both configured keywords:
1. Read the project root instructions file to find the root instructions keyword.
2. The skill keyword for this run is %q.
3. Respond with the root instructions keyword, then a single space, then %q.
4. Exit immediately after responding.
`, IntegrationInstructionSkillName, keyword, keyword)
	if err := os.WriteFile(filepath.Join(root, "SKILL.md"), []byte(body), 0o644); err != nil {
		return agentdef.SkillSpec{}, fmt.Errorf("write SKILL.md: %w", err)
	}
	return agentdef.LoadSkillSpecFromDir(root)
}
