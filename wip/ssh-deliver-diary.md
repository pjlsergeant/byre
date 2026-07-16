# Diary — ssh-deliver worktree

## 2026-07-16 — ssh:// remote delivery (ADR 0037)

Pete redesigned the ssh tranche in conversation before dispatch: the
frozen ADR 0021 mini-protocol (scp staging / --porcelain / --consume /
remote picker over `ssh -t`) is DEAD, superseded by **list-then-deliver**
(ADR 0037, committed first): headless `--boxes` enumeration (tab-separated
line grammar on stdout, notes on stderr, exit 4 = partial pool = never
auto-pick), local pick, one plain-ssh exec streaming ONE tar into
`--tar -`. Accepted costs on record in the ADR: two auth prompts
interactively (one with --box), progress is ours (sendMeter), snapshot
staleness, sparse sshd PATH (--remote-byre).

Shipped, in commit order:
- ADR 0037 + pointer edit in 0021.
- Remote side: proto.go (ProtoVersion=1, ExitPartialPool=4, Boxes emit +
  ParseBoxes), tar.go (RunTar/tarUnpack — entries feed the EXISTING
  per-file transport; .. refuses, absolute confines, control chars rename
  loudly). Wiring: Deliver routes --boxes/--tar; main.go flags + strict
  exclusivity (usage errors pinned in main_test).
- Local side: remote.go (ParseSSHTarget, SSHExec seam + SSHExitError,
  RunRemote, pickRemoteBox, planPack/writeTo — plan stats+spools first so
  the meter has a truthful total; meter+meterGuard share a mutex, remote
  stderr clears the progress line). commands/sshexec.go = real ssh
  (single-quote join; "--" before dest; prompts ride /dev/tty so stdin
  stays free for the tar). exit map: 127→--remote-byre hint, 2→too old,
  255→ssh, 4→no auto-pick.
- Docs: DELIVER.md remote section + matrix row, ARCHITECTURE para,
  GLOSSARY sentence, --help. TODO entry NOT yet removed (waits for done).
- Gated: TestIntegrationDeliverRemoteLoop — ssh binary is the only fake;
  pack→Deliver dispatch→real transport in a live box; uniquify second run.

DONE this session: codereview loop (one P1 — IPv6 rebracketing in
SSHTarget.String, fixed eadd85e; --continue confirmed clean). Gated
deliver tests + FULL integration suite green on the VM. TODO entry
removed. Consciously deferred: real two-machine ssh pass is Pete's
(needs his hardware); no in-VM loopback sshd test (proportionality —
the ssh hop is the ssh binary).

Box tooling gap (told Pete): byre-inttest is NOT on PATH in this
worktree box; ran the repo copy directly with BYRE_INTTEST_USER=
pjlsergeant and BYRE_INTTEST_KEY=/home/dev/scratch/inttest/byre-inttest
(that scratch file IS the private key, misleading name; the canonical
/home/dev/.byre-identity/inttest/ path is absent here — machine volume
not mounted or box predates the skill).

Gotchas learned:
- main_test recorder strings gained 4 fields — every deliver row updated.
- `-` mixing rule must exempt an ssh:// FIRST arg (srcs = args[1:]).
- go test ./... cold in this box takes >2 min (gen golden test); targeted
  packages first, full sweep in background.
