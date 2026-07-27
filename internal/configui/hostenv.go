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

// The picker options, in order. Inherit is FIRST and is not a scheme: it is
// the absence of a local entry, and it exists so a user can UN-pin. Without
// it there is no way back to the cascade from the editor once a key has been
// set locally -- only a hand edit, which is the answer P0 says byre does not
// give.
const (
	schemeInherit = iota
	schemeGit
	schemeEnv
	schemeTZ
	schemeDisabled
)

var hostEnvSchemes = []string{"inherit", "git:", "env:", "tz:", "disabled"}

// hostEnvArgLabel is the second input's label for a scheme. tz: takes no
// argument and disabled takes none either; the label says so rather than
// leaving an input that does nothing.
func hostEnvArgLabel(scheme int) string {
	switch scheme {
	case schemeGit:
		return "git config key (e.g. user.name)"
	case schemeEnv:
		return "host variable name (e.g. TERM)"
	case schemeTZ:
		return "no argument — the host's timezone"
	case schemeDisabled:
		return "no argument — the key is passed through to nothing"
	}
	return "no argument — the value comes from the cascade"
}

// hostEnvScheme decodes a stored source into (scheme, argument). "" is the
// DISABLE sentinel, not an absent entry: a config writes KEY = "" to switch a
// lower layer's passthrough off, which is why it is a scheme here and
// "inherit" (no entry at all) is not.
func hostEnvScheme(src string) (int, string) {
	switch {
	case src == "":
		return schemeDisabled, ""
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
