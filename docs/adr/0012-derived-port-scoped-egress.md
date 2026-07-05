# Egress allowlist: derived from skills, port-scoped

The firewall allowlist is **derived, not static**: each skill declares
the egress it needs via a typed `skill.toml` field (`[runtime] egress =
["host[:port]", ...]`, port defaulting to 443). Agent skills carry their
own API/auth endpoints; the firewall skill carries only the generic base
(git hosting, package registries, apt mirrors with explicit `:80`). byre
unions every enabled skill's egress plus the user's `FIREWALL_ALLOW` env
(same grammar) and passes the result to the netns helper.

Why: a static list hardcoded in the firewall skill was backwards
coupling -- the firewall had to know every agent, enabling only claude
still opened openai+google endpoints, and a new agent skill required a
firewall edit. Derivation inverts it: enabling only claude opens only
claude's endpoints, and a new agent brings its own egress.

Rules are **port-scoped** (`-d <ip> -p tcp --dport <port> ACCEPT`), not
all-ports-to-the-IP: agent APIs and registries sit on shared CDN/cloud
addresses fronting many tenants, so "anything to this address" is far
looser than what was meant. An **empty allowlist is legal** (a
maximally-locked box -- loopback + scoped DNS only); the helper dies only
when a non-empty request resolves to nothing.

Consequences: per-project additions go through `FIREWALL_ALLOW` -- the
generic env mechanism -- rather than a `firewall_allow` core config key,
which would put firewall opinion in core (PRINCIPLES.md #2). `byre
status` shows the resolved allowlist as an Egress section with each
host:port attributed to its declaring skill. v2 candidate: move
package-registry egress into the language templates that imply it.
