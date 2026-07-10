# Code review loop (codereview)

byre placed this guidance here; it applies to every session in this box.

## Run a review after each feature or fix

This box ships `byre-codereview` — an independent reviewer (Codex by default;
`--reviewer grok|claude` or `BYRE_REVIEWER=...` picks another installed one).
After completing any feature or fix, run it yourself and act on the findings;
don't ask permission first.

```sh
byre-codereview                       # review the current changes
byre-codereview "auth error handling" # focus the review
byre-codereview --continue "..."      # re-check after fixes (resumes the session)
byre-codereview --reviewer grok "..." # second opinion from grok instead
```

Prefer a reviewer that ISN'T the model driving this session: same-model review
is a second pass, not a second opinion. `--reviewer claude` is there for boxes
where claude is the only CLI.

The loop: run it → read every finding → for each, fix it or note why you're
leaving it → if you changed anything, re-run with `--continue` → stop only when
clean or all remaining items are consciously deferred. Findings are also
appended to `.byre-devlog/reviews.md`. Reviewers may run cheap read-only probes
to back up findings but never your test suite — green stays YOUR job — and must
not touch the tree; the script warns if the working tree changed during a
review. The reviewer needs to be logged in once per box (`codex login
--device-auth` / `grok login --device-auth` — both CLIs' plain `login` starts a
browser-redirect flow that cannot complete in a no-browser sandbox); if
`byre-codereview` reports an authentication failure, do that first.
