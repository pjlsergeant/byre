package commands

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"image"
	_ "image/png" // registered so icon dimensions can be decoded
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/pjlsergeant/byre/internal/gen"
)

// `byre deliver --install-app` materializes the DELIVER APP (GLOSSARY): host
// drag targets whose only job is invoking `byre deliver` on what you drop.
// Everything is a readable generated artifact (the Dockerfile.generated
// pattern — ADR 0021): byre writes text, the machine's own tooling makes it
// runnable (osacompile assembles the .app from Apple's signed applet stub;
// Automator and desktop launchers read their files as-is). Nothing prebuilt
// ever crosses a machine boundary, which is why no signing certificate or
// notarization is ever involved. Install ad-hoc codesigns the bundle
// (`codesign --sign -`) because writing the script and icons into it
// invalidates the signature osacompile's applet stub carried.
//
// This file holds the pure generators; install.go-side wiring writes them.

// launchPATH widens the sparse Finder/Automator environment (PATH is just
// /usr/bin:/bin:/usr/sbin:/sbin there) so byre's CHILDREN resolve — the
// engine CLI above all: Docker Desktop symlinks into /usr/local/bin, brew
// podman into /opt/homebrew/bin. Field-found 2026-07-10: without this the
// Quick Action reported "no running byre boxes" with a box plainly running.
const launchPATH = `PATH="$PATH:/usr/local/bin:/opt/homebrew/bin:$HOME/.local/bin" `

// InstallAppOptions is the --install-app surface. SSH is the canonical
// ssh:// URL (empty = local); the commands package does not parse it.
type InstallAppOptions struct {
	Box, Name, RemoteByre, SSH string
}

// appInstall is one resolved install: display names, path fragments, and
// the baked deliver argv. Unnamed local (empty label) keeps today's
// singleton artifact names.
type appInstall struct {
	byrePath   string
	box        string
	name       string // --name as given (empty if derived from SSH)
	remoteByre string
	ssh        string
	label      string // display label; empty = unnamed local singleton
	display    string // "Byre Deliver" or "Byre Deliver (<label>)"
	fsLabel    string // macOS path component
	slug       string // Linux filename fragment
}

// ValidateInstallName reports whether --name can label an install: it
// must be valid UTF-8 (sanitization would otherwise silently rewrite
// invalid bytes to U+FFFD, so the rerun hint no longer reproduces the
// install), and after sanitization it must still name a file on both
// platforms. Exported so the CLI can refuse before dispatch (usage
// errors never dispatch).
func ValidateInstallName(name string) error {
	if !utf8.ValidString(name) {
		return fmt.Errorf("--name is not valid UTF-8")
	}
	if fsLabel(stripInvalidRunes(name)) == "" || linuxSlug(stripInvalidRunes(name)) == "" {
		return fmt.Errorf("--name has no usable characters")
	}
	return nil
}

func resolveInstall(byrePath string, o InstallAppOptions) (appInstall, error) {
	// The CLI rejects these up front; this guards direct API callers. Every
	// baked value lands in line-oriented UTF-8 text (AppleScript comments
	// and string literals, .desktop lines, a UTF-8-declared plist), where a
	// control character is a structural break shell quoting cannot contain
	// and an invalid byte makes the artifact not-text. The byre path is in
	// the same class: it is baked into every grammar too.
	for _, v := range []string{byrePath, o.Box, o.RemoteByre, o.SSH, o.Name} {
		if !utf8.ValidString(v) {
			return appInstall{}, fmt.Errorf("%q is not valid UTF-8 — generated launchers are UTF-8 text", v)
		}
	}
	for _, v := range []string{byrePath, o.Box, o.RemoteByre, o.SSH} {
		if strings.ContainsFunc(v, InvalidArtifactRune) {
			return appInstall{}, fmt.Errorf("characters in %q that a generated launcher cannot carry — line-oriented XML/UTF-8 text", v)
		}
	}
	a := appInstall{
		byrePath:   byrePath,
		box:        o.Box,
		remoteByre: o.RemoteByre,
		ssh:        o.SSH,
		display:    "Byre Deliver",
	}
	// The name reaches display strings (the .desktop Name= line, AppleScript
	// title literals) and the rerun headers, where a control character is a
	// structural break, not text — strip them before anything is derived.
	a.name = stripInvalidRunes(o.Name)
	if o.Name != "" && a.name == "" {
		return appInstall{}, fmt.Errorf("--name has no usable characters")
	}
	label := a.name
	if label == "" && o.SSH != "" {
		label = strings.TrimPrefix(o.SSH, "ssh://")
	}
	if label == "" {
		return a, nil
	}
	a.label = label
	a.display = "Byre Deliver (" + label + ")"
	a.fsLabel = fsLabel(label)
	a.slug = linuxSlug(label)
	if a.fsLabel == "" || a.slug == "" {
		return appInstall{}, fmt.Errorf("--name has no usable characters")
	}
	return a, nil
}

