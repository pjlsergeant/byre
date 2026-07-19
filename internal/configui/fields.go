package configui

import "strings"

// This table is the ONE place a field's cross-cutting metadata lives:
// display label, editing kind, the raw-block TOML key hint, the item
// editor's singular title, and the summary noun. Behavior stays where it
// was (rows in effective.go, item editors in listitem.go, placement in
// newModel's target-specific sections); what this table removes is a new
// field having to declare its IDENTITY across five independent switches —
// label, kind classification, editor title, and noun now arrive together
// or not at all.

// fieldKind classifies how a field is edited.
type fieldKind int

const (
	kindScalar fieldKind = iota // pickers, checkboxes, single-line inputs
	kindList                    // effective-rows list screen (ADR 0018)
	kindText                    // multi-line textarea overlay (raw blocks)
)

// fieldInfo is one field's metadata row.
type fieldInfo struct {
	label   string // display label (not the TOML key)
	kind    fieldKind
	tomlKey string // raw text blocks: the TOML key hint shown in the editor
	// item is the item editor's singular title. Explicit per list field:
	// naive de-pluralizing turned "Egress" into "Egres" (found live
	// 2026-07-08).
	item string
	noun string // summary noun, singular ("3 packages", "1 host")
}

var fieldInfos = map[fieldID]fieldInfo{
	fBase:            {label: "Base image"},
	fTemplate:        {label: "Template"},
	fAgent:           {label: "Pri. Agent"},
	fEngine:          {label: "Engine"},
	fApt:             {label: "Packages", kind: kindList, item: "Package", noun: "package"},
	fEnv:             {label: "Env vars", kind: kindList, item: "Env var", noun: "var"},
	fEgress:          {label: "Egress", kind: kindList, item: "Egress host", noun: "host"},
	fMounts:          {label: "Extra mounts", kind: kindList, item: "Extra mount", noun: "mount"},
	fPorts:           {label: "Ports", kind: kindList, item: "Port", noun: "port"},
	fMCP:             {label: "MCP servers", kind: kindList, item: "MCP server", noun: "server"},
	fClaudeSkills:    {label: "Claude Skills", kind: kindList, item: "Claude Skill", noun: "skill"},
	fVolumes:         {label: "Volumes"},
	fRunArgs:         {label: "Run args", kind: kindText, tomlKey: "run_args"},
	fDockerfilePre:   {label: "Dockerfile before", kind: kindText, tomlKey: "dockerfile_pre"},
	fDockerfilePost:  {label: "Dockerfile after", kind: kindText, tomlKey: "dockerfile_post"},
	fSkills:          {label: "Skills"},
	fWorktreeSibling: {label: "Worktree loc"},
	fWorktreeBase:    {label: "Base path"},
	fExtends:         {label: "Extends"},
}

// fieldLabel is the display name (not the raw TOML key); the underlying key
// is shown as a hint when editing the raw text blocks (rawFieldKey).
func fieldLabel(f fieldID) string { return fieldInfos[f].label }

// rawFieldKey is the TOML key behind a raw text field, "" otherwise.
func rawFieldKey(f fieldID) string { return fieldInfos[f].tomlKey }

func isTextField(f fieldID) bool { return fieldInfos[f].kind == kindText }

func isListField(f fieldID) bool { return fieldInfos[f].kind == kindList }

// itemTitle is the singular noun the item editor's title uses.
func itemTitle(f fieldID) string {
	if t := fieldInfos[f].item; t != "" {
		return t
	}
	return strings.TrimSuffix(fieldLabel(f), "s")
}

// fieldNoun is the summary noun for a list field, pluralized.
func fieldNoun(f fieldID, n int) string {
	noun := fieldInfos[f].noun
	if noun == "" {
		noun = "item"
	}
	if n != 1 {
		noun += "s"
	}
	return noun
}
