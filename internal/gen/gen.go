// Package gen renders the byre Dockerfile from a resolved configuration.
//
// byre's job is to *generate* the Dockerfile; Docker's job is to build and
// cache it. Generation is deterministic — identical input yields byte-identical
// output (sorted maps, preserved list order, no timestamps) — which is what lets
// Docker's layer cache share work across projects.
package gen

import (
	_ "embed"
	"fmt"
	"sort"
	"strings"
)

// LauncherName is the build-context filename of the launcher script that the
// core block COPYs in and installs as the ENTRYPOINT.
const LauncherName = "byre-launch"

//go:embed launcher.sh
var launcherScript []byte

// LauncherScript returns the constant launcher script bytes. The build-context
// assembler writes these to <context>/byre-launch.
func LauncherScript() []byte { return launcherScript }

// ProfileEnvName is the build-context filename of the /etc/profile.d shim that
// sources env.d for login shells (so `byre shell` sessions get the same
// env.d-provided environment the launcher gives the agent).
const ProfileEnvName = "byre-profile-env.sh"

// profileEnvPath is where the core block installs the profile.d shim.
const profileEnvPath = "/etc/profile.d/byre-env.sh"

//go:embed profile-env.sh
var profileEnvScript []byte

// ProfileEnvScript returns the constant profile.d env shim bytes. The
// build-context assembler writes these to <context>/byre-profile-env.sh.
func ProfileEnvScript() []byte { return profileEnvScript }

// Input is the generation input. It carries plain fields (no config dependency)
// so the generator stays standalone; callers map the resolved config onto it.
type Input struct {
	Base         string
	Env          map[string]string
	Files        map[string]string // src -> dest
	Apt          []string
	NpmGlobal    []string
	Skills       []SkillBlock // per-skill build blocks, in order
	AgentCmd     bool         // emit COPY of the agent launch script
	AgentContext bool         // emit COPY of the concatenated agent context
	// AgentContextTarget, when set alongside AgentContext, emits the baked target
	// path file so the launcher knows where to place the context at runtime.
	AgentContextTarget bool
	// VolumeDirs are named-volume mount points to pre-create in the image owned by
	// the baked UID/GID, so a fresh Docker named volume initializes with that
	// ownership (else a root-owned mount point leaves the unprivileged agent unable
	// to write state).
	VolumeDirs     []string
	DockerfilePre  []string // raw block, before the core block
	DockerfilePost []string // raw block, project tail
	// Guard lists security-critical image files re-COPY'd at the Dockerfile
	// TAIL, after the project block, so no project `files` entry or raw build
	// line can leave its own content at those paths. The build layer fills this
	// from the resolved skills (a network-posture skill's gate + netns script);
	// the launcher is re-asserted unconditionally by Dockerfile itself.
	Guard []GuardFile
}

// GuardFile is one security-critical file the tail re-asserts. Staged is its
// build-context source (as staged), Dest the absolute image path, and Exec
// whether to re-apply chmod +x after the COPY (a staged script arrives 0644,
// so the netns enforcement script needs it; the gate file does not).
type GuardFile struct {
	Staged string
	Dest   string
	Exec   bool
}

// SkillBlock is one skill's build contribution.
type SkillBlock struct {
	Name       string
	Apt        []string
	NpmGlobal  []string
	Files      map[string]string // staged-context-path -> image dest (COPY'd before raw lines)
	Dockerfile []string          // raw lines
}

// Context-baked paths the launcher reads at runtime.
const (
	AgentCmdName           = "agent-cmd"
	AgentContextName       = "agent-context.md"
	AgentContextTargetName = "agent-context-target"
	SelfEditDocName        = "self-edit.md"
)

// MCPConfigName is the build-context filename of the canonical declared MCP
// set; the core section below COPYs it to MCPConfigPath. The path and the
// file's format (config.MCPConfigJSON) are a quasi-public contract: baked
// into EVERY image, empty set included, so an agent command can reference it
// unconditionally and any consumer (a reviewer CLI, a hand-wired tool) can
// point at a stable path. Pinned by the golden test; changes are versioned
// decisions.
const (
	MCPConfigName = "mcp.json"
	MCPConfigPath = "/etc/byre/" + MCPConfigName
)

