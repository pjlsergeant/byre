# Standing instructions are config: the [[context]] key

DELIVERY MECHANICS SUPERSEDED by ADR 0046: context
now reaches every agent by injection through its own vendor channel;
`context_target` (referenced below as the delivery pipe) is retired —
byre never writes an agent-owned file. The vocabulary, scoping, merge
semantics, and editor story here remain current.

Decided 2026-07-24. byre gains a `[[context]]` config vocabulary for
**standing agent instructions** -- prose the operator wants in the agent's
standing instructions in every box a declaring layer reaches. A declaration
is `name` plus inline `text` or a host `file`; the merged prose joins the
baked agent context after the skills' snippets (delivery at decision time
rode `context_target`; today it is injected, ADR 0046). No new scoping machinery: **the cascade is
the scoping** -- `default.config` is machine-global, a template or named
layer is per-stack, the project config is per-project.

Principles: opinion-free core (byre ships no prose; the key is wiring, the
words are the user's), legibility (declarations are named, attributed, and
removable, never smuggled), the footgun doctrine (the threat model is the
agent -- the config lives host-side, out of the box's reach short of an
explicit `--self-edit` grant; the user's own file choices are never
policed, only read safely).

## The problem

The delivery pipe for prose already existed -- a skill's `[context]` table
concatenated into the agent's memory file (`context_target`, since retired) -- but the
only way for an OPERATOR to say "always run the linter" machine-wide was
to dress one paragraph up as a package: `skill init`, a dir under
`~/.byre/skills/`, an enable in every scope. Legitimate ("opinions live in
skills") but disproportionate ceremony for what is one prose file, and the
in-tree alternative (a repo `CLAUDE.md`) is the wrong instrument: it is
the *project's* voice -- committed, collaborator-visible, agent-writable
-- and speaks only to the one agent that reads that filename.

## The model

**Three prose voices, three channels.** The skill author's opinions ride
skill.toml `[context]` (travel with the tool, toggle with the skill). The
project's voice is its in-tree agent memory (committed, agent-writable).
The operator's voice is `[[context]]`: host-side config, out of the
boxed agent's reach -- with one explicit exception: a `--self-edit`
session mounts the project's own store config read-write, so the agent
can then edit the PROJECT layer's declarations (that grant is the
feature); the global default and named layers stay outside every box.
It is also agent-neutral: at decision time it rode whatever
`context_target` the selected agent skill declared; today the same
neutrality comes from each agent's injection adapter (ADR 0046).

**Genus, with one home.** The key joins the named-declaration genus
(`[[mcp]]`, `[[claude_skills]]`; nameddecl.go): layers replace by name,
`!name` closures remove, marker-with-extras and in-layer duplicates
refuse. Deliberately UNLIKE the genus elders there is no skill.toml twin
-- a skill's prose already has its home -- so nothing unions in after the
merge and a closure is fully spent removing the inherited declaration
(survivors in `ContextsClosed` are inert, kept for grammar uniformity).

**Templates may carry it.** `[[context]]` is content, not composition
(the apt/env class, not skills/agent) -- a distributable template shipping
its stack's conventions is legitimate. Inline `text` is the portable form;
a `file` in a template fails loudly on a machine that lacks it.

## Sources and the read

`text` is inline TOML (multi-line strings). `file` is a host path, `~/…`
or absolute -- the `[[claude_skills]] path` anchor rule, for the same
reason: configs live in the user's store and a `default.config`
declaration must reach the same file from every project. It is a declared
build input, read at bake (Assemble), and a missing/unreadable file fails
the develop attributed to its declaration -- solicited means loud, never
degraded.

**Prose size is disclosed, never capped** (ruled 2026-07-26, superseding
the original 1 MiB skill-context cap on file sources, which made the
same prose behave differently by form): escalating develop-time tiers at
100 KiB / 500 KiB / 1 MiB apply identically to inline and file sources,
the loudest saying plainly that a skill is the right home. The one
refusal left is the technical read ceiling far above the tiers (16 MiB,
judged from fstat before reading): the read-boundedness every host read
in byre obeys, so a fat-fingered `file` target cannot balloon the
develop -- a boundedness rule, not a prose budget.

The read rides the stageCopy containment stance: a path inside the
agent-writable tree opens through an `os.Root` anchored there (an
agent-planted escaping symlink is refused, closing "swap the file for a
link and bake a host secret into my own context"), while a path genuinely
outside it is the user's own named file, opened following their symlink.

## Composition order

Chassis facts, base image, provisioned inventory, skill snippets in
enable order, then `[[context]]` declarations in cascade order -- the
operator's voice closes the file. Within the baked file the config prose
is one build input among others: editing it takes effect at the next
develop's rebuild (the layer *files* are live per ADR 0035; the prose is
a bake artifact).

## Consequences

- The context-only-skill recipe is superseded for operator prose; skills
  keep `[context]` for opinions that travel with a tool.
- The key ships WITH its editor story (PRINCIPLES.md #6 -- written off
  the back of this decision's first draft, which shipped "hand-editing
  is the interface" and was reversed by maintainer ruling 2026-07-25):
  the Instructions section in `byre config` for every target,
  effective-view rows attributing each snippet to its layer, prose
  edited via the `$EDITOR` handoff (in the form and in the
  `byre context add` verb alike -- the git-commit shape).
- No status surface: a declaration is wiring with no grant to render --
  the `files` class, not the mounts class.
