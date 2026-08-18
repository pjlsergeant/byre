# The tmux seam

Status: DESIGN SKETCH, conversation-stage (Pete + agent, 2026-08-18).
Not dispatched for build. Absorb into an ADR + docs when it ships;
delete this file then.

## The whole feature in one sentence

byre starts the agent inside an invisible tmux server whose socket
lives at a pinned path byre sets; **the pin is the contract**, and
everything anyone builds on it (observation, piping input, in-place
shells, recording) is somebody else's tooling talking to a socket
whose address it knows.

Explicitly NOT in scope: verbs. No `byre shell`, `byre watch`,
`byre send`. Core stays opinion-free; a skill that wants a shell
window runs `tmux -S <socket> new-window`, a recorder runs
`pipe-pane`, byre neither knows nor cares. Verbs were floated in the
originating conversation and cut by Pete: "you're inventing tooling
we might expect skills to fill — we're just designing a seam."

## The small print that makes the pin honest

1. **Invisibility config.** The inner tmux is a programmatic layer,
   not a UI: no prefix key, no status bar, no bindings — the user's
   own (outer) tmux keeps every key because the inner one claims
   none. Passthrough pins are byre's to own because getting them
   wrong breaks every box identically: `allow-passthrough`,
   `set-clipboard` (the deliver paste machinery rides OSC52 +
   bracketed paste and now traverses two tmux layers — inner ours,
   outer the user's; deliver needs its degrade path checked),
   extended keys (Claude's shift+enter), focus/mouse events, `$TERM`.

2. **Lifecycle binding.** The one real design decision. Today
   agent-exit and container-exit are the same event; a tmux server
   holding the tty splits them. v1 pins today's semantics: the
   launcher runs the server foreground, bound to the agent's window —
   session ends, container ends. The already-running-box remedies,
   worktrees, and status keep their model. Detach-survival, if ever,
   is a later loosening, not v1.

3. **The off switch.** Unconditional by default (Pete's call), with a
   config key to disable — which per P0 gets an editor row from day
   one. Off means the seam is ABSENT, not emulated: no server, no
   socket; skills probe for the socket and degrade. The switch
   doubles as the escape hatch when a terminal stack chokes on the
   extra escape-sequence intermediary.

4. **The chassis cost.** tmux becomes byre infrastructure baked into
   every image, with byre owning the version floor. The passthrough
   features want tmux >= 3.4; debian bookworm ships 3.3a, so this may
   be a build/backport, not an apt-get. Unverified beyond that —
   check per-base at build time.

## The host route (part of the contract)

Unix sockets don't cross the container boundary; the only transport
is an exec hop: run the tmux CLIENT inside the container
(`docker exec -it <container> tmux -S <socket> ...`) and carry its
tty out through the exec. byre needs this route for itself anyway —
once the agent lives in a server, the thing on byre's own terminal at
develop time is a tmux client attached to the session. So the route
exists day one; the decision is to PIN it (socket path + session name
stable and documented) so host tooling and skills can rely on it,
rather than keep it a movable internal detail. An unwarranted seam is
just an implementation detail.

Socket presumably lands on a 0052-style managed path (that's the
register for "byre placed this and warrants its location") — exact
path and session name to be chosen at build time.

## The trust line (write into the security model on ship)

The socket is agent-territory: it lives in the container at the
agent's uid, so the agent can capture panes, inject keys, repaint
anything — tier-neutral (it already owns its tty), but it means the
seam is SHARED WITH THE PARTY BEING OBSERVED. Therefore:

- everything byre reads through the seam is display-only and
  P4-escaped;
- nothing consent-shaped ever rides it (an in-box popup asking
  "allow?" is agent-forgeable); confirmations stay host-side, where
  the paste-confirm machinery already lives.

## What this dissolves

The discarded `byre attach` prototype (2026-08-01) bolted a feature
onto `docker attach`; here reattach/second-viewer is just a property
the seam has (`attach -r` for read-only). The parked site-demo
recording rail gets its capture mechanism for free (`pipe-pane` /
`capture-pane`) — still parked, but the substrate arrives with this.

## Open at sketch-stage

- Exact socket path + session name.
- tmux version floor and how it's provisioned per base image.
- Canary posture: unconditional-on-day-one puts a new escape-sequence
  intermediary in every user's tty path; the off switch is the
  mitigation, but decide whether the first release wants a louder
  disclosure.
- Config key name/shape (bool vs enum) — bikeshed at build.