// ClaudeSkillsDirName is the build-context directory of the canonical declared
// Claude Skill set; the section below COPYs it to ClaudeSkillsPath. Like
// mcp.json, the path is a quasi-public contract baked into EVERY image, empty
// set included, so an agent command can reference it unconditionally (claude:
// --add-dir) and any consumer can point at a stable path. The layout inside is
// claude's native discovery shape — <path>/.claude/skills/<name>/SKILL.md —
// so the delivered skills load BARE (as /name), not plugin-namespaced.
const (
	ClaudeSkillsDirName = "claude-skills"
	ClaudeSkillsPath    = "/etc/byre/" + ClaudeSkillsDirName
)

// DefaultBase is used when no base is configured (and no template supplies one).
const DefaultBase = "debian:bookworm"

// LauncherPath is where the core block installs the launcher / ENTRYPOINT.
// Exported because the security guard (build layer + status/develop warnings)
// reasons about it as a chassis-owned, re-asserted path.
const LauncherPath = "/usr/local/bin/" + LauncherName

// LaunchGatePath is the launch-gate file the launcher waits on (launcher.sh
// hardcodes this default; a network-posture skill bakes the gate here). It's a
// cross-component contract — the shell launcher, the firewall skill.toml, and
// the security guard must agree on it — so it lives here as the Go-side anchor.
const LaunchGatePath = "/etc/byre/launch-gate"

// coreBlock is the chassis's build-time slice — core's constant contribution
// to every generated Dockerfile. It installs gosu (a build-only helper —
// skills install their CLIs as the dev user via `gosu dev` in a RUN),
// creates the in-box 'dev' user OWNED BY THE HOST UID/GID, installs the launcher,
// and prepares /home/dev + /workspace. The host UID/GID arrive as build args
// (defaulting to 1000), so /home/dev is born owned by the runtime user and a
// fresh named volume inherits that ownership from its image mount point — no
// runtime chown, and the container runs unprivileged as that user (USER set at
// the tail of the Dockerfile, after all root build steps).
//
// The user is created by editing /etc/passwd + /etc/group directly rather than
// via useradd: it avoids useradd's "uid outside UID_MIN..UID_MAX" warning when
// the host uid is low (e.g. 501 on macOS). Any pre-existing `dev` entry is
// dropped first so OUR baked id is authoritative — without that, a base image
// that already ships a `dev` user at some other uid would leave `gosu dev` and
// the final `USER dev` running as that uid while /home/dev is owned by the host
// uid, breaking the build==run contract. After this, the name `dev` always
// resolves to the host UID/GID, so `gosu dev` in skill builds and `USER dev` at
// runtime are correct. (A duplicate UID — a base whose uid is already taken by
// another name — is fine: /etc/passwd allows two names per uid, and `dev` is
// looked up by name.)
//
// The ARG default keeps this block byte-stable (the golden test asserts on the
// template text); only the build-arg VALUE varies per host.
const coreBlock = "ARG BYRE_UID=1000\n" +
	"ARG BYRE_GID=1000\n" +
	"RUN apt-get update \\\n" +
	" && apt-get install -y --no-install-recommends gosu \\\n" +
	" && rm -rf /var/lib/apt/lists/*\n" +
	"RUN if getent passwd dev >/dev/null 2>&1; then sed -i '/^dev:/d' /etc/passwd; fi \\\n" +
	" && if getent group dev >/dev/null 2>&1; then sed -i '/^dev:/d' /etc/group; fi \\\n" +
	" && if ! getent group \"$BYRE_GID\" >/dev/null 2>&1; then echo \"dev:x:${BYRE_GID}:\" >> /etc/group; fi \\\n" +
	" && echo \"dev:x:${BYRE_UID}:${BYRE_GID}:byre:/home/dev:/bin/bash\" >> /etc/passwd \\\n" +
	// /inbox is deliver's landing spot: dev-owned so the exec-stream writes as
	// the dev identity, root-PARENTED (/ stays root's) so the agent cannot
	// replace the inbox itself with a symlink — the structural half of the
	// deliver design (ADR 0021). /workspace stays root-owned here; the bind
	// mount covers it at run time.
	" && mkdir -p /home/dev /workspace /inbox && chown \"${BYRE_UID}:${BYRE_GID}\" /home/dev /inbox\n" +
	"ENV PATH=/home/dev/.local/bin:$PATH\n" +
	// The HEALTHCHECK strip lives at the Dockerfile TAIL (see Dockerfile()),
	// not here: healthchecks never execute during build steps, so an early
	// copy defends nothing — and a second instruction only buys a buildkit
	// MultipleInstructionsDisallowed warning on every build.
	"COPY " + LauncherName + " " + LauncherPath + "\n" +
	"RUN chmod +x " + LauncherPath + "\n" +
	// Login shells (e.g. `byre shell`) source /etc/profile.d/*.sh; this shim
	// sources env.d there so a shell session gets the same env.d-provided
	// environment the launcher gives the agent (COMPOSE_PROJECT_NAME, shared
	// tokens). env.d hooks are pure env-setters, so this is safe and quiet.
	"COPY " + ProfileEnvName + " " + profileEnvPath + "\n"

