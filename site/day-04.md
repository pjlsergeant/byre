---
title: "Day 4: where do the worktrees go?"
---

# Day 4: where do the worktrees go?

> This entry was suggested and drafted by the agent working on byre, but then completely rewritten by a human, following the approximate structure the agent had started with and preserving a couple of sentences

Today's feature was `byre worktree`. We limit you to one agent in a folder at a time so they don't end up fighting (although you can easily `byre shell` into the same container), but if you have multiple tasks on a git repo, you'll want to use git worktrees.

_byre_ now treats worktrees as a first-class citizen. It will do the linking up of the appropriate git assets for you if it detects you're in one, and inherit the parent repo's config, images, and volumes, so a second agent session there is already logged in and runs side by side with the first.

I'm really trying to keep the level of magic as low as possible, so anything you can do with _byre_ you can also do the hard way. In practice that means you can either go to a target folder and do `git worktree add` then `byre develop` **OR** you can just type `byre worktree <name>` and have it do the same under the hood.

## Where do they live tho

The agent decided to create worktrees as siblings: repo at `~/dev/byre`, run `byre worktree feat`, get
`~/dev/byre-feat`. That's the common git convention, it's on the same filesystem and it's easy to find and reasonable, but...

But it nagged at me, because it's byre reaching out and *creating a directory on your machine in a place it chose for you*. That's a small thing, but it's exactly the kind of small thing byre is supposed to not do. The whole pitch is that the box only touches what you grant it. A command that scatters checkouts into your filesystem by default is the same over-helpful instinct, just pointed at your home directory instead of your container.

My first idea was just to use `/tmp`, but the agent wasn't happy: a worktree isn't scratch space, it holds a real branch and possibly hours of uncommitted work, and `/tmp` is exactly where a reboot or a reaper will eat it. That's fair enough, and that would be infuriating. Hiding it in `~/.byre/` is a possibility, but feels like ugly hidden state. So ... let's just refuse to guess.

If there's no `--path` passed in and, no user-configured location for the machine, then don't invent one -- stop, and tell the user they need to choose, but make it really really easy to choose. Least surprise beats the minor convenience from guessing. We tell the user it's not configured, and point them at the configuration command. Possibly we should instead drop them into the configuration GUI (like we do with `byre develop`) but this feels like a more consequential choice, worth interrupting the user for.

## Positioning

Separately, I've been spamming friends and neighbours to try and get the "what does byre _do_" message nailed.

> Is it very clear what this is? Do you need it? If not, what are you doing/using instead? I’ve struggled to get the framing right for a tool I currently spend all day, every day using, and really trying to nail the what and the why of it.

There's a whole README-in-waiting in the repo, as I wait for more feedback, but I think the strongest push I've had was being asked to give a one-sentence "why would I use this":

> If you want to regularly run agents with --dangerously-skips-permissions in many different folders, but don’t trust the agent not to run git push as you, or not to go digging around in other folders

