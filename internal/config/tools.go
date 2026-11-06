package config

import (
	"strings"

	"github.com/hkluke/buildbox/internal/abort"
)

// IDs of the coding tools, as written in AITools and in the [tools] table.
const (
	ToolGrok     = "grok"
	ToolClaude   = "claude"
	ToolCodex    = "codex"
	ToolOpenCode = "opencode"
	ToolNone     = "none"
)

// Tool is one CLI installed for the agent user. Command is the upstream
// installer and never includes a value from the config file.
type Tool struct {
	ID      string
	Name    string
	Command string
}

// Tools lists every coding tool buildbox can install, in display order.
func Tools() []Tool {
	return []Tool{
		{ToolOpenCode, "OpenCode", "curl -fsSL https://opencode.ai/v2/install | bash"},
		{ToolClaude, "Claude Code", "curl -fsSL https://claude.ai/install.sh | bash"},
		{ToolGrok, "Grok Build", "curl -fsSL https://x.ai/cli/install.sh | bash"},
		{ToolCodex, "Codex", "curl -fsSL https://chatgpt.com/codex/install.sh | sh"},
	}
}

// KnownTool reports whether id names one of Tools.
func KnownTool(id string) bool {
	for _, tool := range Tools() {
		if tool.ID == id {
			return true
		}
	}

	return false
}

// ToolSet is the known tools in raw, ignoring anything else.
func ToolSet(raw string) map[string]bool {
	set := map[string]bool{}
	raw = strings.TrimSpace(raw)

	if raw == "" || raw == ToolNone {
		return set
	}

	for field := range strings.FieldsSeq(strings.ToLower(raw)) {
		if KnownTool(field) {
			set[field] = true
		}
	}

	return set
}

func joinCodingTools(set map[string]bool) string {
	ids := make([]string, 0, len(set))

	for _, tool := range Tools() {
		if set[tool.ID] {
			ids = append(ids, tool.ID)
		}
	}

	if len(ids) == 0 {
		return ToolNone
	}

	return strings.Join(ids, " ")
}

// StoredTools is raw in its canonical form: known tools in display order, or
// none.
func StoredTools(raw string) string {
	return joinCodingTools(ToolSet(raw))
}

// ToggleTool adds toolID to raw, or removes it when it is already there.
func ToggleTool(raw, toolID string) string {
	if !KnownTool(toolID) {
		return StoredTools(raw)
	}

	set := ToolSet(raw)
	if set[toolID] {
		delete(set, toolID)
	} else {
		set[toolID] = true
	}

	return joinCodingTools(set)
}

// codingToolsFromTable converts the TOML [tools] table. A missing table
// means OpenCode, which is the recommended selection.
// Every flag false installs nothing.
func codingToolsFromTable(tools *tomlTools) string {
	if tools == nil {
		return ""
	}

	set := map[string]bool{
		ToolGrok:     tools.Grok,
		ToolClaude:   tools.Claude,
		ToolCodex:    tools.Codex,
		ToolOpenCode: tools.OpenCode,
	}

	return joinCodingTools(set)
}

// ToolEnabled reports whether raw installs toolID. An empty value installs
// OpenCode.
func ToolEnabled(raw, toolID string) bool {
	if strings.TrimSpace(raw) == "" {
		raw = ToolOpenCode
	}

	return ToolSet(raw)[toolID]
}

// ParseTools reads the tool selection. An empty value installs OpenCode.
// none installs nothing.
func ParseTools(raw string) ([]string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return []string{ToolOpenCode}, nil
	}

	fields := strings.Fields(strings.ToLower(raw))
	if len(fields) == 1 && fields[0] == ToolNone {
		return nil, nil
	}

	seen := map[string]bool{}
	set := map[string]bool{}

	for _, field := range fields {
		if field == ToolNone {
			return nil, abort.New("none cannot be combined with a tool.")
		}

		if !KnownTool(field) {
			return nil, abort.New("Unknown coding tool: " + field + ".")
		}

		if seen[field] {
			continue
		}

		seen[field] = true
		set[field] = true
	}

	return strings.Fields(joinCodingTools(set)), nil
}

// ToolSummary names the chosen tools for the overview.
func ToolSummary(raw string) string {
	set := ToolSet(raw)
	names := make([]string, 0, len(set))

	for _, tool := range Tools() {
		if set[tool.ID] {
			names = append(names, tool.Name)
		}
	}

	if len(names) == 0 {
		return "None"
	}

	return strings.Join(names, ", ")
}
