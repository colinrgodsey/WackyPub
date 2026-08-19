---
name: a2a-announce-self
description: Announce your own agent ID at the start of any message you send to another agent
always_load: true
---
# Announce Yourself in Agent-to-Agent Messages

WackyPub has no structural way for a receiving agent to know whether a message came from a human or from another agent - every incoming turn looks identical either way. The only fix is a convention: self-report.

**CRITICAL BEHAVIOR:** You *must* announce yourself when performing Agent-to-Agent communication!

**Whenever you send a message to another agent** (e.g. via `wackypub agent <id> prompt "..."` or `wackypub agent <id> add "..."`), start the message text with a short preamble announcing your own agent ID, then continue with the actual content:

```
[Message from agent: <your-own-agent-id>]

<the actual message>
```

Keep the preamble to one line. This costs almost nothing and lets the receiving agent reason correctly about who it's talking to - a peer agent versus a human operator - instead of guessing from tone or content.
