# Docker owns caching; byre owns deterministic generation

byre's job is to generate a Dockerfile from config; Docker's job is to
build and cache it. Early designs had byre hashing content to decide
whether to rebuild -- deleted, because `docker build` already does exactly
that per instruction. byre instead guarantees **Dockerfile determinism**
(byte-stable output for unchanged config, pinned by a golden test) and
emits instructions in a stable expensive-and-shared-first order so
Docker's layer cache shares work across projects.

Consequences:

- An unchanged config re-uses the cached image even if upstream moved
  (`node:22` republished). Deliberate: `byre rebuild` (`--no-cache`) is
  the freshness valve. byre promises Dockerfile determinism, not
  upstream-artifact reproducibility.
- `project_id` is a naming device only, never a cache key.
- byre never parses inside raw blocks, so it cannot dedupe across them --
  the one caching discipline asked of users is to keep expensive shared
  installs in the template block, not per-project blocks.
