## byre conversation summary for the team

> **Captured 2026-06-17. May now be out of date.** The actionable conclusions
> from this discussion (positioning, non-goals, Podman/`engine` support, the
> single `!name` removal mechanism, `byre status` output shape, and the
> rootless-Podman caveat) have been folded into `byre-spec-v0.md` (v0.2), which
> is the source of truth. This document is kept as a record of the reasoning.
>
> Note: the competitive claims about Docker Sandboxes below were gathered via an
> LLM/web search (see the `utm_source=chatgpt.com` citations) about a new,
> fast-moving product, and should be re-verified before being relied on.

### Core read

**byre is useful and sensible, but only if it is positioned narrowly.** The strong pitch is not “a safer sandbox than Docker.” It is:

> **byre is the local-first, inspectable, Docker-native project harness for autonomous coding agents.**

The spec’s best idea is that byre is not replacing Docker. It generates Docker you can read, runs it locally, scopes state/cache to the project, and makes agent grants legible. Raw Docker remains first-class rather than an escape hatch. 

### Name

**The name is good. Keep it.**
`byre` is pronounced like **buyer**. The cowshed metaphor works: it is the enclosure you keep the agent in so it does not wander off.

### What needs sharpening

The most important things to tighten before v0:

1. **Threat model**
   Say bluntly what byre protects and what it does not.

   Suggested wording:

   > byre protects the host filesystem, host credentials, and accidental cross-project damage. It does not prevent exfiltration of anything mounted into the container, and it does not restrict network access unless configured to do so.

2. **Skills contract**
   Skills are the most powerful concept and the most likely source of ambiguity. Define skill ordering, dependencies, conflicts, overrides/removals, exact `skill.toml` shape, and exactly what appears in `byre status`.

3. **Config merge semantics**
   The cascade is good, but needs concrete examples. Consider choosing one blessed removal mechanism rather than both `!name` and `skills_remove`, unless both are truly necessary.

4. **State/cache lifecycle**
   This is a real differentiator. Make the distinction very visible:

   * cache is disposable
   * state is precious
   * credentials are state volumes
   * seeding is one-way into a fresh volume
   * `reset`, `rehome`, and reseeding behavior should be predictable

5. **Non-goals**
   Add a short section saying byre is not:

   * an agent
   * a Docker replacement
   * a devcontainer implementation
   * a policy engine
   * a secret manager
   * a cloud sandbox service

### Competitive landscape

**Docker Sandboxes is the closest competitor for the generic sandbox pitch.** Docker’s product runs AI coding agents in isolated microVM sandboxes, with each sandbox getting its own Docker daemon, filesystem, and network. ([Docker Documentation][1]) Docker also frames the security model around multiple isolation layers, including hypervisor, network, Docker Engine, workspace, and credential proxy isolation. ([Docker Documentation][2])

That means byre should **not** compete on “strongest security boundary.” Docker has the stronger story there.

But Docker Sandboxes also appears to require Docker sign-in. Docker’s FAQ says signing in gives each sandbox a verified identity, ties sandboxes to a real person, enables governance/team/audit features, and authenticates against Docker infrastructure. ([Docker Documentation][3])

That gives byre a much clearer wedge:

> **No account. No cloud identity. No control plane. Just local Docker/Podman and inspectable generated files.**

### Differentiation from Docker Sandboxes

Docker Sandboxes:

* stronger isolation
* microVM-based
* Docker-account-backed
* governance/team/audit oriented
* product/platform shaped

byre:

* local-first
* no sign-in
* no control plane
* transparent generated Dockerfile
* project-scoped image/state/cache
* explicit mounts and grants
* raw Docker remains normal
* skills package workflow, runtime grants, state, and agent context

The clean distinction:

> **Docker Sandboxes isolates the agent. byre explains, builds, and manages the agent’s project environment.**

### Podman support

Supporting Podman looks worth designing in now. Podman supports building from Dockerfiles/Containerfiles, and its CLI intentionally overlaps heavily with Docker-style workflows. ([docs.podman.io][4])

The right design is probably:

```toml
engine = "auto" # auto | docker | podman
```

Internally, byre should avoid depending too deeply on the Docker SDK. A small runner abstraction around build/run/volume/image/container operations would make Docker and Podman both viable.

Strategic value:

> **byre + Podman becomes the no-account, no-daemon, rootless local agent harness story.**

That is a sharp contrast with Docker’s account-backed sandbox product.

### Product conclusion

People will care if byre solves **agent runtime slop**:

* unclear mounts
* agent credentials smeared across projects
* one-off Dockerfiles
* no clean reset story
* no obvious “what can this thing touch?”
* no portable way to package workflows like moarcode/shem
* no local-first alternative to account-backed sandbox products

The best v0 should obsess over one moment:

```sh
cd repo
byre develop
```

And then make this obvious:

```text
Agent: claude
Engine: docker/podman
Project mount: /repo -> /workspace rw
Network: open
Host mounts: none
Skills: moarcode
State volumes: .claude
Cache volumes: node_modules
Generated Dockerfile: ~/.byre/projects/...
```

Final recommendation:

> **Keep building it. Do not pitch byre as “secure sandboxing.” Pitch it as the boring, local-first, inspectable runtime wrapper for autonomous coding agents.**

[1]: https://docs.docker.com/ai/sandboxes/?utm_source=chatgpt.com "Docker Sandboxes"
[2]: https://docs.docker.com/ai/sandboxes/security/isolation/?utm_source=chatgpt.com "Isolation layers"
[3]: https://docs.docker.com/ai/sandboxes/faq/?utm_source=chatgpt.com "FAQ | Docker Docs"
[4]: https://docs.podman.io/en/stable/markdown/podman-build.1.html?utm_source=chatgpt.com "podman-build"
