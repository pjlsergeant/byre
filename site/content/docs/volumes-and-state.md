---
title: Volumes & state
weight: 70
description: what survives a rebuild, and what's disposable
---

**Cache** volumes (`node_modules`, …) are disposable. **State** volumes
(`.claude`, …) hold the agent's login and history, per project, and
survive rebuilds. **Machine** volumes let you share volumes between
different byre boxes. byre never reads or copies host credentials; nothing
crosses unless you enable it, and what you enable, `byre status` shows.

By default agents log in once per project, inside the box, and maintain
their own context. See [How do I…?](../how-do-i/) for enabling shared LLM
credentials.
