# Cross-skill same-destination claims must be byte-identical

Decided 2026-07-29. `skills.Resolve` refuses two `[build] files` sources for
one image destination WITHIN a manifest ("silent shadowing of an authoring
mistake, refused where the author can see it") — but that check was
per-manifest only. Two *different* skills claiming the same absolute dest
sailed through resolve and settled by COPY order (provenance rank, ADR
0041), last writer wins, silently: the exact consequence the intra-skill
check exists to prevent, one composition level up.

Principles: P2 (a silent last-writer-wins between two skills the user
enabled is illegible — nothing they can read says which skill's file their
box runs); P1 scope check (this guards against author mistakes surfacing as
inscrutable box breakage, not against the user's own choices — project
`files` overriding a skill path stays a legitimate, untouched channel, with
the security subset already defended by guard re-assertion).

## The rule

At build-context staging, when two enabled skills claim the same image
destination:

- **Byte-identical staged content is allowed**, both COPYs emitted. This is
  the dual-ship pattern working as intended — two skills each carrying the
  same lib so either works alone (live in the field:
  `pjlsergeant/devlog` and `pjlsergeant/codereview` both ship
  `/usr/local/lib/byre-devlog-lib.sh`) — and identical bytes make the COPY
  order irrelevant.
- **Anything else refuses the assemble**, naming both skills and the
  destination. There is no meaning to "these two skills install different
  content at one path" a user could intend; if the dual-shipped copies ever
  diverge, the loser's consumer sources a lib missing the symbol it calls,
  under `set -e`, decided by build order.

The judgment runs over the STAGED trees — the bytes that actually ship,
after symlink refusal and bounds — never a second traversal of the sources,
so the comparison is exact by construction. Divergence includes permission
bits (COPY preserves the staged mode; a lib that lost its exec bit diverges
in behavior with identical bytes). Granularity matches the intra-skill
rule: same destination STRING. Overlapping directory CONTENTS under
different dest strings merge by COPY semantics, as they always have.

## Amended 2026-07-30: staging normalizes modes, so mode divergence = exec bit

The rule fired in the field one day in: `pjlsergeant/devlog` (installed
snapshot, 0644) against `pjlsergeant/codereview` (adopted working tree,
0664 under the author's group-write umask) — identical bytes, one umask
bit, refused as "different content". Snapshots normalize to 0644 at
install; working trees carry whatever the authoring host's umask left; so
dual-ship across those two provenances could never compose on a
umask-002 host, and the staged image varied by host umask besides.

Staging now normalizes regular-file modes git-style (`stageRegularFromFD`):
0644, or 0755 when the source has any exec bit; an fchmod sets the exact
bits so byre's own process umask can't leak in either; setuid/setgid/sticky
drop with the rest (directories were already staged at a constant 0755).
Only the exec bit of a source mode is authored content — the rest is umask
noise, and a 0600 "restriction" was already fiction in a world-readable
image layer.

The comparison above is UNCHANGED — staged modes still count, and the
refusal on exec-bit divergence stands for the reason given. Normalization
means the only mode divergence that can reach it IS the exec bit. The
refusal also now names what differed (first diverging path, modes per
skill, or "content differs") instead of a blanket "different content".

## Where it does not run

Resolve-time surfaces (`validate`, `status`) do not pre-judge this: a
resolve-time comparison would be a second, weaker implementation reading
bytes that are not the ones shipping. The refusal lands at develop, before
any engine command runs. If an earlier warning is ever wanted, it calls the
staged-bytes check, not a parallel one.
