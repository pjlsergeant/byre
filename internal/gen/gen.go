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
// infra layer COPYs in and installs as the ENTRYPOINT.
const LauncherName = "byre-launch"

//go:embed launcher.sh
var launcherScript []byte

// LauncherScript returns the constant launcher script bytes. The build-context
// assembler writes these to <context>/byre-launch.
func LauncherScript() []byte { return launcherScript }

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
	DockerfilePre  []string // raw block, before the infra layer
	DockerfilePost []string // raw block, project tail
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

// defaultBase is used when no base is configured (config arrives in M2).
const defaultBase = "debian:bookworm"

// launcherPath is where the infra layer installs the launcher / ENTRYPOINT.
const launcherPath = "/usr/local/bin/" + LauncherName

// infraLayer is byre's constant infra block: it installs gosu (a build-only
// helper — skills install their CLIs as the dev user via `gosu dev` in a RUN),
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
const infraLayer = "ARG BYRE_UID=1000\n" +
	"ARG BYRE_GID=1000\n" +
	"RUN apt-get update \\\n" +
	" && apt-get install -y --no-install-recommends gosu \\\n" +
	" && rm -rf /var/lib/apt/lists/*\n" +
	"RUN if getent passwd dev >/dev/null 2>&1; then sed -i '/^dev:/d' /etc/passwd; fi \\\n" +
	" && if getent group dev >/dev/null 2>&1; then sed -i '/^dev:/d' /etc/group; fi \\\n" +
	" && if ! getent group \"$BYRE_GID\" >/dev/null 2>&1; then echo \"dev:x:${BYRE_GID}:\" >> /etc/group; fi \\\n" +
	" && echo \"dev:x:${BYRE_UID}:${BYRE_GID}:byre:/home/dev:/bin/bash\" >> /etc/passwd \\\n" +
	" && mkdir -p /home/dev /workspace && chown \"${BYRE_UID}:${BYRE_GID}\" /home/dev\n" +
	"ENV PATH=/home/dev/.local/bin:$PATH\n" +
	"COPY " + LauncherName + " " + launcherPath + "\n" +
	"RUN chmod +x " + launcherPath + "\n"

// Dockerfile renders the generated Dockerfile in byre's canonical order:
// FROM, template/pre-infra raw block, the constant infra layer, skill blocks,
// the agent files, the project block (byre primitives + post raw block), then
// USER (drop to the baked dev user) and the constant ENTRYPOINT. The infra layer
// precedes skills so the dev user + gosu exist for skill builds and the constant
// layer stays cache-shared; USER comes last so every preceding RUN (infra, skill
// apt installs, the project block) still runs as root. Empty sections render as
// bare markers, keeping the layout stable.
func Dockerfile(in Input) string {
	base := in.Base
	if base == "" {
		base = defaultBase
	}

	var b strings.Builder
	b.WriteString("# Generated by byre. Do not edit — change byre.config and re-run.\n")
	fmt.Fprintf(&b, "FROM %s\n", base)

	b.WriteString("\n# --- template block ---\n")
	writeRaw(&b, in.DockerfilePre)

	// The constant infra layer comes BEFORE skills: it's shared across all
	// projects on a base (skills vary, so placing it after them would rebuild it
	// per skill-set), and it means the dev user + gosu exist when skills build —
	// so a skill can install as the dev user rather than root.
	b.WriteString("\n# --- byre infra layer (constant) ---\n")
	b.WriteString(infraLayer)

	b.WriteString("\n# --- skills ---\n")
	for _, s := range in.Skills {
		fmt.Fprintf(&b, "# skill: %s\n", s.Name)
		writeApt(&b, s.Apt)
		writeNpm(&b, s.NpmGlobal)
		writeFiles(&b, s.Files) // COPY before raw lines so a RUN can use the file
		writeRaw(&b, s.Dockerfile)
	}

	// Agent files are project/agent-specific, so they go after the constant
	// infra layer (and after skills), keeping them out of the shared path.
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

	// Drop to the baked dev user for the runtime container. This comes after every
	// build step (infra, skills, project block) so those still run as root, but
	// before the ENTRYPOINT so the launcher and the agent run unprivileged — no
	// runtime root, no gosu drop.
	b.WriteString("\nUSER dev\n")
	fmt.Fprintf(&b, "ENTRYPOINT [%q]\n", launcherPath)
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
		fmt.Fprintf(b, "COPY %q %q\n", s, files[s])
	}
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

func writeApt(b *strings.Builder, pkgs []string) {
	if len(pkgs) == 0 {
		return
	}
	fmt.Fprintf(b, "RUN apt-get update \\\n"+
		" && apt-get install -y --no-install-recommends %s \\\n"+
		" && rm -rf /var/lib/apt/lists/*\n", strings.Join(pkgs, " "))
}

func writeNpm(b *strings.Builder, pkgs []string) {
	if len(pkgs) == 0 {
		return
	}
	fmt.Fprintf(b, "RUN npm install -g %s\n", strings.Join(pkgs, " "))
}
