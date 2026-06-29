
`byre` is a tool for spinning up quick agent sandboxes, using Docker or Podman. They're not foolproof sandboxes -- right now they're basically just isolated to a directory that you choose -- but the idea is that you can go to any directory you have checked out, and type:

`byre develop`

and have that directory mounted in a place you're happy to give an agent full-access to. One of the sub-aims of the project is to enable my preferred programming harness, which is a setup where Claude is doing the development, and Codex is reviewing it, in a loop.

The first iteration of this project was `moarcode`, which was built specifically around that harness, and was mostly about the Claude/Codex connection. `byre` is meant to be a slightly more generic version of that, just the nice Docker or Podman sandbox, but with the programming loop described above as an installable skill into `byre`.

Today, `byre` is working. I use byre to write byre. It's vibe-coded Go, and I'm not yet happy with the quality of the code or the project, but I want to get it to a point where I'm proud to share it in the next 30 days. I fundamentally designed and understand the architecture of it, but there are a few places in it where I let the agent do all the work (in a review loop, but still) and want to be crystal clear that I'm happy with what it's doing before I suggest it's in any way done.

What's done at the moment:

* It works. `byre develop` is how I am doing all personal and professional programming day to day

* The whole thing is low-magic. `byre` ends up generating and using Dockerfiles based on your configuration. You can easily see the Dockerfile that gets created, and reason about it, and there are escape hatches in both directions: you can embed literal Dockerfile lines in your byre configs, and you can decide you've had enough byre by one-off generating a Dockerfile, and switching to just using that

* Host/container user-permissions are very fiddly, but also almost entirely transparent

* You can re-use templates and various base-images: if you have an environment that you usually use for Node, it's ready to go. The system knows how to provision Claude, Codex, and Gemini in your container out-of-the-box. By default, for Node, intelligent choices have been made about node_modules on the host vs the container, and I hope to build out other stacks with sensible choices

In-progress:

* There's a really shitty TUI configurator, that should in fact be really good, and will save a lot of time. I want it to be a few seconds work to add a new package to your environment, temporarily mount another project as read-only and so on

* Worktree support is mostly there, but needs some more thought on how to make it work really nicely

* I don't have a good mental picture on how the host/container user-permissions work. They do, but there's a lot of behind the scenes work going on to make it transparent that I am currently not able to reason about, and I hate that

* I don't have a clear picture on how agent authentication and configuration should get shared between different containers. There's some of that right now, but it's not _right_, there are sharp edges, I wouldn't want to explain to someone how it's implemented

* I need to work out the direction of travel with "skills" as a concept

* There's not much of a roadmap written down. I have it working and running on my machine, and highly usable (for me!) but now it's time to work out what's needed to make it a project I'm proud of

