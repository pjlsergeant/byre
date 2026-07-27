package configui

import "strings"

// hostenv.go is the env_from_host widget's vocabulary: the closed scheme set
// (config.validateHostSource owns the grammar) rendered as a picker.
//
// The key is a GRANT, not a value: `git:user.name` says "pass the host's git
// identity in", never "set it to this string" -- a literal belongs in [env].
// So the editor asks for a scheme first and an argument second, rather than a
// free-text source a user could fill with a literal that would only be
// refused at save.

// The picker options, in order. `value` is FIRST and is not an
// env_from_host scheme at all -- it is an [env] literal. The two live on one
// screen because they answer ONE question, "where does this variable's value
// come from", and splitting that question across two editors is what left no
// way to ADD a passthrough at all: the screen's add key only ever built a
// literal.
//
// There is deliberately no "inherit" option. Un-pinning is Delete on the row,
// which is what Delete means on every other list field (drop this layer's
// entry, re-inherit the one below) -- a second spelling of it in the picker
// would be a concept the screen does not need.
const (
	schemeValue = iota
	schemeGit
	schemeEnv
	schemeTZ
	schemeDisabled
)

var hostEnvSchemes = []string{"value", "git:", "env:", "tz:", "disabled"}

// isPassthrough reports whether a scheme writes env_from_host rather than an
// [env] literal.
func isPassthrough(scheme int) bool { return scheme != schemeValue }

// hostEnvArgLabel is the second input's label for a scheme. Deliberately
// SHORT and near-uniform in width: the label column is sized from the longest
// label, so a scheme-dependent sentence here made the whole form jump
// sideways on every ←/→ (seen on a real pty -- the widths ranged 30 to 49).
// The guidance moved to the placeholder, which lives inside the input and
// costs no column width.
func hostEnvArgLabel(scheme int) string {
	switch scheme {
	case schemeValue:
		return "Value"
	case schemeGit:
		return "git config key"
	case schemeEnv:
		return "host variable"
	}
	return "(no argument)"
}

// hostEnvArgHint is the placeholder for the argument input: an example where
// there is one to give, and what the scheme does where there is not.
func hostEnvArgHint(scheme int) string {
	switch scheme {
	case schemeValue:
		return "" // a literal explains itself
	case schemeGit:
		return "user.name"
	case schemeEnv:
		return "TERM"
	case schemeTZ:
		return "the host's timezone"
	case schemeDisabled:
		return "the key is passed through to nothing"
	}
	return "the value comes from the cascade"
}

// hostEnvScheme decodes a stored source into (scheme, argument). "" is the
// DISABLE sentinel, not an absent entry: a config writes KEY = "" to switch a
// lower layer's passthrough off, which is why it is a scheme here and
// "inherit" (no entry at all) is not.
func hostEnvScheme(src string) (int, string) {
	switch {
	case src == "":
		return schemeDisabled, ""
	//nolint:gocritic // the ordered switch reads as the grammar it decodes
	case src == "tz:":
		return schemeTZ, ""
	case strings.HasPrefix(src, "git:"):
		return schemeGit, strings.TrimPrefix(src, "git:")
	case strings.HasPrefix(src, "env:"):
		return schemeEnv, strings.TrimPrefix(src, "env:")
	}
	// An unknown scheme cannot round-trip through the picker; treat it as
	// disabled so the editor shows something honest rather than inventing a
	// scheme. Save would refuse it anyway (validateHostSource).
	return schemeDisabled, ""
}

// hostEnvSource encodes a picker selection back into a stored source.
func hostEnvSource(scheme int, arg string) string {
	arg = strings.TrimSpace(arg)
	switch scheme {
	case schemeGit:
		return "git:" + arg
	case schemeEnv:
		return "env:" + arg
	case schemeTZ:
		return "tz:"
	}
	return "" // disabled
}

// hostEnvLine renders one passthrough row. The arrow points the way the value
// travels -- out of the host, into the box -- matching how the row read when
// it was read-only.
func hostEnvLine(key, src string) string {
	if src == "" {
		return key + " <- disabled"
	}
	return key + " <- host " + src
}
