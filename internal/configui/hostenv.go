package configui

import (
	"strings"

	"github.com/pjlsergeant/byre/internal/config"
	"github.com/pjlsergeant/byre/internal/credentials"
)

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
// `credential` comes LAST, and deliberately so: the editor that cannot write
// one (--global, whose file no credentials verb targets either) offers the
// first five and nothing else, and an option list that only ever SHRINKS from
// the tail keeps every other scheme's index meaning one thing.
const (
	schemeValue = iota
	schemeGit
	schemeEnv
	schemeTZ
	schemeDisabled
	schemeCredential
)

// The picker's words, in picker order. `credential` is spelled in user terms
// rather than as the scheme it writes: "encrypted:" says how the row is
// stored, and the question this screen asks is what the box gets.
var hostEnvSchemes = []string{"value", "git:", "env:", "tz:", "disabled", "credential"}

// The credential KIND is a second picker, not two more options on this one:
// the segmented picker paints every option on one line, and six schemes plus
// two spelled-out credential kinds ran past 80 columns -- where the clip would
// eat the very option a reader had selected. It is also the volumes editor's
// shape (an entry carrying two independent closed vocabularies gets two
// controls), and the question really is a second one: what the box gets.
var credKindOpts = []string{"env var", "file"}

const (
	credKindEnv = iota
	credKindFile
)

// hostEnvPickerOpts is the option set for one editor: every scheme, or the
// non-credential five where nothing can write a credential to this file.
func hostEnvPickerOpts(canWriteCredentials bool) []string {
	if canWriteCredentials {
		return hostEnvSchemes
	}
	return hostEnvSchemes[:schemeCredential]
}

// isPassthrough reports whether a scheme writes env_from_host rather than an
// [env] literal.
func isPassthrough(scheme int) bool { return scheme != schemeValue }

// isCredentialScheme reports whether a scheme's value is an encrypted row.
// Such a row is never encoded from the form's input (see hostEnvSource): its
// source is the ciphertext the write path returns.
func isCredentialScheme(scheme int) bool { return scheme == schemeCredential }

// credentialKind maps the kind picker onto the kind the value is encrypted
// and delivered as, and credKindSel maps a stored row back onto the picker.
func credentialKind(sel int) credentials.Kind {
	if sel == credKindFile {
		return credentials.KindFile
	}
	return credentials.KindEnv
}

func credKindSel(src string) int {
	if strings.HasPrefix(src, config.EncryptedFileScheme) {
		return credKindFile
	}
	return credKindEnv
}

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
	case schemeCredential:
		// "Value", like an [env] literal's: the difference is that this one is
		// masked and encrypted on the way to the file, which the notes and the
		// bullets say without spending label width on it.
		return "Value"
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
	case schemeCredential:
		return "typed hidden, encrypted into this file"
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
	// A credential decodes to the scheme and an EMPTY argument (its kind rides
	// the second picker, via credKindSel): the stored value is a ciphertext
	// this editor never shows and never re-encodes, so the form opens with the
	// Value field empty and "empty means unchanged".
	case config.IsCredentialSource(src):
		return schemeCredential, ""
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
//
// A credential scheme never reaches here: its row is the ciphertext the write
// path returns, and encoding one from the form's input would put the typed
// value into the file in the clear. commitEnvRow branches before this call.
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
//
// A credential row is not a host passthrough and must not read as one: its
// value comes out of the config file itself, and "host" would name a source
// it does not have. Its ciphertext elides through config.RenderSource, the
// same renderer the preset-review gate uses -- a wall of base64 in a list row
// pushes every other row off the screen.
func hostEnvLine(key, src string) string {
	if src == "" {
		return key + " <- disabled"
	}
	if config.IsCredentialSource(src) {
		// A credential row carries a value by construction — a row IS the
		// value, so there is no set/unset state to render. What happens to
		// that value in the cascade -- an [env] literal beating it, a nearer
		// "" switching it off -- is the row ANNOTATION's job on this screen,
		// exactly as it is for every other passthrough.
		return key + " <- credential " + config.RenderSource(src)
	}
	return key + " <- host " + src
}
