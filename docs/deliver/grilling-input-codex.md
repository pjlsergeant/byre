# `byre deliver` — grilling input from codex (2026-07-10)

> Working doc, companion to `handoff.md` / `thoughts-fable.md`. Produced by
> the codex reviewer via `byre-codereview`, prompted for a pre-implementation
> DESIGN review: what a grilling session must cover beyond the two docs.
> Verbatim below (also logged in `.devloop/reviews.md`).

## Ranked grilling agenda

1. **Define which engine a machine-scoped command searches.**  
   [docs/deliver/handoff.md:35](/workspace/docs/deliver/handoff.md:35), [docs/deliver/handoff.md:40](/workspace/docs/deliver/handoff.md:40) — `engine=auto` currently prefers Docker, then Podman, but `deliver` cannot rely on cwd project config and machines can have live byre boxes in both engines. Settle whether discovery searches both, uses a global/default preference, or requires an override; otherwise “exactly one session on the machine” can be false and the “engine-agnostic” remote claim can lie. Verified by inspecting `internal/commands/engine.go`, `internal/runner/runner.go`, and `docs/ARCHITECTURE.md`.

2. **Make collision avoidance atomic under concurrent deliveries.**  
   [docs/deliver/handoff.md:58](/workspace/docs/deliver/handoff.md:58), [docs/deliver/handoff.md:61](/workspace/docs/deliver/handoff.md:61) — “uniquify, never overwrite” plus temp-and-rename does not specify an atomic reservation; two invocations can both choose `shot-2.png`, and ordinary POSIX `rename` overwrites. The session must choose a Docker/Podman-portable no-clobber protocol and define cleanup after interruption, full disk, failed rename, or container exit; directory delivery cannot be atomic in the same sense. Verified by design inspection.

3. **Constrain the remote `--consume` protocol before treating it as safe.**  
   [docs/deliver/handoff.md:94](/workspace/docs/deliver/handoff.md:94), [docs/deliver/handoff.md:98](/workspace/docs/deliver/handoff.md:98), [docs/deliver/handoff.md:101](/workspace/docs/deliver/handoff.md:101) — a remotely callable flag that consumes an arbitrary positional path and a later `rm -rf` cleanup is a deletion primitive unless paths are restricted to a byre-created staging directory and passed without remote-shell interpolation. Specify staging authentication/ownership, quoting, `--` handling, symlink behavior, cleanup bounds, partial `scp`, disconnect semantics, URI parsing (IPv6/user/aliases), and protocol/version errors—not merely CRLF stripping. Verified by inspection; no SSH probe was run.

4. **Define the source filesystem policy, especially symlinks and special files.**  
   [docs/deliver/handoff.md:22](/workspace/docs/deliver/handoff.md:22), [docs/deliver/handoff.md:63](/workspace/docs/deliver/handoff.md:63) — recursive delivery is ambiguous for symlinks outside the selected directory, symlink cycles, FIFOs that block forever, sockets/devices, hard links, unreadable files, and files changing mid-stream. Decide whether v1 accepts only regular files/directories, whether symlinks are copied or followed, and whether a failed multi-file delivery is partial or rolled back. Verified by design inspection.

5. **Specify filename, path, and output encoding as a protocol contract.**  
   [docs/deliver/handoff.md:58](/workspace/docs/deliver/handoff.md:58), [docs/deliver/handoff.md:62](/workspace/docs/deliver/handoff.md:62), [docs/deliver/handoff.md:67](/workspace/docs/deliver/handoff.md:67) — “shell-quoted, space- or newline-separated” is not machine-readable for filenames containing newlines, non-UTF-8 bytes, or platform-normalized Unicode, and “final line” is not a robust remote record format. Settle supported host OSes, Finder file-URL percent decoding, basename sanitization, byte/Unicode policy, an unambiguous porcelain framing, and whether human clipboard text and stdout intentionally use different formats. Verified by inspection.

6. **Reconcile target selection with what the labels can actually prove.**  
   [docs/deliver/handoff.md:35](/workspace/docs/deliver/handoff.md:35), [docs/deliver/handoff.md:40](/workspace/docs/deliver/handoff.md:40), [docs/deliver/thoughts-fable.md:44](/workspace/docs/deliver/thoughts-fable.md:44) — labels contain derived IDs, not canonical host workdir paths, so “cwd is inside a running workdir” needs a precise derivation rule for subdirectories, symlinks, moved worktrees, deleted workdirs, and remote staging cwd. Also decide how daemon-wide results are scoped on shared/rootless daemons and how stale/malformed or lookalike labeled containers are handled. Verified against ADR 0004, `internal/commands/naming.go`, and `internal/project`.

