# The agent is a skill

byre ships agent skills for Claude, Codex, and Gemini; the `agent` config
scalar selects which one the constant launcher execs, and setting it
implicitly enables that skill. An agent skill contributes its CLI (build),
its launch command + autonomy flag, and its auth state volume -- through
the same skill mechanisms as everything else. "A constant entrypoint
launches a variable agent" is the trick that keeps the chassis
agent-agnostic.

Follows from PRINCIPLES.md #2 (core ships no opinions): which agent to
run is the biggest opinion byre has, so it cannot live in core. Multiple
agent skills can be enabled at once (both CLIs installed); `agent`
decides the default command.
