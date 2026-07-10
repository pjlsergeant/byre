# `byre deliver` — grilling input from grok (2026-07-10)

> Working doc, companion to `handoff.md` / `thoughts-fable.md` /
> `grilling-input-codex.md`. Produced by the grok reviewer via
> `byre-codereview --reviewer grok`, prompted for a pre-implementation
> DESIGN review and told to read codex's agenda and find only what it
> missed. Verbatim below (also logged in `.devloop/reviews.md`).

# Design review: `docs/deliver/` (pre-implementation)

**Scope:** committed design workspace only (`59ccd1c`, `ec49524`). Working tree clean for these files; untracked `README-next.md` ignored.  
**Constraint honored:** no tree mutations, no builds/tests.  
**Codex agenda:** 11 items in `grilling-input-codex.md` — **not repeated** below; this pass is what that list underweights or misses.

---

## Ranked grilling agenda (net-new)

### 1. **Dock / non-TTY + `ssh://` cannot host a remote interactive picker**
`handoff.md:90–94,106` asserts remote interactive picker works “because `-t` keeps a real TTY,” and droplets may bake a default `ssh://` target. Dock / `Terminal=false` `.desktop` launches have **no local TTY** for `ssh -t` to attach to, so multi-session remote resolution has nowhere to draw Bubble Tea. Settle non-interactive remote resolution: require single remote session, baked target id, local pre-pick via a remote list protocol, or hard-fail with a clear error — before treating baked-`ssh://` droplets as shippable.

