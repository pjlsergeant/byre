package commands

import (
	"fmt"
	"io"
	"reflect"
	"strings"

	"github.com/pjlsergeant/byre/internal/packages"
)

// The package's escaping vocabulary, in one place. A package id, a version, a
// config path, an error's text: byre prints all of it, and none of it is
// byre's to author. A terminal obeys what it is sent, so these surfaces render
// such strings as DATA -- and the choice of helper is the choice of what the
// surface promises.

// EscapeMultiline terminal-escapes hostile text LINE BY LINE -- EscapeTerminal
// strips every control character including newlines, which would collapse a
// rendered file body into one unreadable run. For text byre composed as a
// BLOCK and means to print as one: an error's own message, a preset body, a
// rendered reference list.
func EscapeMultiline(text string) string {
	lines := strings.Split(strings.TrimRight(text, "\n"), "\n")
	for i, l := range lines {
		lines[i] = packages.EscapeTerminal(l)
	}
	return strings.Join(lines, "\n")
}

// dataf formats one report to w with every ARGUMENT rendered as data: a
// string-shaped or error argument is stripped of control sequences and held to
// one line, so the only line breaks in the output are the ones byre's own
// format string put there. It is the funnel these surfaces print through, so
// that a new line of reporting is escaped by default rather than by memory.
//
// An argument its composer has ALREADY rendered for the terminal -- a
// multi-line block, or a line byre styled itself -- says so at the call site
// with escaped(), which names the judgment instead of hiding it (hostopen's
// Reason is the same idea).
func dataf(w io.Writer, format string, args ...any) {
	fmt.Fprintf(w, format, dataArgs(args)...)
}

// escaped marks an argument already rendered for the terminal by its composer.
// dataf passes it through untouched -- including any styling on it, which is
// why the preset review's TTY highlight survives the funnel.
type escaped string

func dataArgs(args []any) []any {
	out := make([]any, len(args))
	for i, a := range args {
		switch v := a.(type) {
		case escaped:
			out[i] = string(v)
			continue
		case error:
			out[i] = packages.EscapeTerminal(v.Error())
			continue
		}
		// Named string types (packages.Kind, runner.Engine) are strings to the
		// terminal too, so the check is the KIND rather than the type.
		if rv := reflect.ValueOf(a); rv.Kind() == reflect.String {
			out[i] = packages.EscapeTerminal(rv.String())
			continue
		}
		out[i] = a
	}
	return out
}
