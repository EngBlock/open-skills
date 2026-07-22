package state

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
)

type agentConfig struct {
	ID          string
	DisplayName string
	ProjectDir  string
	GlobalDir   string
	DetectPath  string
	NoGlobal    bool
	UsesXDG     bool
	GlobalEnv   string
	DetectEnv   string
}

// agentConfigs is the immutable npm 0.1.2 agent vocabulary. Paths are kept
// here so inspection and later installation work share one source of truth.
var agentConfigs = []agentConfig{
	{ID: "aider-desk", DisplayName: "AiderDesk", ProjectDir: ".aider-desk/skills", GlobalDir: ".aider-desk/skills", DetectPath: ".aider-desk"},
	{ID: "amp", DisplayName: "Amp", ProjectDir: ".agents/skills", GlobalDir: "agents/skills", DetectPath: "amp", UsesXDG: true},
	{ID: "antigravity", DisplayName: "Antigravity", ProjectDir: ".agents/skills", GlobalDir: ".gemini/antigravity/skills", DetectPath: ".gemini/antigravity"},
	{ID: "antigravity-cli", DisplayName: "Antigravity CLI", ProjectDir: ".agents/skills", GlobalDir: ".gemini/antigravity-cli/skills", DetectPath: ".gemini/antigravity-cli"},
	{ID: "astrbot", DisplayName: "AstrBot", ProjectDir: "data/skills", GlobalDir: ".astrbot/data/skills", DetectPath: ".astrbot"},
	{ID: "autohand-code", DisplayName: "Autohand Code CLI", ProjectDir: ".autohand/skills", GlobalDir: "skills", GlobalEnv: "AUTOHAND_HOME", DetectEnv: "AUTOHAND_HOME", DetectPath: ".autohand"},
	{ID: "augment", DisplayName: "Augment", ProjectDir: ".augment/skills", GlobalDir: ".augment/skills", DetectPath: ".augment"},
	{ID: "bob", DisplayName: "IBM Bob", ProjectDir: ".bob/skills", GlobalDir: ".bob/skills", DetectPath: ".bob"},
	{ID: "claude-code", DisplayName: "Claude Code", ProjectDir: ".claude/skills", GlobalDir: "skills", GlobalEnv: "CLAUDE_CONFIG_DIR", DetectEnv: "CLAUDE_CONFIG_DIR", DetectPath: ".claude"},
	{ID: "openclaw", DisplayName: "OpenClaw", ProjectDir: "skills", GlobalDir: ".openclaw/skills", DetectPath: ".openclaw"},
	{ID: "cline", DisplayName: "Cline", ProjectDir: ".agents/skills", GlobalDir: ".agents/skills", DetectPath: ".cline"},
	{ID: "codearts-agent", DisplayName: "CodeArts Agent", ProjectDir: ".codeartsdoer/skills", GlobalDir: ".codeartsdoer/skills", DetectPath: ".codeartsdoer"},
	{ID: "codebuddy", DisplayName: "CodeBuddy", ProjectDir: ".codebuddy/skills", GlobalDir: ".codebuddy/skills", DetectPath: ".codebuddy"},
	{ID: "codemaker", DisplayName: "Codemaker", ProjectDir: ".codemaker/skills", GlobalDir: ".codemaker/skills", DetectPath: ".codemaker"},
	{ID: "codestudio", DisplayName: "Code Studio", ProjectDir: ".codestudio/skills", GlobalDir: ".codestudio/skills", DetectPath: ".codestudio"},
	{ID: "codex", DisplayName: "Codex", ProjectDir: ".agents/skills", GlobalDir: "skills", GlobalEnv: "CODEX_HOME", DetectEnv: "CODEX_HOME", DetectPath: ".codex"},
	{ID: "command-code", DisplayName: "Command Code", ProjectDir: ".commandcode/skills", GlobalDir: ".commandcode/skills", DetectPath: ".commandcode"},
	{ID: "continue", DisplayName: "Continue", ProjectDir: ".continue/skills", GlobalDir: ".continue/skills", DetectPath: ".continue"},
	{ID: "cortex", DisplayName: "Cortex Code", ProjectDir: ".cortex/skills", GlobalDir: ".snowflake/cortex/skills", DetectPath: ".snowflake/cortex"},
	{ID: "crush", DisplayName: "Crush", ProjectDir: ".crush/skills", GlobalDir: ".config/crush/skills", DetectPath: ".config/crush"},
	{ID: "cursor", DisplayName: "Cursor", ProjectDir: ".agents/skills", GlobalDir: ".cursor/skills", DetectPath: ".cursor"},
	{ID: "deepagents", DisplayName: "Deep Agents", ProjectDir: ".agents/skills", GlobalDir: ".deepagents/agent/skills", DetectPath: ".deepagents"},
	{ID: "devin", DisplayName: "Devin for Terminal", ProjectDir: ".devin/skills", GlobalDir: "devin/skills", DetectPath: "devin", UsesXDG: true},
	{ID: "dexto", DisplayName: "Dexto", ProjectDir: ".agents/skills", GlobalDir: ".agents/skills", DetectPath: ".dexto"},
	{ID: "droid", DisplayName: "Droid", ProjectDir: ".factory/skills", GlobalDir: ".factory/skills", DetectPath: ".factory"},
	{ID: "eve", DisplayName: "Eve", ProjectDir: "agent/skills", NoGlobal: true},
	{ID: "firebender", DisplayName: "Firebender", ProjectDir: ".agents/skills", GlobalDir: ".firebender/skills", DetectPath: ".firebender"},
	{ID: "forgecode", DisplayName: "ForgeCode", ProjectDir: ".forge/skills", GlobalDir: ".forge/skills", DetectPath: ".forge"},
	{ID: "gemini-cli", DisplayName: "Gemini CLI", ProjectDir: ".agents/skills", GlobalDir: ".gemini/skills", DetectPath: ".gemini"},
	{ID: "github-copilot", DisplayName: "GitHub Copilot", ProjectDir: ".agents/skills", GlobalDir: ".copilot/skills", DetectPath: ".copilot"},
	{ID: "goose", DisplayName: "Goose", ProjectDir: ".goose/skills", GlobalDir: "goose/skills", DetectPath: "goose", UsesXDG: true},
	{ID: "grok", DisplayName: "Grok Build", ProjectDir: ".grok/skills", GlobalDir: "skills", GlobalEnv: "GROK_HOME", DetectEnv: "GROK_HOME", DetectPath: ".grok"},
	{ID: "hermes-agent", DisplayName: "Hermes Agent", ProjectDir: ".hermes/skills", GlobalDir: "skills", GlobalEnv: "HERMES_HOME", DetectEnv: "HERMES_HOME", DetectPath: ".hermes"},
	{ID: "inference-sh", DisplayName: "inference.sh", ProjectDir: ".inferencesh/skills", GlobalDir: ".inferencesh/skills", DetectPath: ".inferencesh"},
	{ID: "jazz", DisplayName: "Jazz", ProjectDir: ".jazz/skills", GlobalDir: ".jazz/skills", DetectPath: ".jazz"},
	{ID: "junie", DisplayName: "Junie", ProjectDir: ".junie/skills", GlobalDir: ".junie/skills", DetectPath: ".junie"},
	{ID: "iflow-cli", DisplayName: "iFlow CLI", ProjectDir: ".iflow/skills", GlobalDir: ".iflow/skills", DetectPath: ".iflow"},
	{ID: "kilo", DisplayName: "Kilo Code", ProjectDir: ".kilocode/skills", GlobalDir: ".kilocode/skills", DetectPath: ".kilocode"},
	{ID: "kimchi", DisplayName: "Kimchi", ProjectDir: ".kimchi/skills", GlobalDir: ".config/kimchi/harness/skills", DetectPath: ".config/kimchi"},
	{ID: "kimi-code-cli", DisplayName: "Kimi Code CLI", ProjectDir: ".agents/skills", GlobalDir: ".agents/skills", DetectPath: ".kimi-code"},
	{ID: "kiro-cli", DisplayName: "Kiro CLI", ProjectDir: ".kiro/skills", GlobalDir: ".kiro/skills", DetectPath: ".kiro"},
	{ID: "kode", DisplayName: "Kode", ProjectDir: ".kode/skills", GlobalDir: ".kode/skills", DetectPath: ".kode"},
	{ID: "lingma", DisplayName: "Lingma", ProjectDir: ".lingma/skills", GlobalDir: ".lingma/skills", DetectPath: ".lingma"},
	{ID: "loaf", DisplayName: "Loaf", ProjectDir: ".agents/skills", GlobalDir: ".agents/skills", DetectPath: ".loaf"},
	{ID: "mcpjam", DisplayName: "MCPJam", ProjectDir: ".mcpjam/skills", GlobalDir: ".mcpjam/skills", DetectPath: ".mcpjam"},
	{ID: "mistral-vibe", DisplayName: "Mistral Vibe", ProjectDir: ".vibe/skills", GlobalDir: "skills", GlobalEnv: "VIBE_HOME", DetectEnv: "VIBE_HOME", DetectPath: ".vibe"},
	{ID: "moxby", DisplayName: "Moxby", ProjectDir: ".moxby/skills", GlobalDir: ".moxby/skills", DetectPath: ".moxby"},
	{ID: "mux", DisplayName: "Mux", ProjectDir: ".mux/skills", GlobalDir: ".mux/skills", DetectPath: ".mux"},
	{ID: "opencode", DisplayName: "OpenCode", ProjectDir: ".agents/skills", GlobalDir: "opencode/skills", DetectPath: "opencode", UsesXDG: true},
	{ID: "openhands", DisplayName: "OpenHands", ProjectDir: ".openhands/skills", GlobalDir: ".openhands/skills", DetectPath: ".openhands"},
	{ID: "ona", DisplayName: "Ona", ProjectDir: ".ona/skills", GlobalDir: ".ona/skills", DetectPath: ".ona"},
	{ID: "pi", DisplayName: "Pi", ProjectDir: ".pi/skills", GlobalDir: ".pi/agent/skills", DetectPath: ".pi/agent"},
	{ID: "qoder", DisplayName: "Qoder", ProjectDir: ".qoder/skills", GlobalDir: ".qoder/skills", DetectPath: ".qoder"},
	{ID: "qoder-cn", DisplayName: "Qoder CN", ProjectDir: ".qoder/skills", GlobalDir: ".qoder-cn/skills", DetectPath: ".qoder-cn"},
	{ID: "qwen-code", DisplayName: "Qwen Code", ProjectDir: ".qwen/skills", GlobalDir: ".qwen/skills", DetectPath: ".qwen"},
	{ID: "replit", DisplayName: "Replit", ProjectDir: ".agents/skills", GlobalDir: "agents/skills", DetectPath: ".replit", UsesXDG: true},
	{ID: "reasonix", DisplayName: "Reasonix", ProjectDir: ".reasonix/skills", GlobalDir: ".reasonix/skills", DetectPath: ".reasonix"},
	{ID: "rovodev", DisplayName: "Rovo Dev", ProjectDir: ".rovodev/skills", GlobalDir: ".rovodev/skills", DetectPath: ".rovodev"},
	{ID: "roo", DisplayName: "Roo Code", ProjectDir: ".roo/skills", GlobalDir: ".roo/skills", DetectPath: ".roo"},
	{ID: "tabnine-cli", DisplayName: "Tabnine CLI", ProjectDir: ".tabnine/agent/skills", GlobalDir: ".tabnine/agent/skills", DetectPath: ".tabnine"},
	{ID: "terramind", DisplayName: "Terramind", ProjectDir: ".terramind/skills", GlobalDir: ".terramind/skills", DetectPath: ".terramind"},
	{ID: "tinycloud", DisplayName: "Tinycloud", ProjectDir: ".tinycloud/skills", GlobalDir: ".tinycloud/skills", DetectPath: ".tinycloud"},
	{ID: "trae", DisplayName: "Trae", ProjectDir: ".trae/skills", GlobalDir: ".trae/skills", DetectPath: ".trae"},
	{ID: "trae-cn", DisplayName: "Trae CN", ProjectDir: ".trae/skills", GlobalDir: ".trae-cn/skills", DetectPath: ".trae-cn"},
	{ID: "warp", DisplayName: "Warp", ProjectDir: ".agents/skills", GlobalDir: ".agents/skills", DetectPath: ".warp"},
	{ID: "windsurf", DisplayName: "Windsurf", ProjectDir: ".windsurf/skills", GlobalDir: ".codeium/windsurf/skills", DetectPath: ".codeium/windsurf"},
	{ID: "zed", DisplayName: "Zed", ProjectDir: ".agents/skills", GlobalDir: ".agents/skills", DetectPath: ".config/zed"},
	{ID: "zcode", DisplayName: "ZCode", ProjectDir: ".zcode/skills", GlobalDir: ".zcode/skills", DetectPath: ".zcode"},
	{ID: "zencoder", DisplayName: "Zencoder", ProjectDir: ".zencoder/skills", GlobalDir: ".zencoder/skills", DetectPath: ".zencoder"},
	{ID: "zenflow", DisplayName: "Zenflow", ProjectDir: ".zencoder/skills", GlobalDir: ".zencoder/skills", DetectPath: ".zencoder"},
	{ID: "neovate", DisplayName: "Neovate", ProjectDir: ".neovate/skills", GlobalDir: ".neovate/skills", DetectPath: ".neovate"},
	{ID: "pochi", DisplayName: "Pochi", ProjectDir: ".pochi/skills", GlobalDir: ".pochi/skills", DetectPath: ".pochi"},
	{ID: "promptscript", DisplayName: "PromptScript", ProjectDir: ".agents/skills", NoGlobal: true, DetectPath: ".promptscript"},
	{ID: "adal", DisplayName: "AdaL", ProjectDir: ".adal/skills", GlobalDir: ".adal/skills", DetectPath: ".adal"},
	{ID: "universal", DisplayName: "Universal", ProjectDir: ".agents/skills", GlobalDir: "agents/skills", UsesXDG: true},
}

