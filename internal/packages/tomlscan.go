package packages

import "strings"

// tomlStringState tracks, across a line-based scan of a TOML document,
// whether the scan currently sits inside a multiline string — so header
// and marker detection can tell a real `[package]` line from identical
// text embedded in a `"""`/`”'` value (a Dockerfile heredoc is the
// canonical case). Only multiline strings can span lines, so this is the
// entire cross-line state a line scanner needs.
type tomlStringState int

const (
	tomlOutside   tomlStringState = iota
	tomlMLBasic                   // inside """ ... """
	tomlMLLiteral                 // inside ''' ... '''
)

// advance consumes one line and returns the state after it. Inside a
// multiline basic string a backslash escapes the next character; literal
// strings have no escapes. Outside strings, single-line strings are skipped
// (so quotes inside them can't open a multiline state) and `#` starts a
// comment, ending the scan. An unterminated single-line string — invalid
// TOML — is treated as ending at EOL.
func (s tomlStringState) advance(line string) tomlStringState {
	i := 0
	for i < len(line) {
		switch s {
		case tomlMLLiteral:
			j := strings.Index(line[i:], "'''")
			if j < 0 {
				return s
			}
			i += j + 3
			s = tomlOutside
		case tomlMLBasic:
			closed := false
			for i < len(line) {
				if line[i] == '\\' {
					i += 2
					continue
				}
				if strings.HasPrefix(line[i:], `"""`) {
					i += 3
					s = tomlOutside
					closed = true
					break
				}
				i++
			}
			if !closed {
				return s
			}
		default: // outside any string
			switch c := line[i]; {
			case c == '#':
				return s
			case strings.HasPrefix(line[i:], `"""`):
				s = tomlMLBasic
				i += 3
			case c == '"':
				i++
				for i < len(line) {
					if line[i] == '\\' {
						i += 2
						continue
					}
					if line[i] == '"' {
						i++
						break
					}
					i++
				}
			case strings.HasPrefix(line[i:], "'''"):
				s = tomlMLLiteral
				i += 3
			case c == '\'':
				i++
				if j := strings.IndexByte(line[i:], '\''); j >= 0 {
					i += j + 1
				} else {
					i = len(line)
				}
			default:
				i++
			}
		}
	}
	return s
}
