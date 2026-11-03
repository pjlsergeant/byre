# E1: engine build-cache import/export, measured (2026-11-03)

Status: investigation complete, DECISION PENDING (Pete). Measured on the
sacrificial inttest VM; raw logs and scripts in ~/scratch/headless-e1/ on the
planning box. Lifecycle: absorbed into ADR 0065 if adopted, otherwise into a
sentence in ADR 0061, then deleted.

Investigation for hosted-runner builds under `byre run` (ADR 0061; TODO.md
"`--build-cache` for hosted runners"). Measured
2026-11-03 on the inttest Lima VM (Debian 13, kernel 6.12, 4 vCPU, 3 GB,
host 172.17.0.1:60022). Nothing in /workspace was changed.

- Engines: Docker 29.6.2 (BuildKit v0.31.2, buildx v0.35.0, **containerd
  image store**, default `docker` driver); rootless Podman 5.4.2.
- Sample project: an empty git repo, `byre build --template none --agent
  claude` (byre built from this branch, `headless-runs` @ 435a6e1f +
  working tree). Its generated context (36 steps: apt gosu/curl/jq, the
  claude installer, byre's support files) was copied out and built with
  the raw engine commands byre's `runner.Build` uses, plus
  `--build-arg BYRE_UID=1000 --build-arg BYRE_GID=1000`.
- Registry: `registry:2` on `localhost:5000` (plain http, no auth),
  running on the VM itself.
- "Fresh runner" between runs: docker -- remove the test tag and
  `debian:trixie`, `docker builder prune -af` (verified: build cache 0B,
  no base image); podman -- a throwaway `--root/--runroot` store per
  measurement, deleted between runs (verified: 0 images).

## Wall-clock (seconds, one run each unless noted)

| measurement | docker | podman |
|---|---|---|
| cold (no cache, no base image) | 81.8 / 66.8 (mean 74.3) | 75.7 / 73.6 (mean 74.7) |
| cold + export cache (the first CI job) | 70.4 (`type=local`) | 107.9 (registry) |
| warm rebuild, local engine cache (persistent runner) | 2.5 | 4.3 |
| export only, from warm cache | 8.2 (local) / 7.0 (registry) | 35.4 (registry) |
| **import from local dir**, fresh engine | **16.9** -- 35/35 CACHED | not supported |
| **import from registry**, fresh engine | **22.7** -- 35/35 CACHED | **21.5** -- 41/41 "Using cache" |
| import + export (every later CI job) | 26.5 (local) | 20.9 (registry) |
| `--no-cache --pull=true` + import (+ export) | 68.3 -- 0 CACHED, export written | 108.5 -- 0 cached, export pushed |
| `--no-cache --pull=always` + import | -- | 73.3 -- 0 cached |
| `--layers=false` + import + export | -- | 61.3 -- 0 cached, nothing pushed |
| base image pull alone (`debian:trixie`) | 5.4 | 2.5 |

Speed-up of a later job's build over a cold build: docker **4.4x**
(local dir import), **3.3x** (registry import), 2.8x (import + export);
podman **3.5x** (registry import), 3.6x (import + export). The saving is
about 50s per job on a build that takes about 75s cold here. A warm local
cache, which a persistent runner keeps, is still 7-10x faster than any
import.

Exported cache size for this claude box: docker `type=local,mode=max`
dir 204 MB on disk (213.5 MB of blobs over the registry form); podman
220.2 MB over 35 per-step blobs. Exporting under `--no-cache` writes the
same 204 MB.

Not measured: the network time to restore ~210 MB on a real hosted runner
(actions/cache restore of a dir, or a pull from ghcr.io). Every transfer
here was VM-local, so the import rows are a lower bound on a real
runner's import cost; the cold rows include real internet downloads (apt,
the claude installer, Docker Hub).

## Flags that worked

docker (plain `docker build`, which is buildx/BuildKit on Docker 29):

    docker build ... --cache-to   type=local,dest=DIR,mode=max
    docker build ... --cache-from type=local,src=DIR
    docker build ... --cache-to   type=registry,ref=localhost:5000/byre-cache:docker,mode=max
    docker build ... --cache-from type=registry,ref=localhost:5000/byre-cache:docker

podman (registry refs only, repository without a tag):

    podman build ... --cache-to   localhost:5000/byre-cache-pm
    podman build ... --cache-from localhost:5000/byre-cache-pm
    # --tls-verify=false was needed ONLY for this plain-http test
    # registry; byre would not pass it. It is a build-wide flag that also
    # affects the base pull.

## The four questions

1. **Does `--cache-to` need BuildKit/buildx, and `--layers` on podman?**
   docker: it needs BuildKit, and plain `docker build` IS BuildKit on
   Docker 29. With `DOCKER_BUILDKIT=0` the legacy builder rejects the
   flag (`unknown flag: --cache-to`, exit 125). `docker buildx build`
   behaves the same (2.2s, all CACHED). This VM uses the containerd image
   store, which lets the default `docker` driver export cache. On the
   classic overlay2 store, the `docker` driver refuses cache export
   ("Cache export is not supported for the docker driver"), and the user
   would need a `docker-container` buildx builder. That limit comes from
   the BuildKit docs; it was NOT measured here. Hosted runners with an
   older Docker or an upgraded install can be on overlay2.
   podman: `--layers` is required, and it defaults to true. With
   `--layers=false`, `--cache-from` and `--cache-to` are silently
   ignored: 0 steps reused, 0 pushes, exit 0. podman has no `type=local`
   dir form; `--cache-from type=local,src=...` fails with `invalid repo
   ... must contain registry and repository` (exit 125).
