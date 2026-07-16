# Composable box configurations -- named layers and `extends`

Status: design pass output (grilling session, Pete + Claude, 2026-07-16).
Awaiting Pete's confirmation of shared understanding; not yet implemented.
Absorbed into an ADR + docs when it ships; this file is then DELETED
(wip/ rules).

## Problem

One employer (Torn) with many projects: a central Torn config, each
project adding on. Today's cascade is a fixed three layers
(`default ⊕ template ⊕ project`) -- there is nowhere to put a shared,
user-authored baseline between the global default and a project.

## Decisions

1. **Use case & trust.** The shared layer is user-authored (employer or
   personal baseline) and carries the FULL config vocabulary -- skills,
   agent, mounts, env_from_host, egress, `[sources]`, everything. The
   template shape-only ban is about distributable packages and stays
   where it is; it does not apply to layers.

2. **Live layer, not preset-copy.** Layers are resolved at every
   develop, like the template slot. Edit the Torn layer once and every
   Torn project's next develop picks it up. Presets remain the
   apply-time consent ceremony and may reference layers.

3. **Plain files, not packages.** A named layer lives at
   `~/.byre/layers/<name>/layer.config` (directory form; may carry
   payload files beside it for `files = {...}`). No `[package]` table,
   no version, no pack/install/fork verbs. Distribution = send someone
   the file. If installable layers are ever wanted, that is a future
   ADR facing the third-party-composition consent question head-on.

4. **Chaining: scalar `extends`, linear chains only.** Any layer --
   including the project config, which IS a layer (the leaf) -- may name
   at most ONE parent via `extends = "<name>"`. Chains are arbitrary
   length; byre walks to the root and merges root-first. No lists, so
   no diamonds and no linearization rule. Cycles and dangling parents
   are hard errors (the cycle error names the loop; the dangling error
   names the missing path).

   Combining orthogonal stacks (employer chain + language shape) is
   covered because **the template slot survives unchanged** -- it is NOT
   subsumed into the chain. Cascade:

   ```
   default ⊕ template ⊕ chain(root … parent) ⊕ project
   ```

   Widening `extends` to a list is a backward-compatible future step if
   a real two-chain need appears; not paid for now (proportionality).

5. **Key bans in layer files.** `template` is banned in a layer file
   (loud parse error) -- shape selection has exactly one owner, the
   project config (default.config only as picker prefill). `extends` is
   the only pointer key a layer may carry. Existing picker-state strip
   rules (`shared_auth` etc.) apply unchanged.

6. **Reserved names.** A layer may not take a bundled or retired
   template name. `byre layer new` refuses at creation; a hand-dropped
   squatter gets the LEGACY treatment (never loaded, listed with
   reason). No shadowing rule needed because shadowing cannot arise.

7. **Merge mechanics: nothing new.** Each chain layer is one more fold
   step under today's exact rules -- scalars last-wins, lists union,
   `!name` / `remove = true` removals apply against everything merged so
   far, later layers can re-add. `[sources]` hints are stamped with the
   contributing layer's name (existing stampSources, more callers).

8. **Consent surface.**
   - Attribution everywhere names the layer: `byre status`, the preset
     apply grant review, missing-package remedies
     (`mount ... -- from layer torn`). Reports print the resolved chain
     (`torn -> torn-frontend -> project`).
   - Layer edits propagate with NO ceremony -- no drift notes, no
     re-review. Same trust position as editing default.config; the
     threat model is the agent, never the user.
   - A preset referencing a layer the machine doesn't have fails loudly
     at apply with the exact path to create. No install hint (layers
     aren't packages).

9. **Self-edit exclusion.** Layer files are OUTSIDE the `--self-edit`
   writable set: a boxed agent must never edit a file that propagates
   into other projects' sandboxes. Escape hatch if a box legitimately
   needs to edit layers: an explicit RW mount of `~/.byre` -- a visible
   grant that documents itself.

10. **CLI surface.**
    - `byre layer new <name>` -- stub + the reserved-name gate.
    - `byre layer list` -- enumerate, flag broken (parse errors,
      dangling extends, reserved-name squatters).
    - `byre layer validate <name>` -- parse + ban list + chain walk;
      mirrors `byre template validate`.
    - `byre config` (project) becomes chain-aware: inherited entries
      attributed to their layer; removals written to the project layer
      as today. It also gains an EXTENDS section to pick/change/clear
      the project's parent layer.
    - `byre config --layer <name>` -- the same effective-state editor
      pointed at a layer: resolves `default ⊕ ancestors ⊕ <name>`,
      shows that state with ancestor attribution, writes to the layer's
      file. Descendants/projects are deliberately not in view.
    - NOT built: `edit` ($EDITOR does it), `show` (status/grant review
      own the composed view), pack/install/fork.
    - The first-run picker never writes `extends` (keys with teeth are
      never picker-written).

11. **Vocabulary & docs landing.** Glossary: "Layer" definition survives
    ("one file in the cascade"), extended -- layers can be *named*
    (`~/.byre/layers/<name>/`) and chained via `extends`; new entry
    **named layer**. New ADR ("Named layers and the extends chain")
    recording the cluster above, citing ADR 0003 (host-side stores),
    0018 (merge/off-switches), 0029 (package boundary).
    ARCHITECTURE.md Config section: cascade line becomes
    `default ⊕ template ⊕ chain… ⊕ project`. SKILLS.md keeps
    "composition belongs in a preset" for templates; a line
    distinguishes layers (user-authored, may compose). This wip file is
    deleted on absorb.

## Implementation sketch (order of work)

1. `internal/config`: `Extends string` on Config; layer-file parse
   (ban `template`, allow the rest; ValidateLayer); chain loader with
   cycle/dangling detection; resolveWith folds
   default ⊕ template ⊕ chain ⊕ project. Golden tests for merge order,
   cycles, reserved names.
2. Attribution: stampSources per layer; grant-review / status labels.
3. Preset apply: chain resolution in review; missing-layer remedy.
4. `cmd/byre`: `layer new|list|validate`; `byre config` EXTENDS section
   + `--layer` target.
5. Self-edit: assert layers dir is outside the writable set (test).
6. Docs sweep: ADR, GLOSSARY, ARCHITECTURE, SKILLS, README.
