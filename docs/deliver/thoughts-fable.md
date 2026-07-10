# `byre deliver` — in-box review of the handoff (Fable, 2026-07-10)

> Working doc, companion to `handoff.md`. First-pass review done in-repo, with
> the doc's claims checked against the code. Verdict up front: the spec is
> buildable and the big decisions are right; the flags below are things to
> settle or handle with care, plus a section on designing the feature's
> *documentation* (Pete's addition: that needs real design work too).

## Claims verified against the repo

- Labels exist as described: `byre.project` / `byre.workdir`
  (`internal/commands/naming.go:29`); runner re-asserts them last so
  `run_args` can't override (`internal/runner/runargs.go:35`).
- Runner surface: `RunningContainersByLabel`, `Exec` (uid/gid/workdir/env/tty),
  and a `streamIn` exec plumbing hook already exist in
  `internal/runner/runner.go`. Deliver needs a stdin-streaming exec method —
  small addition, the plumbing is there.
- Deps: bubbletea/bubbles/lipgloss and `mattn/go-isatty` are already in
  go.mod; `internal/configui` has the TUI patterns.
- Exec-as-dev uid math is trivial on the same machine: ADR 0008 bakes the
  host UID, so `os.Getuid()` is the right uid to exec as.

## Where the design is right (no re-litigation needed)

- **Exec-stream transport** is byre's grain: zero config surface, ownership
  for free, nothing new for `byre status` to explain, inbox dies with the
  throwaway container. Ephemerality-as-feature is consistent with everything
  else in the box.
- **Stdout is the contract, clipboard is garnish** — correct posture, and the
  non-TTY path-only output makes it composable.
- **Per-axis capability degradation** is the footgun doctrine applied
  properly: degrade claims, never block.
- **scp-then-`ssh -t`** keeping stdin free for the remote picker is the kind
  of detail that is expensive to rediscover. Keep it.

## Flags — settle before or during the build

1. **Filename quoting is the sharpest implementation edge.** The moment
   someone drops `Screenshot 2026-07-10 at 3.14.15 PM.png` (the *common*
   case on macOS), any `sh -c 'cat > ~/inbox/<name>'` with an interpolated
   name breaks or worse. Pass names as argv (`sh -c 'cat > "$1"' _ "$name"`),
   never splice into the script string. Same care for the shell-quoted
   clipboard output.
2. **Picker metadata is thinner than the spec's wishlist.** Discovery is
   label-only and labels carry ids (slug-hash), not paths, branches, or agent
   names. Uptime/image come free from `docker ps`; "worktree/branch, agent,
   path" mostly don't, for existing containers. Options: inspect harder, add
   labels for future containers and degrade for old ones, or trim the picker
   rows to what's honestly available. Lean: trim for v1.
3. **`--porcelain` through `ssh -t` needs `\r` stripping.** A pty emits
   `\r\n`; the "parse the final machine-readable line" step will bite the
   first real test. It's the frozen mini-protocol — pin it with a test.
4. **v1 scope line: after step 5.** Core + clipboard both directions +
   pickers is a complete, honest feature. `ssh://` (step 6) and
   `--install-app` (step 7) are each feature-sized with their own failure
   surfaces (PATH probing, version skew, bundle generation) — separate review
   loops, separately shippable.
5. **Directory delivery: consider tar as the transport** (`tar c | docker
   exec -i ... tar x -C ~/inbox`): one stream, preserves structure/mtimes,
   debian base has tar. Wrinkle: weakens uniquify-on-collision (tar
   overwrites). Acceptable v1 answer: per-file streams everywhere, tar later
   if dirs feel slow. Don't let recursion logic grow clever before that
   trade is examined.
6. **Open questions 1–4: leanings endorsed.** `~/inbox` (zero chassis
   change); chassis constant for the agent context line (it describes a byre
   *mechanism*, like the workspace path — not an opinion in the skills
   sense); preserve directory structure; "inbox" as the glossary term
   (airlock connotes a two-way chamber and oversells the mechanism). One ADR
   covering machine-scoped-verb + exec-stream transport, with picker adapter
   and ssh shape as sections or a second ADR — three separate ADRs is
   gold-plating.
7. **TODO.md overlap.** `deliver` isn't in TODO.md yet, and Someday's
   "Drag-and-drop into the boxed terminal" is partially superseded by it.
   TODO is Pete's to edit; when this is dispatched the item should land there
   and the Someday entry get a cross-reference or trim.
8. **Discovery is daemon-wide.** Label-filtered `docker ps` will also see
   boxes launched by other users on a shared daemon. Fine under the threat
   model (daemon access is root-equivalent anyway), but the picker showing
   someone else's session shouldn't surprise us — one line in the ADR.

## Documentation design (Pete, 2026-07-10: design this deliberately)

The feature's documentation needs the same design pass as the feature. Why
this one specifically: `deliver` is byre's first machine-scoped verb, it has
a degradation matrix (behavior varies by TTY/GUI/clipboard context), and its
killer detail (the clipboard round-trip) is invisible unless told. Docs that
just restate the flag table will fail it. To settle in the grilling:

- **Where each audience reads.** README (user pitch + the one-liner flow),
  `docs/ARCHITECTURE.md` (mechanics: discovery cascade, exec-stream, the
  adapter), GLOSSARY (deliver, inbox — binding vocabulary), ADR(s) (the
  decisions), agent-facing context line (the box's side: "files the user
  delivers appear in `~/inbox`"). Five surfaces; what's the single source
  each one leans on so they don't drift? (Status/marketing lockstep is a
  standing tripwire in TODO.md — this feature adds surfaces of that kind.)
- **The degradation matrix is user-facing truth.** Where does it live so
  support questions ("why didn't my clipboard get set over SSH?") have a
  canonical answer — README table, `byre deliver --help`, or both?
- **Self-describing command output** as documentation: the "clipboard
  unavailable — path printed above" line IS the doc for the degraded case.
  Decide the voice/format of those lines up front (GLOSSARY is binding for
  user-facing strings).
- **The README demo moment.** The drop-a-screenshot → Cmd-V flow is the
  feature's pitch; it likely earns a place in the README's demo/How-do-I
  section. Marketing lockstep applies: whatever output the README shows must
  be re-verified against the code.
- **`--install-app` is doc-adjacent**: the generated app/Quick
  Action/.desktop artifact should be readable-generated (like
  `Dockerfile.generated`) and its install path documented; uninstall story
  too.
- **The `--install-app` artifact needs a glossary name, and two candidates
  are already burned** (Pete, 2026-07-10): "droplet" is overloaded by
  DigitalOcean (avoid), and GLOSSARY pins "materialize" to built-in skill
  copies (grok's F13). The grilling should pick the noun and the verb for
  this artifact before any user-facing string exists.

## Consulted externally

`grilling-input-grok.md` and `grilling-input-codex.md` hold the external
reviewers' passes over this brief (what else a grilling session must cover).