7. **Correct the UID/Podman ownership claim.**  
   [docs/deliver/thoughts-fable.md:20](/workspace/docs/deliver/thoughts-fable.md:20), [docs/deliver/handoff.md:129](/workspace/docs/deliver/handoff.md:129) — the review calls `os.Getuid()` “trivial,” but ADR 0008 explicitly says rootless Podman breaks the baked-UID model, and existing exec code uses numeric UID:GID rather than the literal `dev` shown in the brief. Decide whether `deliver` inherits the existing unsupported warning, inspects the running container’s configured user, or uses a stable in-container name; document Docker versus rootful/rootless Podman behavior honestly. Verified against ADR 0008 and the runner exec implementation.

8. **Design test seams beyond the injected container runner.**  
   [docs/deliver/handoff.md:135](/workspace/docs/deliver/handoff.md:135), [docs/deliver/thoughts-fable.md:14](/workspace/docs/deliver/thoughts-fable.md:14) — runner fakes cover engine argv and streams, but not clipboard representations, `osascript`/Wayland/X11 tools, TTY/GUI detection, OSC 52, time/collision generation, filesystem walks, temp staging, SSH/SCP dialogue, notifications, or generated app artifacts. The grilling should establish small injected adapters and byte-level protocol tests; otherwise the hardest cross-platform behavior will sit behind process-global environment and executable lookups. Verified against `internal/runner` and existing command fake structure.

9. **Resolve the clipboard privacy contradiction.**  
   [docs/deliver/handoff.md:31](/workspace/docs/deliver/handoff.md:31), [docs/deliver/handoff.md:110](/workspace/docs/deliver/handoff.md:110) — the acknowledged risk is accidentally delivering a copied password, yet the proposed mitigation may print its first line, creating another durable disclosure in terminal scrollback or notifications. Decide what metadata is safe—kind, byte count, basename, perhaps a redacted preview—and what empty, oversized, multi-representation, or clipboard-read-failure cases do before delivery begins. Verified by design inspection.

10. **Assign one owner for every user-visible truth before writing the docs.**  
    [docs/deliver/thoughts-fable.md:89](/workspace/docs/deliver/thoughts-fable.md:89), [docs/deliver/thoughts-fable.md:96](/workspace/docs/deliver/thoughts-fable.md:96), [docs/deliver/handoff.md:115](/workspace/docs/deliver/handoff.md:115) — the docs list the surfaces but do not settle their authority or lifecycle. Decide which source generates or pins top-level/command help, README examples, degradation behavior, ADR mechanics, glossary vocabulary, notification/error strings, install/uninstall instructions, and the agent context line; that context must not imply the inbox exists, persists, or is agent-writable from the host when none of those claims is unconditional. Verified against the generated CLI command table, launcher context mechanics, README, ARCHITECTURE, GLOSSARY, and PRINCIPLES.

11. **Specify generated launcher installation and upgrade behavior.**  
    [docs/deliver/handoff.md:106](/workspace/docs/deliver/handoff.md:106), [docs/deliver/thoughts-fable.md:107](/workspace/docs/deliver/thoughts-fable.md:107) — “materialize” still hides decisions about locating the current binary from Finder’s sparse environment, paths containing spaces, upgrades/moves, idempotent reinstall, uninstall, stale baked remote targets, `.desktop` field-code escaping, executable permissions, and reporting errors when no terminal exists. These determine whether generated artifacts remain readable and truthful rather than becoming detached copies of old behavior. Verified by design inspection.

Probes run:

- `rg --files` and `rg -n` searches for guidance, ADR/doc surfaces, command behavior, context mechanics, and runner seams.
- `sed`/`nl` inspections of `CLAUDE.md`, both deliver docs, `README.md`, `ARCHITECTURE.md`, `PRINCIPLES.md`, `GLOSSARY.md`, ADRs 0002/0004/0008, and relevant Go sources.
- No runtime, build, test, Docker/Podman, clipboard, or SSH probes.