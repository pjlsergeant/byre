# Review brief -- the prompt behind the 2026-07-27 four-way review

**Status: KEPT, re-runnable.** This is the brief given verbatim to all four
reviewers (codex, grok, Opus 5, Fable 5). Results: `review-findings.md` (the
bugs) and `philosophical-problems.md` (the doctrine criticism). Kept because
re-running it against a later tree is the natural way to check whether the
structural criticism was addressed or just papered over.

How it was run: the two CLI reviewers via
`byre-codereview --reviewer codex|grok --raw "$(cat this-file)"` (they
self-append to `.byre-devlog/reviews.md`); the two model reviewers as subagents
given the same text plus "do not look at git history or .byre-devlog" (nothing
archives those automatically -- their output has to be written out by hand).

---

You are doing a from-scratch review of an entire open-source repository
maintained by a single author. Nobody has ever reviewed this code before —
you are the first second pair of eyes it has had. Review it the way a senior
engineer would if a friend asked "before I depend on this / before I take
this project over, tell me what you see."

Assume linters and formatters exist. Do not report style, formatting, or
trivial issues. Your job is judgment.

IMPORTANT CONTEXT: This codebase is unusually thorough about documenting its
reasoning. There is substantial developer documentation explaining why things
are the way they are. This raises the bar for you in both directions:

- Before flagging ANY design decision as questionable, search the docs and
  comments for the stated rationale. If you find one, evaluate it on its
  merits: does the reasoning actually hold? Is it still valid, or does it
  rest on an assumption the code has since outgrown? "The author explained
  this and the explanation is sound" is not a finding. "The author explained
  this and the explanation doesn't survive scrutiny" is one of the most
  valuable findings you can produce — quote the rationale and say precisely
  where it fails.
- Conversely, in a codebase this deliberate, an odd decision with NO
  documented reason is itself a signal. In a sloppy repo, silence means
  nothing; here it means either an oversight or reasoning that never got
  written down. Flag it as: "This is unusual, the codebase normally explains
  its choices, and I found no explanation for this one."
- Treat the docs as part of the reviewed artifact. Flag places where the
  documentation and the code have drifted apart — a documented invariant the
  code no longer upholds, or behavior the docs don't acknowledge. In a
  reasoning-heavy codebase, doc rot is worse than in others, because readers
  will trust the docs.

STEP 0 — INTENT. From the README, developer docs, examples, and code, state
what this project is for, who its users are, and what its implicit promises
are (stability? correctness? security?). If the docs and the code disagree
about any of this, that's a finding. Review everything else against these
promises. Note: performance is explicitly NOT a priority for this codebase —
do not raise performance findings unless something is so pathological it
breaks the project's actual promises.

Then prioritize, in order:

1. REPEATED BLIND SPOTS. A single author's mistakes are systematic, not
   random. If you find one instance of a flawed pattern — an unchecked error,
   a missing input validation, a subtle misuse of an API — actively search
   for every other instance before moving on. Report it as "this pattern
   appears in N places," not as N separate comments. Documentation habits are
   included: if a category of decision is consistently under-explained
   relative to the rest of the repo, that asymmetry is worth naming.

2. REASONING THAT DOESN'T HOLD. Beyond individual rationales, look for load-
   bearing assumptions stated in the docs that the rest of the design depends
   on. If one of those is wrong or outdated, everything built on it inherits
   the problem — this is the highest-consequence category in a codebase that
   builds carefully on its own stated reasoning.

3. ABSENCES. What should exist and doesn't? Focus on what one person building
   alone tends to skip: tests for failure paths rather than happy paths,
   input validation at trust boundaries, handling for the concurrent case,
   upgrade paths between versions. Given how much IS documented here, an
   undocumented invariant the code silently depends on is both more
   surprising and more dangerous than usual — contributors will assume that
   if it mattered, it would be written down.

4. TRUST BOUNDARIES AND SECURITY. This is public code that strangers will run
   on inputs the author never imagined. For every point where external data
   enters — files, network, env vars, CLI args, deserialization — ask what a
   hostile or malformed input does. Check dependencies: unmaintained, stale-
   pinned, or unnecessary. Single-maintainer projects rarely get security
   review; assume you are it.

5. THE PUBLIC CONTRACT. Identify the actual public API surface — what
   downstream users can reach, not what the author intends them to use.
   Flag: accidentally-public internals, undocumented behavior users will
   depend on anyway (Hyrum's Law), and anything where the docs' description
   of the contract and the code's actual contract differ.

Rules of engagement:
- Rank findings by consequence. A short review with the five things that
  matter beats an exhaustive one. If a subsystem is genuinely solid — and in
  particular if its documented reasoning is sound — say so in one line. The
  author has never heard that from anyone.
- For each finding: confidence level, what evidence would change your mind,
  and — critically — confirmation that you checked the docs for a rationale
  first. A finding that ignores an existing explanation is worse than no
  finding; it tells the author you didn't read their work.
- Distinguish "wrong" from "different than I'd do it." Documented taste is
  taste. Only flag idiosyncrasy when it has a concrete cost the docs don't
  acknowledge.
- End with "Questions for the maintainer" — but hold this section to a
  higher standard than usual. Most questions you'd normally ask are answered
  in the docs; check first. What remains should be genuine gaps: decisions
  the documentation doesn't cover, or places where you need to know whether
  an absence is deliberate scope or oversight.

Make sure not to look at the git history or .byre-devlog — take the codebase
as it is.
