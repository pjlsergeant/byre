# skills/ -- byre's own dev-harness skills

Skill packages that are byre-repo dev tooling, not product -- installed via
path `[sources]` hints in `byre.preset`. The full story (why in-repo, the
packed-manifest edit loop, the inttest VM) is in
**`docs/BYRE-DEVELOPMENT.md`**; read that before editing anything here --
in particular, each `skill.toml` is committed *packed* and must be
re-packed after any payload edit.

- **inttest** (`pjlsergeant/inttest`) -- run the gated integration suite on
  the sacrificial Lima VM (`byre-inttest` on PATH; the VM template rides the
  package).
