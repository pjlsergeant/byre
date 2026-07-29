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

## Where it does not run

Resolve-time surfaces (`validate`, `status`) do not pre-judge this: a
resolve-time comparison would be a second, weaker implementation reading
bytes that are not the ones shipping. The refusal lands at develop, before
any engine command runs. If an earlier warning is ever wanted, it calls the
staged-bytes check, not a parallel one.
