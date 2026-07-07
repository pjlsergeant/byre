# Security model

This is the straight version of byre's security story -- the facts that
are true and important but wrong for the README's register. The README's
"What's boxed, what isn't" is the summary; this is the detail. byre is
young; read `README.md`'s warning block first.

## Threat model

**The threat is the agent, never the user.** byre exists so an
autonomous coding agent can run at full permissions without wearing your
identity or reaching your machine. It is NOT designed to protect you
from yourself: every protection is one config edit from off, and that is
deliberate ("not your nanny" -- the box is locked against the agent, not
against you). byre's standing promise is legibility, not gates:
`byre status` always tells you what the box can reach.

byre is meant to contain over-eager, reckless, or misbehaving agents --
including one acting on hostile instructions it read somewhere -- up to
the strength of a container. It is not built to resist a dedicated
attacker with kernel exploits.

## The contract

- **Boxed:** your host filesystem, environment, and credentials. The
  agent sees the project folder, plus exactly what you mount, pass, or
  enable -- nothing else. byre reads no host credentials and copies
  none; agent logins happen inside the box.
- **Not boxed, by design:** the network (open by default; the
  default-deny firewall skill closes it to a derived allowlist) and the
  project itself (mounted read-write -- editing it is the agent's job).
- **A container is not a microVM.** The box shares your kernel. If your
  requirement is "the agent must never share a kernel with my machine",
  use a microVM product or a separate physical box.

## Specific facts worth knowing

**Docker daemon access is root-equivalent.** Anyone who can talk to the
Docker daemon (the `docker` group) can mount any named volume -- byre's
included, identity volumes included -- or the host filesystem itself.
This is Docker's design, not a byre gap, and byre cannot change it. On
shared machines, treat daemon access as root. byre's uid-qualified
naming (`byre-<id>-u<uid>-...` images, `byre-machine-u<uid>-...`
volumes) prevents users *accidentally* sharing state; it cannot stop a
daemon user doing it deliberately.

**An open network is an exfiltration channel.** With the default open
posture, an agent can send anywhere -- including the project it is
working on, or any credential you passed in. The firewall skill
(deny-by-default, per-skill derived allowlist, applied from outside the
box, fail-closed launch gate) is the mitigation; enable it if this is in
your threat model.

**`--self-edit` is transitive trust of the agent with your host.** A
self-edit agent authors the next develop's config -- mounts, run args,
build context -- through the front door. There is no meaningful
containment beyond that point, and byre does not pretend otherwise: the
launch banner says exactly this, and the session ends with a diff of
what changed in the store.

**Shared (machine-scoped) volumes cross project boundaries.** The
shared-auth skills (ADR 0017) put one agent credential where every one
of your projects' boxes can use it -- that is their purpose. A
misbehaving agent in ANY box can read (and so exfiltrate) that
credential, exactly as it can its own per-project login today; the
tokens involved are inference-scoped where the vendor allows it
(Claude's setup-token). `byre status` names shared volumes; `reset` /
`forget` never delete them silently.

**Agents hold usable credentials by construction.** Whatever auth story
you choose, the agent can read its own credential -- it needs it to
work. byre's job is that the credential's scope is what you chose, its
location is legible, and nothing you did not choose rides along.

## Reporting

byre is a young single-maintainer project. Report security issues via
GitHub security advisories on `pjlsergeant/byre` (preferred) or a plain
issue if the report is not sensitive.
