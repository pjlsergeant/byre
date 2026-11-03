---
title: Security model
weight: 85
description: the threat model, the contract, and the sharp facts
params:
  reference: true
---

This page is the detail behind
[What's boxed, what isn't](/docs/whats-boxed/): the threat model, and
every residual byre knows about, grouped by where it bites. byre is
young; read the warning on the [install page](/docs/install/) first.

## Threat model

**The threat is the agent, never the user.** byre exists so an
autonomous coding agent can run at full permissions without wearing your
identity or reaching your machine. It is NOT designed to protect you
from yourself: every protection is one config edit from off, and that is
deliberate. byre's standing promise is legibility, not gates:
`byre status` always tells you what the box can reach.

byre is built for over-eager, reckless, or misbehaving agents -- the
ordinary case, where the agent isn't trying to do anything to you.
Against an actively malicious agent, including one acting on hostile
instructions it read somewhere, byre offers the precautions on this
page and nothing more. This isn't a security product. It is not built
to resist an agent deliberately working to get out, or a dedicated
attacker with kernel exploits.

## The contract

- **Boxed:** your host filesystem, your environment, and your
  credentials stay outside unless you grant them.
- **Open by design:** the network (until a firewall skill closes it) and
  the project directory, which the agent can write.
- **Not a microVM:** a container shares the engine host's kernel.
- **Locked against the agent, not you:** every protection is one config
  edit away from off, and `byre status` shows where it is.

The full contract: [What's boxed, what isn't](/docs/whats-boxed/). The
rest of this page is the residuals.

## Project credentials

**Project credentials protect the values on disk, and only there.**
Once a launch unlocks them the box has them, and anyone who can write
your config files can replace them.

