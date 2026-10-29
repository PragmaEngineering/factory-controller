# Drive Factory from another agent

The [Factory operator skill](../skills/factory-operator/SKILL.md) lets a user invoke Factory from Codex, Claude Code, or another agent with skills and shell access. The external agent operates the CLI and reports progress; Factory remains responsible for running the configured workflow.

Install the `factory` binary and configure the project's `factory.json` first; see the [tutorial](../tutorial.md). The operator skill does not install workers, change models, or replace the agent packages selected by that file. It uses no worker MCP credentials. The calling agent needs permission to invoke the local CLI and read the relevant project/evidence files.

## Make the skill available

From the **controls checkout**, choose the agent you use. These links keep one canonical skill directory in this checkout; leave the checkout in place. If the destination already exists, inspect it instead of overwriting it.

Codex, for this user across projects:

```sh
mkdir -p "$HOME/.agents/skills"
ln -s "$PWD/skills/factory-operator" "$HOME/.agents/skills/factory-operator"
```

Claude Code, for this user across local projects:

```sh
mkdir -p "$HOME/.claude/skills"
ln -s "$PWD/skills/factory-operator" "$HOME/.claude/skills/factory-operator"
```

For project-only discovery, use `.agents/skills` or `.claude/skills` inside that project instead. Copying the complete `factory-operator` folder, including `references/` and `agents/`, also works if a symlink is unsuitable. No framework or registry checkout needs to live inside the application repository.

Discovery paths and invocation follow the official [Codex skill documentation](https://learn.chatgpt.com/docs/build-skills) and [Claude Code skill documentation](https://code.claude.com/docs/en/skills). Start a new agent session if the skill does not appear.

## Invoke it

In Codex:

```text
$factory-operator Run this project's configured factory using request.md.
Keep me informed, and pause for my decisions.
```

In Claude Code:

```text
/factory-operator Run this project's configured factory using request.md.
Keep me informed, and pause for my decisions.
```

Other useful requests:

- “Use factory-operator to explain execution <ID>'s current state and any blockers.”
- “Use factory-operator to send this feedback to the implementation agent in execution <ID>.”
- “Use factory-operator to answer the pending decision with my approval.”
- “Use factory-operator to investigate this blocked execution and propose recovery.”

The skill records execution/context IDs, follows status and output, delivers feedback to the selected agent session, and presents pending decisions. Human decisions stay with the user unless explicitly delegated. It rechecks requests before answering because the user may also act through the monitor.

This is an operator skill, not the runtime-injected `control-plane-comms` skill used by workflow agents. It is workflow agnostic: no particular stages, outputs, review roles, or software-development process are assumed. It does not create a new factory definition or launch an individual agent independently; single-agent factory configuration is a separate design discussion.
