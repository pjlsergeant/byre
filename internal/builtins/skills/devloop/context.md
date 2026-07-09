# Dev workflow (devloop)

byre placed this guidance here; it applies to every session in this box. A
project may add its own `CLAUDE.md` in the repo on top of it.

## Your scratch dir: `.devloop/`
Persistent working files live in `.devloop/` at the repo root. It is
**self-ignoring** (a `.gitignore` of `*` is created for you), so nothing in it
is ever committed and you never need to touch the project's own `.gitignore`.
- `.devloop/DIARY.md` — your progress diary (see below).
- `.devloop/reviews.md` — the code-review log, appended by `byre-codereview`.

## Your persistent stash: `~/scratch` (`$BYRE_SCRATCH`)
The container filesystem — including `/tmp` — is thrown away on every container
restart and rebuild. `~/scratch` is a named volume that survives both. Stash
anything there that should outlive the current container: experiment output,
downloaded artifacts, notes-to-self across a sandbox rebuild (e.g. a
`--self-edit` restart). Unlike `.devloop/`, it lives outside the repo — use it
for files that aren't tied to the working tree. It's per-project and shared
with concurrent sessions in worktrees of the same repo, so use a subdirectory
if you might collide.

## Diary discipline
Keep a running diary at `.devloop/DIARY.md`. **Read it at the start of each
session** to recover context, and **update it when you finish**: what you did,
decisions made and why, surprises, and what's next. It's your memory across
sessions — keep it concise and current.

## Autonomy
Keep going — work through the task without stopping to ask "should I continue?"
after each step. Stop and ask only when you're genuinely blocked: the same fix
has failed 2–3 times, the approach is ambiguous, or you'd be guessing.

## Commit discipline
Commit after each coherent unit of work (a function + its tests, a bug fix, a
green refactor). If `git status` shows more than a handful of changed files,
you've waited too long — commit now. Write clear messages describing the *why*.

**Do NOT add `Co-Authored-By`, `Signed-off-by`, or any other commit trailer
unless the user explicitly asks.** Commits are attributed to the developer via
the host git identity byre passes into the box; adding co-sign trailers without
permission misrepresents who authored the work.

## Code review loop (mandatory after a feature or fix)
This box ships `byre-codereview` — an independent reviewer (Codex by default;
`--reviewer grok` or `BYRE_REVIEWER=grok` when the grok skill is enabled).
After completing any feature or fix, run it yourself and act on the findings;
don't ask permission first.

```sh
byre-codereview                       # review the current changes
byre-codereview "auth error handling" # focus the review
byre-codereview --continue "..."      # re-check after fixes (resumes the session)
byre-codereview --reviewer grok "..." # second opinion from grok instead
```

The loop: run it → read every finding → for each, fix it or note why you're
leaving it → if you changed anything, re-run with `--continue` → stop only when
clean or all remaining items are consciously deferred. Findings are also appended
to `.devloop/reviews.md`. Reviewers may run cheap read-only probes to back up
findings but never your test suite — green stays YOUR job — and must not touch
the tree; the script warns if the working tree changed during a review.
The reviewer needs to be logged in once per box (`codex login --device-auth` /
`grok login --device-auth` — both CLIs' plain `login` starts a browser-redirect
flow that cannot complete in a no-browser sandbox); if `byre-codereview`
reports an authentication failure, do that first.

## Before you commit
Keep the tree healthy: run the project's formatter, vet/lint, and tests, and get
them clean before committing. Never commit with failing tests or a broken build.
