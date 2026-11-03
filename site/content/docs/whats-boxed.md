---
title: What's boxed, what isn't
weight: 80
description: the honest contract, including what byre is not
---

- **Boxed:** your host filesystem, environment, and credentials. The agent
  sees only the project folder plus what you mount, pass, or enable.
  byre reads no host credentials and copies none: agent logins happen
  inside the box, and the optional Claude shared-auth token is one you
  mint yourself with `claude setup-token` and hand over at an explicit
  prompt
  ([ADR 0017](https://github.com/pjlsergeant/byre/blob/main/docs/adr/0017-shared-agent-identity.md)).
- **Not boxed, by design:** the network (open by default -- enable the
  default-deny firewall skill to close it to a derived allowlist) and
  the project itself (mounted read-write -- it's the agent's job to edit
  it).
- **Not a security product:** a container is not a microVM. The box
  shares the kernel of the machine the engine runs on -- yours on Linux,
  the engine's Linux VM on macOS. If the agent must never share a kernel
  with your machine, use a microVM product or a separate machine. byre
  is meant to protect you from over-eager and reckless agents, not from
  state-sponsored malware.
- **Not your nanny:** the box is locked against the *agent*, not against
  you. Every protection is one config edit away from off, and skills can
  widen the box as far as you like -- you can hang yourself with skills,
  and that's intentional. byre's promise is that `byre status` always
  tells you where the rope is.

The full [security model](/docs/security-model/): the threat model, the
contract, and the sharp facts.