- **Storage.** A credential is an age-encrypted value stored inline in
  your config files -- an `encrypted:` or `encrypted-file:` row in
  `[env_from_host]`, opened by the identity in that file's own
  `[credentials]` block, which is itself wrapped under a passphrase with
  scrypt
  ([ADR 0057](https://github.com/pjlsergeant/byre/blob/main/docs/adr/0057-project-credentials.md)).
- **Unlock.** byre decrypts only at launch, after you enter the
  passphrase. It writes the decrypted plaintext to exactly one
  filesystem place -- a per-session tmpfs that empties when the box
  stops -- and to no host file, image layer, engine-visible config
  value, or volume.
- **What it protects.** Off-box disk access: a stolen laptop, a backup
  blob, a synced dotfiles directory. That is the whole of the security
  content. Your backups and synced dotfiles carry the ciphertext, so
  the passphrase is what stands between a copied config file and the
  value inside it, and a weak passphrase weakens the encryption to
  match. There is no recovery path: a forgotten passphrase means
  `byre credentials unset` on each row and setting the values again.

**What it does not protect:**

- **Delivered values.** The agent is handed the values -- that is the
  point -- and can copy or send them anywhere it can reach. An open
  network exfiltrates an unlocked credential, so pair credentials with
  the firewall skill. A mount placed over the delivery tmpfs relocates
  the plaintext onto durable storage and can re-surface it on a bare
  restart without a fresh unlock (disclosed as a managed-path shadow).
  Transient plaintext in process memory -- byre's during launch, the
  agent's afterwards -- can reach swap, core dumps, or a hibernation
  image.
- **Names.** The **key names are cleartext**, and so is the recipient
  each value is encrypted to. Disk access learns which credentials exist
  and what they are called.
- **Integrity.** **Anyone who can write your config files can change
  what a row delivers.** The encrypted payload is stamped with the key
  and kind it was set for. That catches accidents -- a blob swapped
  between rows, a value replayed from git history onto a renamed key, a
  copy-paste across files -- but it is not integrity: anyone holding
  the cleartext recipient can produce a correctly-stamped value. The
  prize is durable poisoning of future sessions -- a swapped API key
  that outlives the box -- not plaintext, which the box is handed
  anyway. This bites in three places:
  - `byre preset apply`: your repo is the box's own writable tree, so
    if you ship credentials through a repo preset, whatever writes that
    repo can **mint** a chosen value (it has the recipient), or
    **swap**, **transplant**, or **replay** existing blobs without it.
  - `byre restore`: a backup carries the config file as it is, rows and
    `[credentials]` block included. `byre backup` writes it to the
    current directory by default -- often the project folder, which the
    box can write -- and restore writes the rows it carries byte for
    byte, so whatever writes the file before you restore it can do the
    same.
  - `byre develop --self-edit`, which hands the agent the project's
    config ([below](#self-edit-sessions)).

  byre answers this at the consent gate rather than by hiding the
  values: the apply review flags every changed row where either side is
  a credential ("if you didn't rotate this credential, reject"), rows
  that appeared or vanished, and any change to the `[credentials]`
  block itself. Restore has no earlier file to compare against, so its
  review lists every credential row as appeared, says the backup brings
  its own credentials identity, and says the file's authorship is not
  proven. Credentials you never ship through a preset or a backup have
  no exposure here.

  The unattended `byre preset apply --yes` (for scripts and CI) never stands in for
  that confirm. `--yes` consents to the grants the review shows -- the
  review still prints, into the job log -- and refuses, on a terminal
  too, when the preset or the config it would replace carries a
  credential row or the credential lines show any other change (an
  identity arriving, leaving or replaced). Apply such a preset
  interactively, or keep credentials out of it and hand them to the box
  with `env_from_host` from the runner's environment. What you live with
  on that path: nobody reads the review at apply time, so what you
  consented to is the preset as committed, and a commit nobody reviewed
  applies unread
  ([ADR 0063](https://github.com/pjlsergeant/byre/blob/main/docs/adr/0063-preset-apply-yes.md),
  [the scripted-setup recipe](/docs/how-do-i/workflow/#set-up-a-box-from-a-script-new-machine-ci)).
- **The screen.** The Env editor's multiline draft (`^e`) shows the
  value you type or paste, unmasked, to anyone watching or recording,
  after a warning; stored values are never loaded into it and no
  plaintext editor file is written. Text pasted into the Key field is
  visible too. Paste is not byte-exact: unbracketed control characters
  can act as shortcuts, terminal paste may drop invalid UTF-8
  (U+FFFD), and the passphrase widgets stay single-line and can
  normalize pasted controls. Line-break handling and the byte-exact
  `--file` route are in the
  [configuration reference](/docs/configuration-reference/) (file-kind
  input needs an existing identity).

## The engine and the host

**Docker daemon access is root-equivalent.** Anyone who can talk to the
Docker daemon (the `docker` group) can mount any named volume -- byre's
included, identity volumes included -- or the host filesystem itself.
This is Docker's design, not a byre gap, and byre cannot change it. On
shared machines, treat daemon access as root. Images
(`byre-<id>-u<uid>-...`) and machine volumes (`byre-machine-u<uid>-...`)
are uid-qualified, which stops users sharing them by accident but cannot
stop a daemon user doing it deliberately. **Project state volumes
(`byre-<id>-...`) are not uid-qualified**, so two Unix users sharing one
checkout on the same daemon share that agent state, and one's
`reset`/`forget` can clobber it; byre is a single-operator tool and does
not re-key them. `byre shell` does hide a box started by another Unix
user (`--skip-uid-check` enters it anyway, as that box's dev identity).
The optional `docker-host` skill is exactly this grant, made legible:
see
[docs/DOCKER-HOST.md](https://github.com/pjlsergeant/byre/blob/main/docs/DOCKER-HOST.md)
before enabling it.

**A mount over byre's own paths wins at runtime, and byre only tells
you.** byre bakes its machinery into the image -- the launcher, the
launch gate the firewall's fail-closed wait reads, and the delivery
artifacts under `/etc/byre` -- and re-asserts the security-critical ones
at the end of the build, so a `files` entry cannot quietly replace them.
A bind mount or a named volume is different: the engine applies it over
the finished image, and byre has nothing that runs afterwards. A volume
at `/etc/byre` is filled once -- from the image when the engine first
mounts an empty one, or from your seed if it has one -- and is the
authority from then on, so a gate a later build bakes never reaches the
box, and anything that empties the file it holds is permanent. The
launcher treats no gate as nothing to wait for, so the next `docker
restart` brings the box up with its netns unfirewalled. `byre status`,
`byre develop`, and the grant review a preset asks you to approve all
disclose any mount or volume covering those paths -- the project's own
and any a skill declares, named -- in the Containment register, and then
run it: this is your configuration to make
([ADR 0052](https://github.com/pjlsergeant/byre/blob/main/docs/adr/0052-runtime-mount-shadowing-one-disclosure.md)).

**Concurrent sessions share a pathname race.** byre hands every bind
mount to the engine as a pathname, not an inode-pinned handle. The
docker/podman CLI-to-daemon contract is a pathname, resolved in the
daemon's own namespace (a VM under Docker Desktop), so byre cannot pin
it from the host side. The consequence: an agent in a concurrent rw
session that can rename an ancestor of a bind source during *another*
launch's short detect-to-mount window could redirect that bind.
Worktrees make concurrent sessions an ordinary workflow, which is why
this is worth knowing; the window belongs to a launch in progress -- a
session already running is not affected. The full analysis is in
[ADR 0009](https://github.com/pjlsergeant/byre/blob/main/docs/adr/0009-worktrees-inherit-project-identity.md).

## Skills and config

**A skill is trusted code -- enabling one hands it the box.** Skills
ship raw Dockerfile lines, shell hooks sourced at launch, and (for a
network-posture skill) a root helper in the box's network namespace.
The allowlists on a skill's typed fields (`apt`, env keys, `egress`)
exist for legibility -- so a typed field always reads as data -- not as
containment; there is no sandbox between an enabled skill and the box
it builds. The agent `command` is deliberately a shell fragment for the
same reason (flags ride in it; quoting it would contain nothing).
Treat enabling a skill like installing an editor extension: a trust
decision about its author, made once, with the consequences legible in
`byre status`.

**Config `env` values are baked into the image.** They are emitted as
Dockerfile `ENV` layers, so `docker history` shows them to anyone with
daemon access, and they stay in the image after you delete them from
config -- `byre reset` clears volumes, not images -- until it is rebuilt
or deleted. Changing one re-runs the project block's installs. byre
images never leave your machine (there is no push path). Secrets belong
in an agent's own login, a shared-auth skill, an `[env_from_host]`
passthrough from your host environment, or a project credential.

**byre's launcher runs its own helpers on the system PATH; the agent
does not.** The box's PATH puts `~/.local/bin` first, where agent CLIs
install, and the agent and your repository's code can write there. The
launcher runs its own helpers from the system directories, re-pinned
around each launch env hook
([ADR 0028](https://github.com/pjlsergeant/byre/blob/main/docs/adr/0028-env-d-hooks-are-pure.md)).
What still resolves on the agent's PATH, by design:

- **The agent's own command.** Your tools live on that PATH, so a
  program planted in `~/.local/bin` under the agent's name runs as your
  agent.
- **The interactive login hooks** (codex-login, grok-login,
  opencode-login and the shared-auth first-run hooks), because they run
  the agent CLIs. A helper planted there gains only what the agent,
  which can already write that directory, already has.
- **grok-shared-auth's broker**, on grok's PATH, which refreshes the
  machine-wide grok login the agent can already read and write.

The claude onboarding seed's publish (`mktemp`, then reopen) is not
atomic: it assumes nothing else runs during setup, where the agent has
not started and repo code has finished.

### Self-edit sessions

**`--self-edit` is transitive trust of the agent with your host.** A
self-edit agent authors the next develop's config -- mounts, run args,
build context, credential rows -- through the front door, because the
session mounts the project's store read-write. There is no meaningful
containment beyond that point, and byre does not pretend otherwise: the
launch banner says exactly this, and the session ends with a diff of
what changed in the store.

**A `--self-edit` box can stop `byre status` describing it.** Every
container carries a launch record -- what byre told the engine, in the
project store, addressed by the hash of its own bytes
([ADR 0053](https://github.com/pjlsergeant/byre/blob/main/docs/adr/0053-launch-record-content-addressed.md))
-- and that store is exactly what `--self-edit` mounts read-write. byre
re-hashes rather than trusts, so a record an agent EDITS is refused, not
believed; but an agent can delete its own record or make it unreadable,
and status then says so and falls back to describing your current
config. The failure direction is a claim withdrawn, never a false one,
and the record only ever informs a human reading status -- no host
action is driven by it. Status falls back the same way for any box
that has no launch record.

## The network

**An open network is an exfiltration channel.** With the default open
posture, an agent can send anywhere -- including the project it is
working on, or any credential you passed in. The firewall skill
(deny-by-default, per-skill derived allowlist, applied from outside the
box, fail-closed launch gate) is the mitigation; enable it if this is in
your threat model. The firewall-open skill is NOT: its open-denylist
posture blocks a snapshot of named hosts' IPs -- hygiene against
well-behaved telemetry clients, trivially routed around by an agent that
wants to. The network under it is still open, and this document treats
it as such.

**An allowlisted host is a channel, not a permission.** The firewall
grants *reachability of an endpoint*, never an intent: byre cannot see
what travels over a door you opened, and does not try. Three shapes of
this:

- **DNS.** DNS still resolves under deny-by-default -- the rules allow
  port 53 to the nameservers the box actually uses, because a box that
  cannot resolve cannot work -- so data can be tunnelled out through
  your own resolver
  ([ADR 0010](https://github.com/pjlsergeant/byre/blob/main/docs/adr/0010-firewall-outside-the-box.md)).
- **Shared fronts.** An allowlisted CDN or platform host is an address
  that fronts many services and, on the ones that host user content, an
  upload endpoint; granting `443` to it grants everything reachable at
  that address on that port.
- **Tunnel skills.** A skill whose *function* is a tunnel -- exposing a
  local port to the public internet -- declares the endpoints it needs,
  and byre opens them, because a skill's own functional egress is
  exactly what auto-opens
  ([ADR 0020](https://github.com/pjlsergeant/byre/blob/main/docs/adr/0020-egress-doors-ship-closed.md)):
  enabling it is the grant.

The firewall's job is that the list of open doors is short, derived
from what you enabled, and legible in `byre status`. It is not that
what goes through them is safe.

**The firewall's allowlist is an IP snapshot.** A hostname grant is
resolved once, at session launch, and the rules pin those IPs; the name
is never re-resolved while the box runs. If the host's DNS answer moves
after that -- CDNs rotating a pool, and some resolvers rotating the
answer on *every query* (Azure's forwarding DNS does) -- connections to
that host start failing even though it is granted. On a per-query
resolver this can bite seconds after launch, not just mid-session. The
failure direction is always closed: rotation can cost you reachability,
never containment. A session restart re-resolves; for an endpoint with
a stable address, granting the IP directly sidesteps the race.

## What comes out of the box

**What comes out of the box may be executable.** The project is not just
data; it is a directory your host tools run code from. An agent with the
project mount can leave behind any of these:

- a git hook, or a git config that names a program (`core.hooksPath`,
  `credential.helper`, `core.sshCommand`, `core.fsmonitor`, a
  `filter.*` or `diff.*` helper);
- an `.envrc` that direnv evaluates when you `cd` into the project, or
  an `.env` whose values steer a later process;
- an editor task, or a build script.

Your later host command that reaches one runs it, as you. Which command
depends on what was planted, but ordinary use of the project is the
trigger and nothing else has to go wrong. The firewall skill does not
help here: this rides the filesystem, not the wire. Neither does
reading the diff, for some of it -- the git admin directory sits
outside the working tree and never shows in `git status` or `git diff`,
and ignored files are normally omitted as well, while editor tasks and
build scripts usually do show. Git's configuration is also more than one
file: `include.path` and `includeIf` can pull in others. The practical
rule is that a tree an agent has worked in is its output rather than
your code, and running anything out of it -- `git` included -- runs
what it wrote.

When a session ends byre mentions changes it noticed in a few of those
places, as a notice: it checks a handful of spots, not everything, and
a quiet exit is not a clean bill of health.

**byre holds its own host commands to that rule.** The engine CLI,
`git`, `ssh`, the shell it opens your `$EDITOR` through and the
clipboard helpers are each resolved once per run, and a binary your
PATH answers with out of a directory that project's box can write is
declined by name -- the tool, the path and the directory, so the fix is
reordering PATH. It is no comment on your PATH, and no judgement of
what is inside a binary; where byre resolved it from is the whole test.
One directory is never treated as box-writable: the filesystem root.
Graphical launches start byre from `/`, and a project AT `/` would make
every binary on the machine "inside" the box's tree, so for a box you
deliberately run on `/` the project-tree half of that check does not
apply (byre's own store for the project is still covered). That is a
real gap, not a vacuous one: such a box writes whatever your user can
write, PATH entries included. Running byre on `/` is handing the agent
your whole writable surface, and byre takes you at your word.

## Shared state

**Shared (machine-scoped) volumes cross project boundaries.** The
shared-auth skills
([ADR 0017](https://github.com/pjlsergeant/byre/blob/main/docs/adr/0017-shared-agent-identity.md))
put one agent credential where the box of every project that opted in
can use it -- that is their purpose. A misbehaving agent in any box that
opted in can read (and so exfiltrate) that credential, exactly as it can
its own per-project login; the tokens involved are inference-scoped
where the vendor allows it (Claude's setup-token). `byre status` names shared
volumes; `reset` / `forget` never delete them silently.

**A single-writer volume is byre's contract, not the kernel's.** A
volume can declare `sharing = "exclusive"`
([ADR 0054](https://github.com/pjlsergeant/byre/blob/main/docs/adr/0054-volume-sharing-exclusive.md)).
`byre develop` then refuses to start a second box of the project while
another one holds it, reading the live boxes' launch records to find
out, and refuses equally where it cannot establish that none is
holding it. What that does NOT cover: a `docker run -v` you type
yourself, anything already inside a running box, and a stopped box that
someone starts by hand while a launch is in flight. Exclusive is also
refused on a machine-scoped volume, because the boxes that could break
the contract there belong to your other projects, which byre's scan
does not see -- so cross-project data has no single-writer vocabulary
at all.

## Backups

**A backup is an unencrypted file of your project's state.** `byre backup`
writes one gzip tar holding the project's config and its state volumes
([ADR 0059](https://github.com/pjlsergeant/byre/blob/main/docs/adr/0059-box-backup.md)).
Each of the facts below is a limitation rather than a bug.

Confidentiality:

- The file is mode 0600, and that is the whole of the protection:
  whatever is plaintext inside a volume is plaintext in the file.
  Encrypt the file yourself if it is leaving your machine.
- `--no-credentials` is about the CONFIG file only: it drops the
  encrypted rows and the `[credentials]` block from the copy that
  travels, and says nothing about a volume's contents or about any other
  file you send alongside.
- **A crash byre cannot catch runs no cleanup.** SIGKILL or power loss
  can leave a staging directory under `~/.byre/staging` holding volume
  bytes, plaintext secrets included. byre cannot tell a crashed verb's
  leftovers from a concurrent verb's live staging, so the next backup
  or restore NAMES a stale one and leaves it for you.

Consistency:

- **Stillness is cooperative.** Backup refuses unless no container of
  the project exists on any installed engine, and holds the setup lock
  through the copy, but a `docker run` you type yourself can start a
  box during one, outside anything byre can serialize.
- **An engine you tell byre to ignore is not checked.** `--ignore-podman`
  (or `--ignore-docker`) skips that engine entirely, so a box of the
  project there is not ruled out and its volumes are not in the file;
  the preview and the summary both state this.
- **A shared layer can change while a backup runs.** Named layers have
  their own lock and the project's setup lock does not cover them, so
  the one resolution the file records can be stale by the last payload.

Recovery:

- **The file's authorship is not proven.** The per-payload sha256
  detects corruption, not who wrote it, and the restore review says so
  in those words.
- **A restored agent login shares its rotating token with the source
  box**, which is
  [ADR 0007](https://github.com/pjlsergeant/byre/blob/main/docs/adr/0007-no-credential-seeding.md)'s
  observed failure (the first refresh on either side logs the other
  out) and is your call to make -- byre neither warns nor blocks.
- **Restore reads only the engine it is restoring to**, so a same-named
  project volume on another installed engine is not consulted and not
  reported.
- **A project enrolled after restore's ownership re-check is outside
  it.** That re-check, taken under the lock, is the cross-project
  boundary, and it is a moment rather than a hold on every other
  project's store.
- **A helper container that outlives an engine outage is named, not
  removed.** Backup, restore, develop, `reset`, `forget`, `rehome` and
  the config editor's volume Clear each refuse while one is there,
  printing its `rm -f` line, so it is never silent, but removing it is
  yours.

## Agents hold usable credentials

**Agents hold usable credentials by construction.** Whatever auth story
you choose, the agent can read its own credential -- it needs it to
work. byre's job is that the credential's scope is what you chose, its
location is legible, and nothing you did not choose rides along.

## Reporting

Report security issues via GitHub security advisories on
[pjlsergeant/byre](https://github.com/pjlsergeant/byre/security) -- the
policy lives in
[docs/SECURITY.md](https://github.com/pjlsergeant/byre/blob/main/docs/SECURITY.md).
