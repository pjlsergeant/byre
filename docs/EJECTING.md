# Ejecting

byre is a transparent layer over Docker; leaving is two commands.

```sh
byre dockerfile > Dockerfile   # the image: base, packages, skills' build output, your files
byre dockerrun                 # the exact run command: mounts, env, volumes, ports, args
```

Build the one, run the other, and you have byre's box without byre --
nothing in the image or the run command depends on byre existing.

**The one exception is the firewall.** Its rules are applied from
*outside* the box at launch (`docs/adr/0010`), so no Dockerfile or run
command can carry them -- and a firewalled image refuses to start
without byre's ready signal (it fails closed rather than launching
unwalled). To eject a firewalled project: disable the firewall skill,
regenerate, and bring your own firewall.
