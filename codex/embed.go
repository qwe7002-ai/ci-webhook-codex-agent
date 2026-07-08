// Package codexskill embeds the Codex operating guide (AGENTS.md) into the
// binary so it can be passed to Codex as the MCP call's system / base-instructions
// argument, rather than being read from the working directory.
package codexskill

import _ "embed"

// AgentsMD is the operating guide sent to Codex as the system instructions.
//
//go:embed AGENTS.md
var AgentsMD string