2. **Does it compose with byre rebuild's `--no-cache --pull=true`?** Yes
   syntactically, on both engines: exit 0, the base is pulled, the export
   is written or pushed in full. But `--no-cache` disables the import, as
   it should: 0 steps reused on both engines (docker reads the cache
   manifest and then uses nothing). The same holds for podman with
   `--pull=always`. So `rebuild --build-cache X` means "build fresh, then
   refresh X". That is coherent, and byre has nothing to special-case.
3. **Does a registry ref need auth plumbing byre would have to own?** No.
   The engines read their own credential stores: docker's
   `~/.docker/config.json` and its credential helpers (`docker login`),
   podman's `auth.json` (`podman login`). A CI job logs in before the
   byre step, as it would for any push. The test registry needed no
   login; podman needed `--tls-verify=false` only because the registry
   was plain http. A real HTTPS registry (ghcr.io and the like) needs
   neither flag. docker's `type=gha` backend, which needs the Actions
   runtime token, was not tested; a raw `--build-cache` value could not
   express it anyway.
4. **How big is the exported cache for a claude box?** About 205-220 MB:
   docker dir 204 MB, docker registry 213.5 MB, podman registry 220.2 MB.

## Recommendation

**ADOPT** (as ADR 0065: a `--build-cache <ref|dir>` flag on
`run|develop|build`, with engine-shaped rendering in `runner.Build`).
The measurement holds on both engines: a later job that imports the
cache builds the claude box in 17-23s instead of ~75s cold, a 3.3-4.4x
speed-up (docker local dir 4.4x, docker registry 3.3x, podman registry
3.5x), and 2.8-3.6x even when the job also re-exports. The flags take
nothing from byre beyond rendering one argv: no byre-side hash (ADR 0001
stands -- the engine still decides per step what is cached), no auth
plumbing (the engines' own `login` covers it), and they compose with
`rebuild`, which correctly ignores the import and refreshes the export.
The rendering must be engine-shaped and fail loudly on the gaps:
- docker: a dir becomes `--cache-from type=local,src=D --cache-to
  type=local,dest=D,mode=max` (BuildKit replaces the dir). A ref becomes
  `type=registry,ref=R` on both, with `mode=max`.
- podman: a ref becomes `--cache-from R --cache-to R`. A dir is a usage
  error naming the limit, because podman has no local cache backend.
  byre must not pass `--layers=false` alongside it, since podman then
  ignores the cache without saying so.
- On a classic-store docker the engine's own error surfaces; the docs
  should name the remedy (containerd image store or a buildx
  `docker-container` builder).

The honest ceiling: the saving is ~50s a job on this box. The import cost
on a real runner adds the download of ~210 MB, which was not measured, so
the docs should sell it as "skip most of the image build", not as a
number. If ~50s on top of an agent run that takes minutes is judged too
small to carry an ADR, the fallback is DO NOT ADOPT: v1 stays "hosted
runners rebuild per job", and nothing measured here makes that wrong,
only slower.

## Deviations and caveats

- The first podman pass removed images and pruned dangling ones between
  runs. That did not clear the layer cache once cache images had been
  pulled: its "cold again" run reused 41 of 41 steps in 6.5s. The pass
  was discarded (logs kept in `podman-run1-weak-reset/`) and rerun with a
  throwaway storage root per measurement (`podman/`). Every podman number
  in this report comes from the rerun.
- Docker's cold runs varied 66.8-81.8s on network noise; one sample per
  import row.
- VM side effects outside this investigation: `docker builder prune -af`
  emptied the VM's whole BuildKit cache, which other boxes' inttest runs
  will rebuild. The first podman pass's `podman image prune -f` also
  removed two stale `<none>` podman images and re-pulled `debian:trixie`
  in both stores. `docker volume prune -f` at cleanup removed one
  anonymous volume (939.6 MB), which matches the registry container's
  store.
- Left clean: the registry container and its image, the test images, the
  sample project (`byre forget -y`), the cache dirs, the throwaway podman
  store, the leftover `localhost:5000/*` podman images, and `~/e1*` are
  all removed.

## Raw logs (beside this file)

- `00-sync.log` .. `03-byre-build-first.log`: sync, VM info, byre build
  of the sample project.
- `05-docker-run.log`, `docker/` (per-measurement logs and
  `results.txt`).
- `06-podman-run.log`, `podman-run1-weak-reset/`: the discarded first
  podman pass.
- `07-podman-isolated-run.log`, `podman/`: the podman numbers used here.
- `08-registry-sizes.log`, `09-cleanup.log`, `scripts/`: the measurement
  scripts as run.