// fsLabel sanitizes a label for a macOS path component: / and : become
// '-', controls are stripped, leading dots and surrounding whitespace go.
func fsLabel(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch {
		case r == '/' || r == ':':
			b.WriteByte('-')
		case unicode.IsControl(r):
			// strip
		default:
			b.WriteRune(r)
		}
	}
	out := strings.TrimSpace(b.String())
	out = strings.TrimLeft(out, ".")
	return strings.TrimSpace(out)
}

// linuxSlug sanitizes a label for a .desktop filename fragment: runes
// outside [A-Za-z0-9._@-] become '-', then leading dots and dashes go.
func linuxSlug(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if linuxSlugOK(r) {
			b.WriteRune(r)
		} else {
			b.WriteByte('-')
		}
	}
	return strings.TrimLeft(b.String(), ".-")
}

// InvalidArtifactRune reports a rune no generated artifact grammar can
// carry: controls break line-oriented text, and U+FFFE/U+FFFF fall
// outside XML 1.0's Char production entirely — no escape can represent
// them in a plist. Exported for the CLI's pre-dispatch validation.
func InvalidArtifactRune(r rune) bool {
	return unicode.IsControl(r) || r == 0xFFFE || r == 0xFFFF
}

// stripInvalidRunes drops runes InvalidArtifactRune rejects; everything
// else passes through.
func stripInvalidRunes(s string) string {
	return strings.Map(func(r rune) rune {
		if InvalidArtifactRune(r) {
			return -1
		}
		return r
	}, s)
}

func linuxSlugOK(r rune) bool {
	switch {
	case r >= 'A' && r <= 'Z', r >= 'a' && r <= 'z', r >= '0' && r <= '9':
		return true
	case r == '.' || r == '_' || r == '@' || r == '-':
		return true
	}
	return false
}

func (a appInstall) extra(quote func(string) string) string {
	var b strings.Builder
	if a.ssh != "" {
		b.WriteByte(' ')
		b.WriteString(quote(a.ssh))
	}
	if a.box != "" {
		b.WriteString(" --box ")
		b.WriteString(quote(a.box))
	}
	if a.remoteByre != "" {
		b.WriteString(" --remote-byre ")
		b.WriteString(quote(a.remoteByre))
	}
	return b.String()
}

func (a appInstall) rerun() string {
	var b strings.Builder
	b.WriteString("byre deliver --install-app")
	if a.ssh != "" {
		b.WriteByte(' ')
		b.WriteString(gen.ShellQuote(a.ssh))
	}
	if a.box != "" {
		b.WriteString(" --box ")
		b.WriteString(gen.ShellQuote(a.box))
	}
	if a.name != "" {
		b.WriteString(" --name ")
		b.WriteString(gen.ShellQuote(a.name))
	}
	if a.remoteByre != "" {
		b.WriteString(" --remote-byre ")
		b.WriteString(gen.ShellQuote(a.remoteByre))
	}
	return b.String()
}

// idLine is the identity token a labeled artifact carries: the label,
// hex-encoded so the token is inert in every artifact grammar (an XML
// comment may not contain "--"; hex never forms one). Unnamed installs
// carry no line — absence IS the unnamed identity, which also keeps
// pre-feature artifacts regenerable. Sanitization is many-to-one, so the
// filename alone cannot prove two labels are the same install; this can.
func (a appInstall) idLine() string {
	if a.label == "" {
		return ""
	}
	return "byre-install-id:" + hex.EncodeToString([]byte(a.label))
}

