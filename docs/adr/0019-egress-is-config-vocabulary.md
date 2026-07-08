# Egress is config vocabulary; the firewall stays a skill

Decided 2026-07-08 (with Pete driving). The user's path to extra firewall
allowances moves from the `FIREWALL_ALLOW` env var to a first-class
`egress` config key, and ejection-with-firewall becomes a documented
boundary instead of a silent trap. Enforcement does not move: the
firewall remains a skill, applied from outside the box (ADR 0010).

## The problem

Skill-declared egress composes correctly: each skill's `[runtime]
egress` list unions into the allowlist, attributed per skill in status.
The USER's path did not. `FIREWALL_ALLOW` is a config env literal, and
env merges per-key with override semantics -- so a project setting it
silently REPLACES the global default's value rather than adding to it.
It is list-shaped data with union intent riding a scalar vehicle with
override semantics. It is also invisible (nothing declares it; it exists
as a skill.toml comment and a `${FIREWALL_ALLOW:-}` in a shell script),
and it is a grant riding a non-grant vehicle: the glossary counts egress
entries under a restrictive posture as grants, while a config env
literal "is config, not a grant". Status already special-cases it
(`config: FIREWALL_ALLOW` attribution) -- core knowing an env var is
secretly something else is the tell that it wanted to be vocabulary.

## Decision

**The `egress` config key.** `egress = ["host[:port]", ...]` -- a plain
string list, port defaulting to 443, the same grammar skills already
use. The cascade merges it like every other string list: union across
layers, `!host[:port]` removes an inherited entry (entries are strings,
so the `!name` idiom from ADR 0018 applies unchanged). This is the
user's path; skills' own `[runtime] egress` is untouched, and the
resolved allowlist is the union of both, per-source attributed.

**Legible everywhere, inert without teeth.** An Egress row joins the
config UI's GRANTS section and the entries join `byre status`'s egress
table attributed per layer. With no posture skill enabled the entries
are shown annotated as declared-but-unenforced rather than hidden --
config must not carry invisible teeth that a later skill toggle arms.

**`FIREWALL_ALLOW` retires cleanly.** Pre-0.2, no compat shim: the
firewall script stops reading it, status drops the special case, CHANGES
tells users to move the value into `egress`.

**Ejection: everything but the firewall, and it says so.** `byre
dockerfile` + `byre dockerrun` are a complete exit -- image and exact
run command -- except the firewall: its rules are applied from outside
the box by byre's netns helper, so no Dockerfile or run command can
carry them, and the baked-in launch gate makes an ejected firewalled
image fail closed after 30s with a message written for byre's own
failure context. But the enforcement script ships INSIDE the image, so
the walls can travel after all: **`byre ejectfirewall`** (Pete's call,
same session) prints byre's own sidecar invocation as a standalone
script — start the box, run the script against it, the gate opens.
The boundary is documented (`docs/EJECTING.md`) and each surface
explains itself when the firewall is enabled: a comment block in the
`dockerfile` output, a stderr note beside the `dockerrun` command (kept
off stdout so the printed command stays copy-pasteable), and an
eject-aware hint in the launch gate's failure message.

## Rejected

- **A fully first-class firewall** (a `network` scalar, core owning the
  rules script): re-litigates ADR 0010's skill packaging for nothing the
  vocabulary change doesn't deliver. The opinion (deny-by-default) stays
  in the skill; core gains only vocabulary, the same way `ports` is core
  vocabulary that the engine enforces.
- **A FIREWALL_ALLOW compat shim**: a young project keeping a magic env
  var alive next to its replacement doubles the surface and keeps the
  broken override semantics reachable.
- **Making the walls travel INSIDE the box** (rules applied in-container
  at start): needs CAP_NET_ADMIN and an in-box root phase, unwinding
  ADR 0010's "nothing inside the box is privileged". `ejectfirewall`
  keeps the outside-the-box shape: the privileged step stays a separate
  sidecar, just user-run instead of byre-run.

## Boundaries and follow-ons

- Skill guidance strings (a skill declaring env vars it CONSUMES, with a
  one-line hint the env screen can surface) are a separate, general
  feature -- no longer the firewall fix, still worth having (e.g.
  `GEMINI_API_KEY`). Tracked in TODO.
- GLOSSARY's Egress entry mentions FIREWALL_ALLOW; it reconciles when
  the implementation lands, not before (vocabulary describes shipped
  behavior).