### 2. **Inbox path integrity against an adversarial agent (symlink / rebind)**
Threat model is the agent (`PRINCIPLES.md` #1, `SECURITY.md`), and deliver is human-initiated — but the agent controls the box filesystem **before** the human drops. It can `ln -sf /workspace ~/inbox`, replace `~/inbox` with a FIFO, or point a collision name at a sensitive path; `mkdir -p` + write then follows into `/workspace` despite “never deliver into `/workspace` by default” (`handoff.md:52–54,47–48`). Grill a destination policy: resolve final path under a fixed absolute root (`/home/dev/inbox`), refuse non-directories / symlink escapes, and decide whether that check is best-effort or fail-closed.

### 3. **Local I/O protocol: stdout vs stderr, and exit codes**
Stdout-is-the-contract (`handoff.md:67`) is incomplete without channel rules. Scripts need: only path record(s) on stdout; diagnostics (“clipboard unavailable…”, picker chrome, skip notices) on stderr; stable exit codes for zero sessions / ambiguity / source error / partial multi-file / remote protocol failure. Without this, porcelain and “composable” local mode will drift from help/README examples (doc-lockstep problem codex #10 names, but not this contract).

### 4. **What string is pasted: `~/inbox/...` vs absolute `/home/dev/inbox/...`**
Round-trip UX is “Cmd-V into the agent” (`handoff.md:65–67`). Many agents and tools do not expand `~`; absolute `/home/dev/inbox/...` is what shell/`DevHome` already treat as canonical (`skills.DevHome`). One decision for stdout, clipboard, notifications, and the agent context line — not three spellings.

### 5. **“Exactly one session on the machine” is a silent cross-principal auto-pick**
Fable notes daemon-wide visibility; codex #6 scopes discovery. The sharper footgun is cascade step 2 (`handoff.md:36–38`): **zero cwd match + one running label → deliver with no prompt**, which can be another user’s sole box on a shared daemon (SECURITY: daemon access is root-equivalent, but accidental still matters under the footgun doctrine). Decide: only auto-pick sessions owned by this host UID (image tag / `BYRE_UID` / name pattern), only when cwd matched, or always pick when >0 foreign sessions exist.

### 6. **Container lifecycle states that are “running” but not deliverable**
Discovery is `docker ps -q --filter label=…` (`runner.go:125–127`) — engine “running,” including **paused**, and not distinguishing **restarting**, dying, or launch-gate-wait (agent not up yet). Spec never defines mid-stream container exit, `exec` failure after partial write, or whether launch-gate-only boxes are valid targets. Define accept/reject/retry and cleanup when the session vanishes under the stream.

### 7. **Deliver must copy `byre shell`’s identity model, not the brief’s `-u dev` sketch**
Codex #7 flags ADR 0008 / Podman; the concrete precedent is stronger: `shell.go` dual-probes docker+podman, reads **`BYRE_UID`/`BYRE_GID` from the container**, fail-closed, and sets **`HOME=/home/dev`**. The brief’s `docker exec -u dev` and fable’s `os.Getuid()` are both wrong relative to shipped code. Grill “shell is the template for target attach” so deliver does not invent a third identity path (overlaps codex #1/#7 but the *shell template* decision is still unsettled in the brief).

### 8. **Chassis agent-context line vs PRINCIPLES #2, and rebuild skew**
Open Q#2 leans chassis (`handoff.md:115`; fable endorses). That is not free under **“core ships no opinions”**: a hardcoded deliver path is a product feature advertised inside every box. Also: chassis text only appears after rebuild; old live sessions get silent absence — degrade-claims applies to the *agent’s* belief, not just host docs (codex #10 covers doc ownership/wording, not rebuild skew or principle tension).

### 9. **macOS / Linux GUI launch: TCC and clipboard from a non-terminal context**
Capability matrix assumes `pbcopy` / `osascript` / notifications work the same from terminal and Dock (`handoff.md:71–86,106`). Finder-launched `.app` / `.desktop` often hit **different TCC / PATH / pasteboard** behavior than a terminal-born process (Automation for osascript dialogs, notification permission, clipboard access). If the Dock path is the killer demo, grill a minimal permission/fallback matrix for that process identity — not only “is there a GUI session.”

### 10. **Path-mode clipboard overwrite is a separate footgun from clipboard-import privacy**
Codex #9 is “don’t print the password you just imported.” Path mode still **replaces** the host clipboard with inbox paths after a Finder drop (`handoff.md:65–67`) — intentional, but destructive to unrelated clipboard contents with no confirm and no opt-out. Decide always-write vs `--clip`/`--no-clip`, and whether Dock notifications should say “clipboard replaced.”

### 11. **Clipboard image type honesty and stdin namelessness**
Clipboard images always become `clipboard-<ts>.png` (`handoff.md:31`); real pasteboards carry JPEG/TIFF/HEIC/PDF. Stdin captures are extensionless (`stdin-<ts>`), undermining “agent infers type from extension” (`handoff.md:58`). Grill convert-vs-preserve-vs-sniff for v1, and whether stdin may take `--name` / content-type without bloating scope.

### 12. **Remote version / capability negotiation beyond porcelain framing**
Codex #3/#5 cover consume safety and record format. Still missing: remote has no `deliver`, old binary without `--porcelain`/`--consume`, or local/remote skew that prints human help on stdout. Define a probe (`byre deliver --help` / version line) and fail-closed errors before scp of large payloads.

### 13. **Glossary “Materialize” collision and command vocabulary lockstep**
GLOSSARY pins **Materialize** to built-in skill copies; handoff reuses “materialize” for `--install-app` (`handoff.md:26,106`). Small but binding: pick “generate/install droplet” language so skill materialize and app install never share a verb in user-facing strings.

### 14. **Multi-file outcome is one transaction across disk, stdout, and clipboard**
Codex #4 covers partial source failure; also settle the **user-visible triple**: which paths exist in the box, which print, which hit the clipboard, and whether a mid-list failure rolls back prior files or leaves a documented partial. Affects notifications and README demos.

---

## Supporting findings (design holes, not nits)

| ID | Where | Issue | Why it matters | Verified? |
|----|--------|--------|----------------|-----------|
| F1 | `handoff.md:90–94,106` | Remote picker assumes `ssh -t` TTY; Dock has none | Baked-`ssh://` droplet + multi-session remote is underspecified / broken | Design + CLI model inspection |
| F2 | `handoff.md:47–54` + threat model | No destination integrity against agent-controlled FS | Agent can redirect “safe” inbox into `/workspace` or block forever | Design vs PRINCIPLES/SECURITY |
| F3 | `handoff.md:67–86` | No stdout/stderr/exit-code contract | Composability and doc examples will lie | Design inspection |
| F4 | `handoff.md:65–67` | `~/…` paste targets may not resolve in-agent | Killer Cmd-V path can fail in practice | Design; `DevHome` is `/home/dev` in code |
| F5 | `handoff.md:36–38` | Sole-session auto-pick is cross-principal | Accidental deliver into someone else’s only box | Design + SECURITY daemon model |
| F6 | `handoff.md:35–40`, `runner.go:125–127` | “Running” ≠ deliverable | Paused/restarting/exit mid-stream undefined | Code: `ps -q` label filter only |
| F7 | `handoff.md:47`, `thoughts-fable.md:20–21`, `shell.go:15–86` | Brief/fable identity model contradicts `shell` | Third attach path will fork bugs | Read `shell.go` + ADR 0008 |
| F8 | `handoff.md:50,115` vs PRINCIPLES #2/#4 | “Nothing for status” + chassis context | Opinion-in-core + rebuild skew for context line | Doc cross-read |
| F9 | `handoff.md:71–86,106` | GUI process ≠ terminal capabilities | Dock demo may fail clipboard/dialog/notify | Design (no macOS probe) |
| F10 | `handoff.md:65–67` vs `:110` | Path-mode clipboard clobber unaddressed | Different footgun than import privacy | Design inspection |
| F11 | `handoff.md:31,58–60` | Type/extension honesty incomplete | Agent file-type inference breaks | Design inspection |
| F12 | `handoff.md:101` | No remote capability probe | Large scp then opaque failure | Design inspection |
| F13 | `handoff.md:26,106` vs GLOSSARY Materialize | Vocabulary collision | Binding glossary drift | GLOSSARY + handoff |
| F14 | `handoff.md:62` | Multi-file “success” spans three surfaces | Partial success UX/docs | Design inspection |

**Intentionally not re-opened (Pete-settled or codex-owned):** exec-stream transport, no host inbox mount, stdout-over-clipboard priority, scp-then-`ssh -t` payload path, no local wait-for-paste, machine-scoped discovery intent, codex items 1–11 as written.

**Unsettled “decided” residue (call out in grill, not re-litigate Pete’s forks):** directory recurse “leaning” still open; multi-path clipboard “space- **or** newline”; `-u dev` sketch vs real uid; “engine-agnostic remote” without machine-scoped engine policy (codex #1).

---

## Doc-surface residue codex #10 didn’t pin

Beyond “one owner each,” grill these **truths** so surfaces cannot disagree:

- Degradation matrix: canonical home is `--help` vs README vs both; self-describing skip lines must use glossary verbs (`deliver`, `inbox`, not airlock/drop).
- Agent context must not claim inbox exclusivity, persistence, or host writability (agent can write there too).
- README demo bytes (notification text, path form, clipboard claim) are lockstep-sensitive like status/marketing TODOs.
- Hidden protocol flags (`--porcelain`, `--consume`): help text, “internal,” and local misuse story.

---

## Probes run

- `git status`, `git diff`, `git diff --cached`, `git log --oneline -8`, `git show` on `59ccd1c` / `ec49524`
- Read: `docs/deliver/{handoff,thoughts-fable,grilling-input-codex}.md`, `PRINCIPLES.md`, `GLOSSARY.md` (partial), `SECURITY.md` (partial), ADRs 0002/0004/0008, `ARCHITECTURE.md` (skills/commands slices)
- `rg` / targeted reads: `internal/runner/runner.go` (`RunningContainersByLabel`, `Exec`, `streamIn`), `internal/commands/{shell,engine,naming,develop}.go`, `internal/project/project.go`, `cmd/byre/main.go` (cwd → projectDir)
- **No** builds, tests, Docker/Podman, SSH, or clipboard probes