// installIDLineRe is the exact identity line each grammar carries —
// anchored at BOTH ends, because the rerun hint earlier in the file
// carries user-supplied values that can themselves contain token-shaped
// text (user values are control-free, so they can never START a line),
// and an unbounded tail would read a malformed token's valid-hex prefix
// as a label. installIDPrefixRe detects token-SHAPED lines so a
// malformed one refuses rather than reading as unnamed.
var (
	installIDLineRe   = regexp.MustCompile(`(?m)^(?:(?:-- |# )byre-install-id:([0-9a-f]*)|<!-- byre-install-id:([0-9a-f]*) -->)$`)
	installIDPrefixRe = regexp.MustCompile(`(?m)^(?:-- |# |<!-- )byre-install-id:`)
)

// installIDOf is the label an artifact declares itself to belong to
// ("" = unnamed, or a pre-feature artifact). ok is false when the
// declaration can't be trusted — a malformed token line, two id lines,
// or undecodable hex — which no generated artifact contains; the caller
// refuses rather than guesses.
func installIDOf(content []byte) (label string, ok bool) {
	strict := installIDLineRe.FindAllSubmatch(content, -1)
	if len(installIDPrefixRe.FindAllIndex(content, -1)) != len(strict) {
		return "", false
	}
	if len(strict) == 0 {
		return "", true
	}
	if len(strict) > 1 {
		return "", false
	}
	hexPart := strict[0][1]
	if hexPart == nil {
		hexPart = strict[0][2]
	}
	decoded, err := hex.DecodeString(string(hexPart))
	if err != nil {
		return "", false
	}
	return string(decoded), true
}

func (a appInstall) menuItem() string {
	if a.label == "" {
		return "Deliver to Byre"
	}
	return "Deliver to Byre (" + a.label + ")"
}

// iconName is the Linux icon identity, per-install: a shared name would
// make one install's printed uninstall paths delete every sibling's
// icon. The slug is for the human; the hash is the identity — any plain
// suffix could be forged by a --name ending in it (a local 'far-ssh'
// colliding with remote 'far'), so the local/remote kind and the label
// are hashed with a separator no label can contain.
func (a appInstall) iconName() string {
	if a.label == "" {
		return "byre-deliver"
	}
	kind := "local"
	if a.ssh != "" {
		kind = "ssh"
	}
	sum := sha256.Sum256([]byte(kind + "\x00" + a.label))
	return "byre-deliver-" + a.slug + "-" + hex.EncodeToString(sum[:4])
}

func (a appInstall) iconPNG() []byte {
	if a.ssh != "" {
		return deliverIconSSHPNG
	}
	return deliverIconPNG
}

func (a appInstall) appBase() string {
	if a.label == "" {
		return "Byre Deliver"
	}
	return "Byre Deliver (" + a.fsLabel + ")"
}

func (a appInstall) darwinAppName() string    { return a.appBase() + ".app" }
func (a appInstall) darwinStagedName() string { return "." + a.appBase() + ".staged.app" }
func (a appInstall) darwinBackupName() string { return "." + a.appBase() + ".previous.app" }

func (a appInstall) darwinWorkflowName() string {
	if a.label == "" {
		return "Deliver to Byre.workflow"
	}
	return "Deliver to Byre (" + a.fsLabel + ").workflow"
}

func (a appInstall) linuxDesktopName() string {
	if a.label == "" {
		return "byre-deliver.desktop"
	}
	return "byre-deliver-" + a.slug + ".desktop"
}

