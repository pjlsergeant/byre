// Package config loads and resolves byre's configuration cascade:
//
//	~/.byre/default.config  ⊕  ~/.byre/templates/<name>/template.config  ⊕  <project>/byre.config
//
// Files are TOML; byre layers its own merge semantics on top (scalars override,
// lists union, maps merge, `!name` removes a named entry an earlier layer added).
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"

	"byre/internal/project"
)

// ProjectConfigName is the fixed per-project config filename.
const ProjectConfigName = "byre.config"

// volumeNameRe restricts a volume name to Docker's allowed character set. byre
// derives the actual volume name `byre-<id>-<name>`, whose prefix is already
// alphanumeric, so the name itself need not start alphanumeric — this allows
// dotfile-style state volumes like `.claude` / `.codex` / `.gemini`.
var volumeNameRe = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)

// These allowlists constrain the TYPED config fields that byre interpolates into
// generated Dockerfile/shell syntax, so a config or third-party skill can't smuggle
// executable content through a field that looks like inert data. They are NOT a
// general-purpose sanitizer: raw Dockerfile blocks (dockerfile_pre/post) and
// run_args are the sanctioned, consent-surfaced escape hatches for anything these
// reject. See ValidateContent.
var (
	// imageRefRe is the standard OCI image-reference charset. `base` is emitted as
	// `FROM <base>`, so anything outside this set (notably whitespace or a newline)
	// could append a second Dockerfile instruction.
	imageRefRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:/@-]*$`)
	// envKeyRe is a POSIX-shell environment variable name. Keys are emitted as
	// `ENV <key>=...` unquoted; a space or newline in the key would inject.
	envKeyRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
	// packageRe covers real apt and npm package specs — scoped names (@scope/pkg),
	// version pins (pkg=1.2.3, pkg@1.2.3), and semver-ish markers (~^) — while
	// excluding whitespace and every shell metacharacter, since apt/npm_global
	// entries are joined into a `RUN apt-get install`/`npm install -g` shell line.
	packageRe = regexp.MustCompile(`^[A-Za-z0-9@][A-Za-z0-9@/._+:=~^-]*$`)
)

// Mount is a host-bind mount. Identity for `!name` removal is Target.
type Mount struct {
	Host   string `toml:"host"`
	Target string `toml:"target"`
	Mode   string `toml:"mode"` // ro|rw; default ro
}

// Port publishes a container port to the host (docker -p). Interface defaults to
// 127.0.0.1 (localhost-only — exposing to the LAN is a louder, explicit choice);
// Host 0 means "mirror the container port on the host" (the predictable default
// a dev box wants, not a random/ephemeral port).
type Port struct {
	Container int    `toml:"container"`           // container port (1-65535)
	Host      int    `toml:"host,omitempty"`      // host port; 0 = same as Container
	Interface string `toml:"interface,omitempty"` // bind interface; default 127.0.0.1
}

// portEffective resolves a port's effective bind interface and host port (the
// defaults byre applies at run time), used for dedup and collision checks.
func portEffective(p Port) (iface string, host int) {
	iface = p.Interface
	if iface == "" {
		iface = "127.0.0.1"
	}
	host = p.Host
	if host == 0 {
		host = p.Container
	}
	return iface, host
}

// Seed initializes a fresh state volume once. Exactly one source is set.
type Seed struct {
	Host    string `toml:"host"`    // host path (preferred; secret stays off-config)
	Literal string `toml:"literal"` // inline non-secret content (requires Path)
	Path    string `toml:"path"`    // for literal: destination file within the volume
}

// Volume is a named volume. Identity for `!name` removal is Name.
type Volume struct {
	Name   string `toml:"name"`
	Role   string `toml:"role"`   // cache|state
	Target string `toml:"target"` // mount path in the container
	Seed   *Seed  `toml:"seed"`   // state-only, optional
}

// Config is one resolved (or single-layer) byre configuration. omitempty keeps
// regenerated config files (byre config) clean — only set fields are written.
type Config struct {
	Engine     string `toml:"engine,omitempty"`     // auto|docker|podman
	Template   string `toml:"template,omitempty"`   // template name to layer in
	Agent      string `toml:"agent,omitempty"`      // claude|codex|gemini (enables its skill)
	Base       string `toml:"base,omitempty"`       // base image
	Dockerfile string `toml:"dockerfile,omitempty"` // full hand-written Dockerfile opt-out (path)

	// SeedPrefs opts into a one-time copy of the selected agent's curated, non-secret
	// pref files (theme, keybindings — see the skill's [agent.prefs]) from the host
	// into a FRESH agent state volume. Off by default; only acts on a fresh volume.
	SeedPrefs bool `toml:"seed_prefs,omitempty"`

	// WorktreeBase controls where `byre worktree` creates worktrees (leaf:
	// <repo>-<name>). Three values: unset -> refuse (byre won't guess a location);
	// "sibling" -> beside the repo; or a host path (e.g. ~/worktrees, `~` expands)
	// -> under it. A host workflow preference, normally set in
	// ~/.byre/default.config; not part of the container/sandbox, just where the
	// checkout lands. Edited via `byre config` (the WORKTREES section).
	WorktreeBase string `toml:"worktree_base,omitempty"`

	Apt       []string          `toml:"apt,omitempty"`
	NpmGlobal []string          `toml:"npm_global,omitempty"`
	Env       map[string]string `toml:"env,omitempty"`
	Files     map[string]string `toml:"files,omitempty"`
	Skills    []string          `toml:"skills,omitempty"`
	Mounts    []Mount           `toml:"mounts,omitempty"`
	Volumes   []Volume          `toml:"volumes,omitempty"`
	Ports     []Port            `toml:"ports,omitempty"`

	DockerfilePre  []string `toml:"dockerfile_pre,omitempty"`
	DockerfilePost []string `toml:"dockerfile_post,omitempty"`
	RunArgs        []string `toml:"run_args,omitempty"`
}

// Load resolves the full cascade for a project directory and validates the
// result. Missing config files are treated as empty layers; only malformed TOML
// or an invalid resolved config is an error.
func Load(projectDir string) (Config, error) {
	paths, err := project.Resolve(projectDir)
	if err != nil {
		return Config{}, err
	}
	// The project config is read from the host-side store
	// (~/.byre/projects/<id>/byre.config), NOT from the project tree. byre never
	// trusts a config that the (rw-mounted) project could contain — a committed
	// <project>/byre.config is adopted into the store by an explicit, host-side
	// human action (see commands adopt), never read directly here.
	proj, err := loadFile(filepath.Join(paths.Dir, ProjectConfigName))
	if err != nil {
		return Config{}, err
	}
	return resolveWith(paths.Home, proj)
}

// ResolveProposed resolves the cascade as if proj were the project layer
// (default ⊕ template ⊕ proj), so a PROPOSED <project>/byre.config can be shown
// with its EFFECTIVE settings (incl. the grants a selected template adds) before
// a human adopts it — without ever making it live.
func ResolveProposed(proj Config) (Config, error) {
	home, err := project.Home()
	if err != nil {
		return Config{}, err
	}
	return resolveWith(home, proj)
}

// resolveWith applies the cascade default ⊕ template ⊕ proj.
func resolveWith(home string, proj Config) (Config, error) {
	def, err := loadFile(filepath.Join(home, "default.config"))
	if err != nil {
		return Config{}, err
	}
	// default.config's `template`/`agent` are the first-run picker's PRE-SELECTED
	// options only — they must not silently cascade into a project's resolved
	// config (a project's template/agent come from its own byre.config, which the
	// picker writes). default.config still contributes base/apt/env/etc. The
	// picker reads the favourites from the file directly (onboard.Favourites).
	def.Template = ""
	def.Agent = ""

	// The template name is itself a config value; only the project layer selects
	// it. The template layer then sits in the middle of the cascade:
	// default ⊕ template ⊕ project.
	templateName := proj.Template
	var tmpl Config
	if templateName != "" {
		tmplPath := filepath.Join(home, "templates", templateName, "template.config")
		// A selected template is an explicit dependency — a typo must fail
		// loudly, not silently fall back to defaults.
		if _, statErr := os.Stat(tmplPath); statErr != nil {
			if errors.Is(statErr, os.ErrNotExist) {
				return Config{}, fmt.Errorf("template %q not found (looked for %s)", templateName, tmplPath)
			}
			return Config{}, statErr
		}
		tmpl, err = loadFile(tmplPath)
		if err != nil {
			return Config{}, err
		}
	}

	resolved := Merge(Merge(def, tmpl), proj)
	if err := resolved.Validate(); err != nil {
		return Config{}, err
	}
	return resolved, nil
}

// loadFile decodes one TOML layer. A missing file is an empty layer; an unknown
// key is an error (catches typos in a config that would otherwise be ignored).
// ParseFile parses a single byre.config file (no cascade), for inspecting a
// proposed config before adopting it. A missing file yields a zero Config.
func ParseFile(path string) (Config, error) {
	return loadFile(path)
}

func loadFile(path string) (Config, error) {
	var c Config
	md, err := toml.DecodeFile(path, &c)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Config{}, nil
		}
		return Config{}, fmt.Errorf("%s: %w", path, err)
	}
	if und := md.Undecoded(); len(und) > 0 {
		return Config{}, fmt.Errorf("%s: unknown key(s): %v", path, und)
	}
	return c, nil
}

// Merge layers over onto base per byre's rules and returns the result.
func Merge(base, over Config) Config {
	out := base

	// Scalars: a non-empty over value wins.
	out.Engine = override(base.Engine, over.Engine)
	out.Template = override(base.Template, over.Template)
	out.Agent = override(base.Agent, over.Agent)
	out.Base = override(base.Base, over.Base)
	out.Dockerfile = override(base.Dockerfile, over.Dockerfile)
	// Bool opt-in: enabled if any layer sets it (default/template never do in
	// practice, so this is effectively "project wins"). A bool can't distinguish
	// unset from false, so there's no "turn it back off" in a higher layer.
	out.SeedPrefs = base.SeedPrefs || over.SeedPrefs
	out.WorktreeBase = override(base.WorktreeBase, over.WorktreeBase)

	// Package lists: plain dedup union. `!name` removal is reserved for named
	// lists (skills/mounts/volumes), so a package literally named "!x" is kept.
	out.Apt = unionStrings(base.Apt, over.Apt)
	out.NpmGlobal = unionStrings(base.NpmGlobal, over.NpmGlobal)
	// Skills: union with `!name` removal.
	out.Skills = mergeStrings(base.Skills, over.Skills)

	// Maps: union, over wins per key.
	out.Env = mergeMap(base.Env, over.Env)
	out.Files = mergeMap(base.Files, over.Files)

	// Structured named lists: union keyed by identity, with `!name` removal.
	out.Mounts = mergeMounts(base.Mounts, over.Mounts)
	out.Volumes = mergeVolumes(base.Volumes, over.Volumes)
	out.Ports = mergePorts(base.Ports, over.Ports)

	// Raw blocks: append-only/union, no per-line removal in v0.
	out.DockerfilePre = appendAll(base.DockerfilePre, over.DockerfilePre)
	out.DockerfilePost = appendAll(base.DockerfilePost, over.DockerfilePost)
	out.RunArgs = appendAll(base.RunArgs, over.RunArgs)

	return out
}

// Validate checks the resolved config for v0-supported, well-formed values.
// validContainerTarget requires an in-container mount/volume target to be an
// absolute path with no control characters and no comma. Absolute keeps mounts
// unambiguous; rejecting control chars (esp. CR/LF) stops a target from injecting
// a new line into a generated Dockerfile RUN; rejecting the comma stops it from
// injecting extra fields into docker's comma-delimited `--mount` value (e.g. a
// target `/x,readonly` flipping the mode, or a volume opt remounting the host).
func validContainerTarget(t string) error {
	if !strings.HasPrefix(t, "/") {
		return errors.New("must be an absolute path")
	}
	if i := strings.IndexFunc(t, func(r rune) bool { return r < 0x20 }); i >= 0 {
		return errors.New("must not contain control characters")
	}
	if strings.Contains(t, ",") {
		return errors.New("must not contain a comma (docker --mount is comma-delimited)")
	}
	return nil
}

// ValidateContent checks the typed config fields that byre interpolates into
// generated Dockerfile or shell syntax against strict allowlists (see imageRefRe,
// envKeyRe, packageRe). It is split out from Validate because it applies to every
// content-bearing source — the resolved config AND each skill's build contribution
// — so both are held to the same anti-injection bar. Fields byre only ever emits
// %q-quoted (env values, file paths) are safe by construction and not checked here.
func ValidateContent(base string, apt, npm []string, env map[string]string) error {
	if base != "" && !imageRefRe.MatchString(base) {
		return fmt.Errorf("base image %q: not a valid image reference (use dockerfile_pre for anything unusual)", base)
	}
	for _, p := range apt {
		if !packageRe.MatchString(p) {
			return fmt.Errorf("apt package %q: not a valid package name", p)
		}
	}
	for _, p := range npm {
		if !packageRe.MatchString(p) {
			return fmt.Errorf("npm_global package %q: not a valid package spec", p)
		}
	}
	for k := range env {
		if !envKeyRe.MatchString(k) {
			return fmt.Errorf("env key %q: not a valid environment variable name", k)
		}
	}
	return nil
}

func (c Config) Validate() error {
	switch c.Engine {
	case "", "auto", "docker", "podman":
	default:
		return fmt.Errorf("engine: %q invalid (want auto|docker|podman)", c.Engine)
	}

	// Anti-injection allowlists for the typed fields byre interpolates into
	// generated Dockerfile/shell. Skills' own apt/npm/env are held to the same bar
	// where their build blocks are resolved (see internal/skills).
	if err := ValidateContent(c.Base, c.Apt, c.NpmGlobal, c.Env); err != nil {
		return err
	}

	// Full hand-written Dockerfile opt-out: a project-relative path. byre builds
	// it (from the project dir) instead of generating, and the user owns the
	// infra layer — including the dev user and its ownership (byre passes no
	// UID/GID build args on this path); byre still owns runtime.
	if c.Dockerfile != "" && !relSafe(c.Dockerfile) {
		return fmt.Errorf("dockerfile = %q: must be a relative path within the project", c.Dockerfile)
	}

	// worktree_base: "" (refuse), "sibling", or a host path (~ or absolute, and
	// comma-free — `byre worktree` binds it, and docker --mount can't express a
	// comma). Caught here so a bad value is rejected at save, not at worktree time.
	if b := c.WorktreeBase; b != "" && b != "sibling" {
		if b != "~" && !strings.HasPrefix(b, "~/") && !filepath.IsAbs(b) {
			return fmt.Errorf("worktree_base = %q: must be \"sibling\", ~/…, or an absolute path", b)
		}
		if strings.Contains(b, ",") {
			return fmt.Errorf("worktree_base = %q: cannot contain a comma (docker --mount can't express it)", b)
		}
	}

	// Container targets must be unique across mounts and volumes — they become
	// distinct `docker run` mount points; a collision is ambiguous.
	targets := map[string]string{} // target -> what claims it

	for _, m := range c.Mounts {
		if m.Host == "" {
			return fmt.Errorf("mount %s: host path is required", m.Target)
		}
		if m.Target == "" {
			return errors.New("mount: target is required")
		}
		if err := validContainerTarget(m.Target); err != nil {
			return fmt.Errorf("mount target %q: %w", m.Target, err)
		}
		switch m.Mode {
		case "", "ro", "rw":
		default:
			return fmt.Errorf("mount %s: mode %q invalid (want ro|rw)", m.Target, m.Mode)
		}
		if claimed := targets[m.Target]; claimed != "" {
			return fmt.Errorf("mount target %s collides with %s", m.Target, claimed)
		}
		targets[m.Target] = "mount " + m.Target
	}

	names := map[string]bool{}
	for _, v := range c.Volumes {
		if v.Name == "" {
			return errors.New("volume: name is required")
		}
		if !volumeNameRe.MatchString(v.Name) {
			return fmt.Errorf("volume %q: name has characters not allowed in a docker volume name", v.Name)
		}
		if names[v.Name] {
			return fmt.Errorf("volume %s: duplicate name", v.Name)
		}
		names[v.Name] = true
		switch v.Role {
		case "cache", "state":
		default:
			return fmt.Errorf("volume %s: role %q invalid (want cache|state)", v.Name, v.Role)
		}
		if v.Target == "" {
			return fmt.Errorf("volume %s: target is required", v.Name)
		}
		if err := validContainerTarget(v.Target); err != nil {
			return fmt.Errorf("volume %s target %q: %w", v.Name, v.Target, err)
		}
		if claimed := targets[v.Target]; claimed != "" {
			return fmt.Errorf("volume %s target %s collides with %s", v.Name, v.Target, claimed)
		}
		targets[v.Target] = "volume " + v.Name
		if v.Seed != nil {
			if v.Role != "state" {
				return fmt.Errorf("volume %s: seed is only valid for state-role volumes", v.Name)
			}
			if v.Seed.Host != "" && v.Seed.Literal != "" {
				return fmt.Errorf("volume %s: seed has both host and literal (choose one)", v.Name)
			}
			if v.Seed.Host == "" && v.Seed.Literal == "" {
				return fmt.Errorf("volume %s: seed set but empty", v.Name)
			}
			if v.Seed.Literal != "" {
				if v.Seed.Path == "" {
					return fmt.Errorf("volume %s: literal seed requires a path (destination file in the volume)", v.Name)
				}
				if !relSafe(v.Seed.Path) {
					return fmt.Errorf("volume %s: literal seed path %q must be relative and not escape the volume", v.Name, v.Seed.Path)
				}
			}
			if v.Seed.Host != "" && v.Seed.Path != "" {
				return fmt.Errorf("volume %s: seed path is only for literal seeds", v.Name)
			}
		}
	}

	// Ports: container required in range; host 0 (= same as container) or in range;
	// no two bindings collide on the same effective interface:host, and a binding
	// on 0.0.0.0 (all interfaces) can't share a host port with any other interface
	// — docker would fail at run time in both cases.
	byHostPort := map[int]map[string]bool{} // effective host port -> set of interfaces
	for _, p := range c.Ports {
		if p.Container < 1 || p.Container > 65535 {
			return fmt.Errorf("port: container port %d out of range (1-65535)", p.Container)
		}
		if p.Host < 0 || p.Host > 65535 {
			return fmt.Errorf("port %d: host port %d out of range (0-65535; 0 = same as the container port)", p.Container, p.Host)
		}
		if strings.IndexFunc(p.Interface, func(r rune) bool { return r < 0x20 }) >= 0 {
			return fmt.Errorf("port %d: interface must not contain control characters", p.Container)
		}
		iface, host := portEffective(p)
		ifaces := byHostPort[host]
		if ifaces == nil {
			ifaces = map[string]bool{}
			byHostPort[host] = ifaces
		}
		if ifaces[iface] {
			return fmt.Errorf("port: host binding %s:%d is used by two ports", iface, host)
		}
		ifaces[iface] = true
	}
	for host, ifaces := range byHostPort {
		if ifaces["0.0.0.0"] && len(ifaces) > 1 {
			return fmt.Errorf("port: host port %d is bound on 0.0.0.0 and another interface (0.0.0.0 already covers all)", host)
		}
	}
	return nil
}

// relSafe reports whether p is a relative path that stays within its root (no
// absolute path, no ".." escape).
func relSafe(p string) bool {
	if filepath.IsAbs(p) {
		return false
	}
	clean := filepath.Clean(p)
	return clean != ".." && !strings.HasPrefix(clean, ".."+string(filepath.Separator))
}

func override(base, over string) string {
	if over != "" {
		return over
	}
	return base
}

// unionStrings unions base with over, deduping and preserving first occurrence.
// No `!name` handling — for plain lists like apt/npm_global.
func unionStrings(base, over []string) []string {
	out := append([]string{}, base...)
	for _, it := range over {
		if !containsString(out, it) {
			out = append(out, it)
		}
	}
	return out
}

// mergeStrings unions base with over (dedup, preserving first occurrence), then
// applies `!name` removals listed in over.
func mergeStrings(base, over []string) []string {
	out := append([]string{}, base...)
	var removals []string
	for _, it := range over {
		if name, ok := strings.CutPrefix(it, "!"); ok {
			removals = append(removals, name)
			continue
		}
		if !containsString(out, it) {
			out = append(out, it)
		}
	}
	for _, rm := range removals {
		out = removeString(out, rm)
	}
	return out
}

func mergeMap(base, over map[string]string) map[string]string {
	if base == nil && over == nil {
		return nil
	}
	out := map[string]string{}
	for k, v := range base {
		out[k] = v
	}
	for k, v := range over {
		out[k] = v
	}
	return out
}

func mergeMounts(base, over []Mount) []Mount {
	out := append([]Mount{}, base...)
	var removals []string
	for _, m := range over {
		if name, ok := strings.CutPrefix(m.Target, "!"); ok {
			removals = append(removals, name)
			continue
		}
		replaced := false
		for i := range out {
			if out[i].Target == m.Target {
				out[i] = m
				replaced = true
				break
			}
		}
		if !replaced {
			out = append(out, m)
		}
	}
	for _, rm := range removals {
		out = filterMounts(out, func(m Mount) bool { return m.Target != rm })
	}
	return out
}

// mergePorts unions port bindings, deduping by EFFECTIVE identity so an override
// that spells out the defaults (e.g. adds interface=127.0.0.1, or host equal to
// the container port) collapses onto the base entry instead of colliding. (No
// `!name` identity — a port has no name; a real host-port clash is Validate's job.)
func mergePorts(base, over []Port) []Port {
	key := func(p Port) string {
		iface, host := portEffective(p)
		return fmt.Sprintf("%s:%d:%d", iface, host, p.Container)
	}
	out := append([]Port{}, base...)
	seen := map[string]bool{}
	for _, p := range out {
		seen[key(p)] = true
	}
	for _, p := range over {
		if !seen[key(p)] {
			seen[key(p)] = true
			out = append(out, p)
		}
	}
	return out
}

func mergeVolumes(base, over []Volume) []Volume {
	out := append([]Volume{}, base...)
	var removals []string
	for _, v := range over {
		if name, ok := strings.CutPrefix(v.Name, "!"); ok {
			removals = append(removals, name)
			continue
		}
		replaced := false
		for i := range out {
			if out[i].Name == v.Name {
				out[i] = v
				replaced = true
				break
			}
		}
		if !replaced {
			out = append(out, v)
		}
	}
	for _, rm := range removals {
		out = filterVolumes(out, func(v Volume) bool { return v.Name != rm })
	}
	return out
}

func appendAll(base, over []string) []string {
	if len(base) == 0 && len(over) == 0 {
		return nil
	}
	out := append([]string{}, base...)
	return append(out, over...)
}

func containsString(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

func removeString(s []string, v string) []string {
	out := s[:0:0]
	for _, x := range s {
		if x != v {
			out = append(out, x)
		}
	}
	return out
}

func filterMounts(s []Mount, keep func(Mount) bool) []Mount {
	out := s[:0:0]
	for _, x := range s {
		if keep(x) {
			out = append(out, x)
		}
	}
	return out
}

func filterVolumes(s []Volume, keep func(Volume) bool) []Volume {
	out := s[:0:0]
	for _, x := range s {
		if keep(x) {
			out = append(out, x)
		}
	}
	return out
}

// SortedEnvKeys returns env keys in deterministic order (helper for generation).
func SortedEnvKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