func AgentIDs() []string {
	ids := make([]string, 0, len(agentConfigs))
	for _, agent := range agentConfigs {
		ids = append(ids, agent.ID)
	}
	return ids
}

func AgentDisplayName(id string) string {
	for _, agent := range agentConfigs {
		if agent.ID == id {
			return agent.DisplayName
		}
	}
	return id
}

// AgentSkillsPath resolves the established skills directory for an agent. The
// boolean result reports whether the path is the canonical .agents/skills
// topology shared by universal agents.
func AgentSkillsPath(id string, scope Scope, project, home, xdgConfigHome string) (string, bool, bool) {
	for _, agent := range agentConfigs {
		if agent.ID != id || (scope == Global && agent.NoGlobal) {
			continue
		}
		options := InspectOptions{
			Scope: scope, Project: project, Home: home, XDGConfigHome: xdgConfigHome,
		}
		path := agentSkillsDir(agent, options)
		canonical := agent.ProjectDir == ".agents/skills"
		return path, canonical, true
	}
	return "", false, false
}

func selectedAgentConfigs(filter []string) []agentConfig {
	if len(filter) == 0 {
		return append([]agentConfig(nil), agentConfigs...)
	}
	selected := make(map[string]bool, len(filter))
	for _, id := range filter {
		selected[id] = true
	}
	result := []agentConfig{}
	for _, agent := range agentConfigs {
		if selected[agent.ID] {
			result = append(result, agent)
		}
	}
	return result
}

