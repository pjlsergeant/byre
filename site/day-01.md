---
title: "Day 1: I use byre to write byre"
---

# Day 1: I use byre to write byre

`byre` is a tool for spinning up quick agent sandboxes, using Docker or Podman, and as of today it's good enough that I build it with itself. It's a harness in which you can (hopefully) let your agent run wild in YOLO mode.

They're not (yet) completely foolproof sandboxes -- right now a box is basically isolated to a directory you choose but there's no network isolation -- but the idea is that you can go to any directory you have checked out and type:

`byre develop`

and have that directory mounted somewhere you're happy to give an agent full access. One of the sub-aims of the project is to enable my preferred programming harness: a setup where Claude does the development and Codex reviews it, in a loop.

The first iteration of this was [moarcode](https://github.com/pjlsergeant/moarcode), built specifically around that harness and mostly about the Claude/Codex connection. `byre` is the more generic version -- just the nice Docker or Podman sandbox, with that programming loop available as an installable skill rather than baked in.

I'm writing this down because I've given myself 30 days to get byre from "works for me" to something I'm proud to share. It's vibe-coded Go, and I'm not there yet on the quality of either the code or the project. I designed and understand the architecture, but there are a few places where I let the agent do all the work (in a review loop, but still), and I want to be crystal clear I'm happy with what it's doing before I suggest it's in any way done.

What's done at the moment:

* It works. `byre develop` is how I am doing all personal and professional programming day to day.

* The whole thing is low-magic. `byre` ends up generating and using Dockerfiles based on your configuration. You can easily see the Dockerfile that gets created, and reason about it, and there are escape hatches in both directions: you can embed literal Dockerfile lines in your byre configs, and you can decide you've had enough byre by one-off generating a Dockerfile and switching to just using that.

* Host/container user-permissions are very fiddly, but also almost entirely transparent.

* You can re-use templates and various base-images: if you have an environment that you usually use for Node, it's ready to go. The system knows how to provision Claude, Codex, and Gemini in your container out-of-the-box. By default, for Node, intelligent choices have been made about node_modules on the host vs the container, and I hope to build out other stacks with sensible choices.

In-progress:

* The TUI kinda configurator works, but is bad -- it should be a few seconds' work to add a new package to your environment, temporarily mount another project as read-only, and so on.

* Worktree support is mostly there, but needs some more thought on how to make it work really nicely.

* I don't yet have a good mental picture of how the host/container user-permissions work. They do, but there's a lot happening behind the scenes to make it transparent that I currently can't fully reason about, and I hate that -- I don't think it's right to stand behind code you didn't write if you don't fully understand the reason for everything it's doing

* I don't have a clear picture of how agent authentication and configuration should be shared between containers. There's some of that right now, but it's not _right_ -- there are sharp edges, and I wouldn't want to explain to someone why it's implemented the way it is.

* I need to work out the direction of travel with "skills" as a concept.

* There's not much of a roadmap written down. I have it working and running on my machine, and highly usable (for me!), but now it's time to work out what's needed to make it a project I'm proud of.
