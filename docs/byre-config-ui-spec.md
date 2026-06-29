# `byre config` — UI design brief (v0)

_For a designer. Describes purpose, content, and constraints — not layout. The
current implementation is a throwaway first pass; design from this, not from it._

---

## 1. What byre is (context)

byre runs an AI coding agent (Claude / Codex / Gemini) inside a **throwaway,
project-scoped Docker container**. Its whole promise: the container sees **only
the project directory and what you explicitly grant it** — nothing else of your
machine. Local-first, inspectable, you run it from a terminal:

```
cd ~/my-project && byre develop      # builds + drops you into the box
```

## 2. What this screen is

`byre config` — view and edit a project's configuration. Two modes:

- **`byre config`** — edits **this project's** config.
- **`byre config --global`** — edits the user's **defaults** applied to every new
  project (their personal baseline + the picker's pre-selected favourites).

It's launched from the user's terminal on the host. It edits a file byre owns on
the host side (never inside the sandbox), so what you set here is **trusted** —
it's the thing that *defines* the sandbox.

## 3. The medium (hard constraint)

This is a **terminal UI**, built in Go on the Bubble Tea / Lipgloss stack. It
runs in whatever terminal the user invoked byre from, **including over SSH**.

Design implications — please respect these:
- **Keyboard-driven.** Don't assume a mouse.
- **Works at ~80 columns**, degrades gracefully wider/narrower.
- **Color is decorative, never load-bearing** (must read on monochrome / no-color
  terminals). Use it to signal grant-weight, errors, focus.
- Rich is fine: panes, scrollable lists, inline editing, key-hint footers, modal
  add/edit. Just not GUI/web idioms.

## 4. The mental model (the spine)

Organize the UI around **"what can this box touch?"**, not "edit these TOML
fields." Three tiers, in priority order:

### Tier 1 — GRANTS — "what can this box reach?" (the heart of the screen)
The deliberate holes in the sandbox. Each one widens what a possibly-autonomous
agent can do, so these are **security-sensitive** and **tuned often**. This tier
should be foregrounded and feel weighty.
- **Mounts** — filesystem access (host paths → into the box)
- **Ports** — network exposure (box ports → out to the host)
- **Env** — values/secrets passed in
- _(later)_ devices/GPU, network mode, Linux capabilities

### Tier 2 — BUILD — "how is the box made?" (set mostly once)
- base image / template, agent, apt packages, skills, container engine

### Tier 3 — RAW ESCAPE HATCHES — **read-only here**
- `run_args`, `dockerfile_pre` / `dockerfile_post`, full-Dockerfile opt-out.
  Shown so the user *knows* they exist and what they currently are, but edited by
  hand in the file (they're open-ended and the highest-privilege knobs). Surface
  a clear "to change these, edit `<path>`" affordance.

## 5. Field reference (the content)

Grant? = appears in byre's security surfaces (`byre status`, the adoption
prompt). Editable = editable in this UI (vs read-only / file-only).

### Tier 1 — Grants

| Field | Meaning | Shape / format | Validation | Grant? | Editable |
|---|---|---|---|---|---|
| **Mounts** | host paths the box can read/write | list of `{host, target, mode}`; mode = `ro`\|`rw` | host non-empty; target absolute; no duplicate targets; **`rw` of `$HOME`/broad paths should warn** | ✅ | ✅ |
| **Ports** | box ports exposed to the host (**new feature**) | list of `{container, host?, interface?}` | container 1–65535; host optional (blank = ephemeral); **interface default `127.0.0.1`** | ✅ | ✅ |
| **Env** | env vars into the box | list of `{key, value}`; value verbatim | key matches `[A-Za-z_][A-Za-z0-9_]*`; **consider masking secret-looking values** | ✅ (values may be secret) | ✅ |
| _Network mode_ | currently always open bridge | enum: `open` \| `none` (offline) | — | ✅ | _future_ |
| _Devices / GPU_ | `--gpus`, device passthrough | list | — | ✅ | _future_ |
| _Capabilities_ | Linux `--cap-add` | list | known cap names | ✅ (dangerous) | _future / advanced_ |

### Tier 2 — Build

| Field | Meaning | Shape | Notes | Editable |
|---|---|---|---|---|
| **Base image** | the FROM image | string, e.g. `golang:1.22-bookworm` | usually set by the template | ✅ |
| **Template** | a named starter (sets base/apt) | select from discovered templates + `none` | discovered from `~/.byre/templates/` | ✅ |
| **Agent** | which AI agent runs | select from discovered agent skills + `none` | `claude` / `codex` / `gemini` | ✅ |
| **Apt packages** | extra Debian packages | list of strings | | ✅ |
| **Skills** | enabled skill bundles | multi-select from discovered skills | the agent is itself a skill | ✅ |
| **Engine** | container runtime | enum: `auto` \| `docker` \| `podman` | | ✅ |

### Tier 3 — Raw (read-only in UI)

| Field | Meaning |
|---|---|
| `run_args` | raw `docker run` flags (can grant `--privileged`, the docker socket, host net) |
| `dockerfile_pre` / `dockerfile_post` | raw Dockerfile lines injected before/after byre's build |
| `dockerfile` | full hand-written Dockerfile opt-out (a path) |

## 6. Interaction requirements

- **List editing** is the core gesture (mounts, ports, env, apt, skills): add /
  edit / remove an item, ideally without leaving the screen. This is where the
  current UI is worst — flat textareas. Items deserve structured rows.
- **Selects** for template / agent / engine, populated from what's installed.
- **Inline validation** with clear messages (bad port, empty mount target,
  malformed env key) — catch before save, don't fail after.
- **Save / cancel** that's obvious. Cancel must leave the file untouched. Save
  validates the *whole* config (e.g. mount/port/volume target collisions) and
  reports any conflict.
- **Always show the target file path** (where edits land) — and for the raw tier,
  that same path as "edit here."
- **Grant weight should be legible.** A glanceable summary like _"This box can
  reach: 2 host paths (1 writable), 1 exposed port, 3 env vars"_ helps the user
  see the sandbox's footprint at a glance.

## 7. Trust & consistency (why grants matter here)

- This UI edits the **host-side, trusted** config — the file that *defines* the
  sandbox. (A config inside the project tree would let the contained agent rewrite
  its own sandbox; byre forbids that. So edits here carry real authority.)
- Grants set here appear in two other byre surfaces that already exist:
  **`byre status`** ("what can this thing touch?") and the **adoption prompt**
  (when a repo ships a config, byre shows its grants before a human accepts it).
  Use **one consistent vocabulary and visual language for grants** across all
  three so "a mount", "an exposed port", "a capability" look the same everywhere.

## 8. Decisions to make (with recommendations)

1. **Does `byre config` own the Build tier, or just Grants?** Recommendation:
   own both, but lead with Grants; Build is secondary (and the first-run picker
   already covers agent/template at creation).
2. **Ports model.** Recommendation: `{container, host?, interface?}` with
   **`interface` defaulting to `127.0.0.1`** (localhost-only is the safe default;
   binding to all interfaces / the LAN is a louder, opt-in grant). Support both
   "fixed host port" and "blank = ephemeral".
3. **v1 grant scope.** Recommendation: **mounts + ports + env** for v1; defer
   devices/GPU, network-mode, and capabilities (capabilities can stay raw).
4. **Secret handling for env.** Decision needed: mask secret-looking values?
   Warn that env values live in a host-side file (not encrypted)?

## 9. Out of scope (v0)

- Editing the raw tier in-UI (run_args / dockerfile_*).
- Devices/GPU, network-mode, capability editing.
- Per-skill configuration.
- Mouse-first / web layouts.

## 10. Anti-goals (what the throwaway version got wrong)

- A flat dump of every TOML field with no grouping or hierarchy.
- Lists-as-textareas (`apt`, `env`, `mounts` as free text) — no structure, no
  per-item validation, easy to corrupt.
- No sense of which settings are security-weighty vs cosmetic.
