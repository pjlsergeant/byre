---
title: "Day 3: mounting extra folders with byre config"
---

# Day 3: mounting extra folders with byre config

One goal for moving from moarcode to byre was being able to quickly and safely mount other code directories when I needed to give a given sandbox more context. This was possible with moarcode, but fiddly.

Today's work has been around making "byre config" -- byre's graphical configuration UI -- able to do this fast. byre config edits the underlying TOML configuration for a given dev directory, and that in turn controls the Dockerfile that's created to build the image (which you can see at any time with "byre dockerfile") and the build args.

Mounting new folders is now very easy:

<link rel="stylesheet" href="https://cdn.jsdelivr.net/npm/asciinema-player@3.8.0/dist/bundle/asciinema-player.css" />
<div id="demo-cast"></div>
<script src="https://cdn.jsdelivr.net/npm/asciinema-player@3.8.0/dist/bundle/asciinema-player.min.js"></script>
<script>
  AsciinemaPlayer.create(
    {{ '/day-03.cast' | relative_url | jsonify }},
    document.getElementById('demo-cast'),
    { fit: 'width', idleTimeLimit: 2, theme: 'asciinema' }
  );
</script>