// deliverAppSource is the AppleScript for the "Byre Deliver" app (the drag
// target half of the deliver app -- GLOSSARY). The byre
// path is baked at generation time (Finder launches carry a sparse PATH);
// well-known locations are fallbacks, and byre-not-found is reported via
// notification — a Dock launch has no terminal to print to.
func deliverAppSource(a appInstall) string {
	extra := a.extra(gen.ShellQuote)
	idComment := ""
	if a.idLine() != "" {
		idComment = "-- " + a.idLine() + "\n"
	}
	return `-- Generated by byre. Do not edit; re-run: ` + a.rerun() + `
` + idComment + `-- Byre Deliver: drop files here to deliver them into your running byre
-- box's /inbox; open it with nothing to deliver the CLIPBOARD instead.
-- byre prints the landed path, copies it to your clipboard, and reports
-- via notification. The binary below generated this app.

property byrePath : "` + asQuote(a.byrePath) + `"

on byreBinary()
	set candidates to {byrePath, "/opt/homebrew/bin/byre", "/usr/local/bin/byre", (POSIX path of (path to home folder)) & ".local/bin/byre"}
	repeat with c in candidates
		try
			do shell script "test -x " & quoted form of c
			return c as string
		end try
	end repeat
	display dialog "byre not found — re-run 'byre deliver --install-app' after reinstalling byre" with title "` + asQuote(a.display) + `" buttons {"OK"} default button 1 with icon caution
	error "byre not found"
end byreBinary

on open droppedItems
	set args to ""
	repeat with f in droppedItems
		set args to args & " " & quoted form of POSIX path of f
	end repeat
	runByre(args)
end open

on run
	-- A plain click (no drop) opens a TERMINAL running byre deliver: the
	-- interactive paste beat, with its sampled what's-on-your-clipboard
	-- prompt, is the point — silently shipping the clipboard from a Dock
	-- click would skip the one moment to notice the wrong thing.
	-- Terminal's scripting exposes no exit codes, so the command drops a
	-- flag file on failure: success closes the window (whatever the
	-- profile says); a failure's window stays open to read.
	set failFlag to do shell script "mktemp -u /tmp/byre-deliver-fail.XXXXXX"
	-- clear first: the window is dedicated, and byre's paste prompt should
	-- be the first thing read — not the login banner and the echoed command.
	set cmd to "clear; ` + asQuote(launchPATH) + `" & quoted form of byreBinary() & " deliver` + asQuote(extra) + ` || touch " & quoted form of failFlag & "; exit"
	-- Launching Terminal opens its default startup window; do script would
	-- then add a SECOND one (field-found: a stray unused window). When
	-- Terminal has only just launched, run in that startup window instead —
	-- but never hijack a window of an already-running Terminal, and only
	-- reuse one that is sitting idle.
	set sawBusy to false
	set wasRunning to application "Terminal" is running
	tell application "Terminal"
		activate
		if not wasRunning and (count of windows) > 0 and busy of selected tab of front window is false then
			set t to do script cmd in front window
		else
			set t to do script cmd
		end if
		set theTTY to tty of t
		try
			-- Wait for the run to START before waiting for it to end:
			-- do script types the command and the shell takes a beat to
			-- launch byre, so an immediate busy-poll sees idle and closed
			-- the window ONTO the running process (field-found: Terminal's
			-- terminate-processes dialog before anything else happened).
			set waited to 0
			repeat until busy of t or waited ≥ 50
				delay 0.1
				set waited to waited + 1
			end repeat
			if busy of t then set sawBusy to true
			repeat while busy of t
				delay 0.2
			end repeat
		end try
	end tell
	set failed to false
	try
		do shell script "test -e " & quoted form of failFlag
		set failed to true
	end try
	-- Close ONLY a run we actually observed start and finish: never-went-
	-- busy is UNKNOWN (slow shell start, an Automation prompt), and closing
	-- on unknown re-opens the terminate-processes race. Unknown leaves the
	-- window; the user sees whatever it's doing.
	if failed then
		do shell script "rm -f " & quoted form of failFlag
	else if sawBusy then
		delay 0.5 -- let the final output land before the window goes
		tell application "Terminal"
			try
				close (every window whose tty of selected tab is theTTY)
			end try
		end tell
	end if
end run

on runByre(args)
	try
		-- The PATH prefix reaches byre's children (the docker/podman CLI):
		-- Finder launches carry a sparse PATH that can't see Docker Desktop.
		do shell script "` + asQuote(launchPATH) + `" & quoted form of byreBinary() & " deliver` + asQuote(extra) + `" & args & " </dev/null"
	on error errMsg
		-- byre shows its own outcomes; this catches byre failing to RUN.
		-- A dialog, not a notification: banners are permission-gated.
		display dialog errMsg with title "` + asQuote(a.display) + `" buttons {"OK"} default button 1 with icon caution
	end try
end runByre
`
}