// Dockerfile renders the generated Dockerfile in byre's canonical order:
// FROM, the template block, the constant core block, the hoisted skill apt
// section, skill blocks,
// the agent files, the project block (byre primitives + post raw block), the
// security guard (re-asserting chassis-owned paths), then USER (drop to the
// baked dev user) and the constant ENTRYPOINT. The core block precedes skills so
// the dev user + gosu exist for skill builds and the constant block stays
// cache-shared; the guard, USER, and ENTRYPOINT come last so every preceding RUN
// (core block, skill apt installs, the project block) still runs as root and no
// project input can override the security-critical tail. Empty sections render
// as bare markers, keeping the layout stable.
func Dockerfile(in Input) string {
	base := in.Base
	if base == "" {
		base = DefaultBase
	}

	var b strings.Builder
	b.WriteString("# Generated by byre. Do not edit — change byre.config and re-run.\n")
	fmt.Fprintf(&b, "FROM %s\n", base)

	b.WriteString("\n# --- template block ---\n")
	writeRaw(&b, in.DockerfilePre)

	// The constant core block comes BEFORE skills: it's shared across all
	// projects on a base (skills vary, so placing it after them would rebuild it
	// per skill-set), and it means the dev user + gosu exist when skills build —
	// so a skill can install as the dev user rather than root.
	b.WriteString("\n# --- byre core block (constant) ---\n")
	b.WriteString(coreBlock)

	// Skill apt installs hoist ABOVE the skill blocks (ADR 0042). Within a
	// block apt already ran before the skill's own COPYs and raw lines, so no
	// declarative apt list can depend on any raw line — hoisting preserves
	// that order while moving the only layers with a network dependency on
	// mutable external state (apt-get update) where payload and raw-line
	// churn can't invalidate them. One RUN per skill keeps cache granularity
	// and attribution. npm_global stays in the block: node/npm may be
	// provided by an earlier skill's raw lines.
	b.WriteString("\n# --- skill apt (hoisted; see ADR 0042) ---\n")
	for _, s := range in.Skills {
		if len(s.Apt) == 0 {
			continue
		}
		fmt.Fprintf(&b, "# skill: %s\n", s.Name)
		writeApt(&b, s.Apt)
	}

	b.WriteString("\n# --- skills ---\n")
	for _, s := range in.Skills {
		fmt.Fprintf(&b, "# skill: %s\n", s.Name)
		writeNpm(&b, s.NpmGlobal)
		writeFiles(&b, s.Files) // COPY before raw lines so a RUN can use the file
		writeRaw(&b, s.Dockerfile)
	}

	// Agent files are project/agent-specific, so they go after the constant
	// core block (and after skills), keeping them out of the shared path.
	if in.AgentCmd || in.AgentContext || in.AgentContextTarget {
		b.WriteString("\n# --- agent ---\n")
		if in.AgentCmd {
			fmt.Fprintf(&b, "COPY %s /etc/byre/%s\n", AgentCmdName, AgentCmdName)
			b.WriteString("RUN chmod +x /etc/byre/" + AgentCmdName + "\n")
		}
		if in.AgentContext {
			fmt.Fprintf(&b, "COPY %s /etc/byre/%s\n", AgentContextName, AgentContextName)
		}
		if in.AgentContextTarget {
			fmt.Fprintf(&b, "COPY %s /etc/byre/%s\n", AgentContextTargetName, AgentContextTargetName)
			// The launcher appends this to the agent's memory only under --self-edit.
			fmt.Fprintf(&b, "COPY %s /etc/byre/%s\n", SelfEditDocName, SelfEditDocName)
		}
	}

	// The canonical declared MCP set — always baked (empty set included), so
	// the path exists in every box regardless of agent or adapter. Placed
	// after skills/agent so an mcp-set change never busts their layers.
	b.WriteString("\n# --- mcp (canonical declared set; stable path) ---\n")
	fmt.Fprintf(&b, "COPY %s %s\n", MCPConfigName, MCPConfigPath)

	// The canonical declared Claude Skill set — same posture: always baked
	// (empty tree included) at a stable path, after skills/agent so a skill
	// edit never busts their layers.
	b.WriteString("\n# --- claude skills (canonical declared set; stable path) ---\n")
	fmt.Fprintf(&b, "COPY %s %s\n", ClaudeSkillsDirName, ClaudeSkillsPath)

	// Pre-create named-volume mount points owned by the baked UID: Docker seeds a
	// fresh named volume from the image dir at its mount point (content AND
	// ownership), so this is what makes a fresh state/cache volume writable by the
	// unprivileged agent — no runtime chown needed. (Keeping a state volume's seed
	// CLEAN is each agent skill's job — it cleans its own installer residue from
	// its state dir; byre does not blanket-wipe arbitrary config-supplied targets.)
	if dirs := SortedUnique(in.VolumeDirs); len(dirs) > 0 {
		b.WriteString("\n# --- volume mount points (owned by the baked uid) ---\n")
		quoted := make([]string, len(dirs))
		for i, d := range dirs {
			quoted[i] = shellQuote(d) // these land in a shell RUN; quote to prevent injection
		}
		joined := strings.Join(quoted, " ")
		fmt.Fprintf(&b, "RUN mkdir -p %s && chown \"${BYRE_UID}:${BYRE_GID}\" %s\n", joined, joined)
	}

	b.WriteString("\n# --- project block ---\n")
	b.WriteString("WORKDIR /workspace\n")
	writeEnv(&b, in.Env)
	writeFiles(&b, in.Files)
	writeApt(&b, in.Apt)
	writeNpm(&b, in.NpmGlobal)
	writeRaw(&b, in.DockerfilePost)

	// Capture the image's effective PATH for login shells. Debian's
	// /etc/profile unconditionally resets PATH, dropping base-image ENV
	// entries (golang's /usr/local/go/bin — a go-template box had no `go` in
	// `byre shell`, QA pass-2); the profile.d shim restores what this
	// records. Captured in the tail so every PATH-touching layer above (base
	// image, template, skills, project block) is already in effect.
	b.WriteString("\n# --- image PATH capture (login shells restore from this; see byre-env.sh) ---\n")
	b.WriteString("RUN mkdir -p /etc/byre && printf '%s\\n' \"$PATH\" > /etc/byre/image-path\n")

	// Strip any HEALTHCHECK — inherited from the base image or introduced by a
	// raw block (skill Dockerfile lines, dockerfile_post; a pasted service
	// fragment is enough). The engine runs healthcheck commands in the
	// container's netns independently of our ENTRYPOINT, so a probe could do
	// network I/O before a network-posture skill's launch gate lands
	// (fail-open window); byre boxes are interactive sessions, not
	// health-monitored services, so we never want one regardless. The tail is
	// the ONE place this works (last HEALTHCHECK wins) and the one place it's
	// needed — healthchecks never execute during build steps, so no earlier
	// copy is required (a second instruction would only draw buildkit's
	// MultipleInstructionsDisallowed warning). Same tail posture as
	// USER/ENTRYPOINT below: chassis-owned instructions come last so no raw
	// block can override them.
	// Security guard: re-assert byre's own copy of the security-critical files
	// AFTER the project block (and any dockerfile_post), so a project `files`
	// entry or raw build line targeting these paths cannot leave its content in
	// the final image. Same posture as the USER/ENTRYPOINT/HEALTHCHECK tail
	// below: byre forces its security-critical instructions last wherever it
	// controls the order. Without this the ENTRYPOINT *pointer* is tail-protected
	// but its *content* — the launcher, and the launch gate it enforces — sits in
	// the overridable zone, so a one-line `files` clobber could stub the launcher
	// or empty the gate while status still reads deny-by-default. The launcher is
	// re-asserted unconditionally (it is the ENTRYPOINT's content); a
	// network-posture skill's gate + netns script arrive in in.Guard from the
	// build layer, derived from the resolved skills so the set can't rot.
	b.WriteString("\n# --- byre security guard (re-assert chassis paths after the project block) ---\n")
	b.WriteString("COPY " + LauncherName + " " + LauncherPath + "\n")
	b.WriteString("RUN chmod +x " + LauncherPath + "\n")
	for _, g := range in.Guard {
		b.WriteString(CopyLine(g.Staged, g.Dest) + "\n")
		if g.Exec {
			fmt.Fprintf(&b, "RUN chmod +x %s\n", shellQuote(g.Dest))
		}
	}

	b.WriteString("\nHEALTHCHECK NONE\n")

	// Drop to the baked dev user for the runtime container. This comes after every
	// build step (core block, skills, project block) so those still run as root, but
	// before the ENTRYPOINT so the launcher and the agent run unprivileged — no
	// runtime root, no gosu drop.
	b.WriteString("USER dev\n")
	fmt.Fprintf(&b, "ENTRYPOINT [%q]\n", LauncherPath)
	return b.String()
}