func agentSkillsDir(agent agentConfig, options InspectOptions) string {
	if options.Scope == Project {
		return filepath.Join(options.Project, filepath.FromSlash(agent.ProjectDir))
	}
	if agent.NoGlobal {
		return ""
	}
	if agent.GlobalEnv != "" {
		if base := os.Getenv(agent.GlobalEnv); base != "" {
			return filepath.Join(base, filepath.FromSlash(agent.GlobalDir))
		}
		return filepath.Join(options.Home, filepath.FromSlash(agent.DetectPath), filepath.FromSlash(agent.GlobalDir))
	}
	if agent.ID == "openclaw" {
		for _, directory := range []string{".openclaw", ".clawdbot", ".moltbot"} {
			if info, err := os.Stat(filepath.Join(options.Home, directory)); err == nil && info.IsDir() {
				return filepath.Join(options.Home, directory, "skills")
			}
		}
	}
	base := options.Home
	if agent.UsesXDG {
		base = options.XDGConfigHome
		if base == "" {
			base = filepath.Join(options.Home, ".config")
		}
	}
	return filepath.Join(base, filepath.FromSlash(agent.GlobalDir))
}

func agentDetected(agent agentConfig, options InspectOptions) bool {
	switch agent.ID {
	case "codex":
		if info, err := os.Stat("/etc/codex"); err == nil && info.IsDir() {
			return true
		}
	case "eve":
		if info, err := os.Stat(filepath.Join(options.Project, "agent")); err != nil || !info.IsDir() {
			return false
		}
		data, err := os.ReadFile(filepath.Join(options.Project, "package.json"))
		if err != nil {
			return false
		}
		var manifest struct {
			Dependencies    map[string]json.RawMessage `json:"dependencies"`
			DevDependencies map[string]json.RawMessage `json:"devDependencies"`
		}
		if json.Unmarshal(data, &manifest) != nil {
			return false
		}
		if _, exists := manifest.Dependencies["eve"]; exists {
			return true
		}
		_, exists := manifest.DevDependencies["eve"]
		return exists
	case "openclaw":
		for _, directory := range []string{".openclaw", ".clawdbot", ".moltbot"} {
			if info, err := os.Stat(filepath.Join(options.Home, directory)); err == nil && info.IsDir() {
				return true
			}
		}
	case "kimi-code-cli":
		for _, directory := range []string{".kimi-code", ".kimi"} {
			if info, err := os.Stat(filepath.Join(options.Home, directory)); err == nil && info.IsDir() {
				return true
			}
		}
	case "promptscript":
		for _, path := range []string{filepath.Join(options.Project, ".promptscript"), filepath.Join(options.Project, "promptscript.yaml")} {
			if _, err := os.Stat(path); err == nil {
				return true
			}
		}
	case "zed":
		configHome := options.XDGConfigHome
		if configHome == "" {
			configHome = filepath.Join(options.Home, ".config")
		}
		paths := []string{filepath.Join(configHome, "zed")}
		if appData := os.Getenv("APPDATA"); appData != "" {
			paths = append(paths, filepath.Join(appData, "Zed"))
		}
		if flatpak := os.Getenv("FLATPAK_XDG_CONFIG_HOME"); flatpak != "" {
			paths = append(paths, filepath.Join(flatpak, "zed"))
		}
		for _, path := range paths {
			if info, err := os.Stat(path); err == nil && info.IsDir() {
				return true
			}
		}
	}
	if agent.DetectEnv != "" {
		if path := os.Getenv(agent.DetectEnv); path != "" {
			if info, err := os.Stat(path); err == nil && info.IsDir() {
				return true
			}
		}
	}
	if agent.DetectPath == "" {
		return false
	}
	base := options.Home
	if agent.UsesXDG {
		base = options.XDGConfigHome
		if base == "" {
			base = filepath.Join(options.Home, ".config")
		}
	}
	if agent.ID == "replit" || agent.ID == "promptscript" {
		base = options.Project
	}
	info, err := os.Stat(filepath.Join(base, filepath.FromSlash(agent.DetectPath)))
	return err == nil && info.IsDir()
}

func sortAgentIDs(ids []string) {
	order := make(map[string]int, len(agentConfigs))
	for index, agent := range agentConfigs {
		order[agent.ID] = index
	}
	sort.Slice(ids, func(i, j int) bool { return order[ids[i]] < order[ids[j]] })
}