// quickActionFiles is the Finder Quick Action ("Deliver to Byre"): an
// Automator .workflow bundle — two plists, read by the OS as-is.
func quickActionFiles(a appInstall) (infoPlist, documentWflow string) {
	extra := a.extra(gen.ShellQuote)
	// The rerun hint rides the shell script, not an XML comment: comments
	// may not contain "--" at all (entity spelling doesn't help — comments
	// aren't entity-decoded), and every rerun contains "--install-app".
	command := "# Generated by byre. Do not edit; re-run: " + a.rerun() + "\n" +
		launchPATH + gen.ShellQuote(a.byrePath) + " deliver" + extra + ` "$@" </dev/null`
	idComment := ""
	if a.idLine() != "" {
		idComment = "\n<!-- " + a.idLine() + " -->"
	}
	infoPlist = `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>NSServices</key>
	<array>
		<dict>
			<key>NSMenuItem</key>
			<dict>
				<key>default</key>
				<string>` + xmlEscape(a.menuItem()) + `</string>
			</dict>
			<key>NSMessage</key>
			<string>runWorkflowAsService</string>
			<key>NSSendFileTypes</key>
			<array>
				<string>public.item</string>
			</array>
		</dict>
	</array>
</dict>
</plist>
`
	documentWflow = `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<!-- Generated by byre. Do not edit. -->` + idComment + `
<plist version="1.0">
<dict>
	<key>AMApplicationBuild</key>
	<string>528</string>
	<key>AMApplicationVersion</key>
	<string>2.10</string>
	<key>AMDocumentVersion</key>
	<string>2</string>
	<key>actions</key>
	<array>
		<dict>
			<key>action</key>
			<dict>
				<key>AMActionVersion</key>
				<string>2.0.3</string>
				<key>AMParameterProperties</key>
				<dict>
					<key>COMMAND_STRING</key>
					<dict/>
					<key>CheckedForUserDefaultShell</key>
					<dict/>
					<key>inputMethod</key>
					<dict/>
					<key>shell</key>
					<dict/>
					<key>source</key>
					<dict/>
				</dict>
				<key>ActionBundlePath</key>
				<string>/System/Library/Automator/Run Shell Script.action</string>
				<key>ActionName</key>
				<string>Run Shell Script</string>
				<key>ActionParameters</key>
				<dict>
					<key>COMMAND_STRING</key>
					<string>` + xmlEscape(command) + `</string>
					<key>CheckedForUserDefaultShell</key>
					<true/>
					<key>inputMethod</key>
					<integer>1</integer>
					<key>shell</key>
					<string>/bin/bash</string>
					<key>source</key>
					<string></string>
				</dict>
				<key>BundleIdentifier</key>
				<string>com.apple.RunShellScript</string>
				<key>CFBundleVersion</key>
				<string>2.0.3</string>
				<key>CanShowSelectedItemsWhenRun</key>
				<false/>
				<key>CanShowWhenRun</key>
				<true/>
				<key>Class Name</key>
				<string>RunShellScriptAction</string>
				<key>InputUUID</key>
				<string>6E9F7A2C-0000-4000-8000-000000000001</string>
				<key>Keywords</key>
				<array>
					<string>Shell</string>
				</array>
				<key>OutputUUID</key>
				<string>6E9F7A2C-0000-4000-8000-000000000002</string>
				<key>UUID</key>
				<string>6E9F7A2C-0000-4000-8000-000000000003</string>
				<key>ignoresInput</key>
				<false/>
			</dict>
		</dict>
	</array>
	<key>connectors</key>
	<dict/>
	<key>workflowMetaData</key>
	<dict>
		<key>applicationBundleIDsByPath</key>
		<dict/>
		<key>applicationPaths</key>
		<array/>
		<key>inputTypeIdentifier</key>
		<string>com.apple.Automator.fileSystemObject</string>
		<key>outputTypeIdentifier</key>
		<string>com.apple.Automator.nothing</string>
		<key>presentationMode</key>
		<integer>15</integer>
		<key>processesInput</key>
		<false/>
		<key>serviceInputTypeIdentifier</key>
		<string>com.apple.Automator.fileSystemObject</string>
		<key>serviceOutputTypeIdentifier</key>
		<string>com.apple.Automator.nothing</string>
		<key>serviceProcessesInput</key>
		<false/>
		<key>systemImageName</key>
		<string>NSActionTemplate</string>
		<key>useAutomaticInputType</key>
		<false/>
		<key>workflowTypeIdentifier</key>
		<string>com.apple.Automator.servicesMenu</string>
	</dict>
</dict>
</plist>
`
	return infoPlist, documentWflow
}

