# Ejecting

byre is a transparent layer over Docker; leaving is two commands.

```sh
byre dockerfile > Dockerfile   # the image: base, packages, skills' build output, your files
byre dockerrun                 # the exact run command: mounts, env, volumes, ports, args
```

Build the one, run the other, and you have byre's box without byre --
nothing in the image or the run command depends on byre existing.

**The exceptions are the two things byre does from OUTSIDE the box at
launch.** Both make the box fail closed without them, so the printed
command carries the gate and the run stops at it rather than starting
subtly wrong.

**The firewall.** Its rules are applied from outside the box at launch
(`docs/adr/0010`), so no Dockerfile or run command can carry them -- and
a firewalled image refuses to start without byre's ready signal (it
fails closed rather than launching unwalled). Two ways out:

```sh
byre ejectfirewall > firewall.sh   # the netns helper byre runs, as a script:
                                   # start the box, then ./firewall.sh <container>
```

or disable the firewall skill and regenerate -- bring your own firewall.

**Credentials.** A project that declares credential rows has byre
decrypt them on the host and stream them into the started box, which no
run command can do. The printed command carries the gate -- the
`BYRE_CRED_EXPECT` flag and the `/run/byre` tmpfs the values land on --
so an ejected box waits and then exits instead of running without the
values its config declares. There is no eject for the delivery itself:
it needs the passphrase, and that is `byre develop`. To run without
them, drop the `-e BYRE_CRED_EXPECT` and the `/run/byre` tmpfs from the
command. If byre cannot read a declared row at all (a reserved key, an
unreadable `[credentials]` identity), `byre dockerrun` prints no command
and says why: without the gate the line would launch a box that declares
credentials and waits for none. `byre develop` refuses the same config.