func writeRaw(b *strings.Builder, lines []string) {
	for _, l := range lines {
		b.WriteString(l)
		b.WriteByte('\n')
	}
}

func writeEnv(b *strings.Builder, env map[string]string) {
	keys := make([]string, 0, len(env))
	for k := range env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		fmt.Fprintf(b, "ENV %s=%q\n", k, env[k])
	}
}

func writeFiles(b *strings.Builder, files map[string]string) {
	srcs := make([]string, 0, len(files))
	for s := range files {
		srcs = append(srcs, s)
	}
	sort.Strings(srcs)
	for _, s := range srcs {
		b.WriteString(CopyLine(s, files[s]) + "\n")
	}
}

// CopyLine is the exact COPY line the generator emits for a staged file (the
// quoted form writeFiles produces). Exported so tests in other packages assert
// the line via its owner instead of respelling the quoting convention.
func CopyLine(stagedPath, dest string) string {
	return fmt.Sprintf("COPY %q %q", stagedPath, dest)
}

// shellQuote single-quotes s for safe interpolation into a shell command (a
// Dockerfile RUN), neutralizing spaces and metacharacters. An embedded single
// quote is closed, escaped, and reopened.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// SortedUnique returns the distinct, sorted, non-empty entries of s. Exported
// because internal/build reuses it to derive the volume-dirs set: build and gen
// must agree on the mkdir/chown set this package emits into the Dockerfile, so
// both sides derive it from this one function.
func SortedUnique(s []string) []string {
	seen := make(map[string]struct{}, len(s))
	out := make([]string, 0, len(s))
	for _, v := range s {
		if v == "" {
			continue
		}
		if _, ok := seen[v]; ok {
			continue
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}
	sort.Strings(out)
	return out
}

// writeApt / writeNpm shell-quote every package name, matching the posture of
// writeVolumeDirs: upstream validation (config.ValidateContent) already
// allowlists the charset, but this layer interpolates into shell and should
// not depend on a check two packages away.
func writeApt(b *strings.Builder, pkgs []string) {
	if len(pkgs) == 0 {
		return
	}
	fmt.Fprintf(b, "RUN apt-get update \\\n"+
		" && apt-get install -y --no-install-recommends %s \\\n"+
		" && rm -rf /var/lib/apt/lists/*\n", joinQuoted(pkgs))
}

func writeNpm(b *strings.Builder, pkgs []string) {
	if len(pkgs) == 0 {
		return
	}
	fmt.Fprintf(b, "RUN npm install -g %s\n", joinQuoted(pkgs))
}

// joinQuoted shell-quotes each element and joins with spaces.
func joinQuoted(items []string) string {
	quoted := make([]string, len(items))
	for i, it := range items {
		quoted[i] = shellQuote(it)
	}
	return strings.Join(quoted, " ")
}