// desktopEntry is the Linux launcher: every Exec argument is quoted per the
// Desktop Entry spec, and % is doubled so user input can't smuggle a field
// code — --box accepts arbitrary text, not just byre-shaped slugs.
func desktopEntry(a appInstall) string {
	extra := a.extra(desktopExecQuote)
	idComment := ""
	if a.idLine() != "" {
		idComment = "# " + a.idLine() + "\n"
	}
	return `# Generated by byre. Do not edit; re-run: ` + a.rerun() + `
` + idComment + `[Desktop Entry]
Type=Application
Name=` + desktopValueEscape(a.display) + `
Comment=Deliver files to your running byre box's /inbox (run with no files to deliver the clipboard)
Exec=` + desktopExecQuote(a.byrePath) + ` deliver` + extra + ` %F
Icon=` + a.iconName() + `
Terminal=false
Categories=Utility;Development;
`
}

// packICNS wraps one PNG as a .icns (modern icns chunks carry PNG bytes
// directly — no Apple tooling involved). The source must be square at a
// size with an icns type: 128/256/512/1024; macOS scales one good high-res
// representation down gracefully.
func packICNS(pngBytes []byte) ([]byte, error) {
	cfg, format, err := image.DecodeConfig(bytes.NewReader(pngBytes))
	if err != nil || format != "png" {
		return nil, fmt.Errorf("icon must be a PNG: %v", err)
	}
	if cfg.Width != cfg.Height {
		return nil, fmt.Errorf("icon must be square (got %dx%d)", cfg.Width, cfg.Height)
	}
	types := map[int]string{128: "ic07", 256: "ic08", 512: "ic09", 1024: "ic10"}
	typ, ok := types[cfg.Width]
	if !ok {
		return nil, fmt.Errorf("icon must be 128, 256, 512, or 1024 px square (got %d)", cfg.Width)
	}
	var out bytes.Buffer
	chunkLen := uint32(8 + len(pngBytes))
	total := uint32(8) + chunkLen
	out.WriteString("icns")
	_ = binary.Write(&out, binary.BigEndian, total)
	out.WriteString(typ)
	_ = binary.Write(&out, binary.BigEndian, chunkLen)
	out.Write(pngBytes)
	return out.Bytes(), nil
}

// asQuote escapes for an AppleScript string literal.
func asQuote(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	return strings.ReplaceAll(s, `"`, `\"`)
}

// xmlEscape escapes for plist string content.
func xmlEscape(s string) string {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;")
	return r.Replace(s)
}

// desktopValueEscape escapes a Desktop Entry string value: values
// interpret backslash sequences (\n, \s, ...), so a literal backslash
// doubles — otherwise a backslash-bearing label misrenders. Controls
// are rejected upstream; nothing else needs escaping in a Name value.
func desktopValueEscape(s string) string {
	return strings.ReplaceAll(s, `\`, `\\`)
}

// desktopExecQuote quotes one Exec argument per the Desktop Entry spec.
// % always doubles (a bare % is a field-code position regardless of quoting).
func desktopExecQuote(s string) string {
	s = strings.ReplaceAll(s, "%", "%%")
	if !strings.ContainsAny(s, " \t\"'\\><~|&;$*?#()`") {
		return s
	}
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	return `"` + s + `"`
}
