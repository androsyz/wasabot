# Agent file reference

An agent file defines the assistant: its name, an optional model, an optional default language, the
tools it may use, and the instructions the model receives. It is a text file, usually named `agent.md`.

## Location

`WASABOT_AGENT_FILE` holds the path. When it is empty, wasabot uses the agent built into the binary.
wasabot reads the file once, at startup.

## Structure

The file has two parts: the frontmatter, then the instructions.

```markdown
---
name: Support
model: gpt-4o-mini
language: id
tools: current_time
---
You are a support assistant for ...
```

## Frontmatter

The frontmatter is the block between the two lines that contain only `---`. The first line of the file
must be the opening `---`.

### Keys

| Key | Required | Value | Description |
|---|---|---|---|
| `name` | Yes | Text | The name of the agent, shown in the startup log. |
| `model` | No | Model name | The model this agent uses. Without it, `WASABOT_LLM_MODEL` applies. Model names depend on your provider. |
| `language` | No | Text, such as `id` or `English` | The default reply language. wasabot adds a line to the instructions that tells the model to use it unless the customer writes in another language. |
| `tools` | No | List | The tools the agent may use, written as `a, b` or `[a, b]`. |

### Rules

- Write one `key: value` pair per line.
- wasabot ignores blank lines and lines that start with `#`.
- Put a comment on its own line. Text after a value on the same line belongs to the value.
- Quote a value with single or double quotes if you need to keep leading or trailing spaces.
- Each key can appear once. wasabot rejects unknown and repeated keys, so a typo such as `modle` is an
  error and not a silent change.

## Instructions

The instructions are everything after the closing `---` line, with leading and trailing blank lines
removed. They must not be empty. They can contain markdown and lines of `---`.

## Tools

The agent can use only the tools listed in `tools`, and only tools that wasabot provides. An agent file
cannot add new tools.

| Tool | Arguments | Description |
|---|---|---|
| `current_time` | `timezone` (optional): an IANA name such as `Asia/Jakarta`. Default: UTC. | Returns the current date and time. |

## Startup errors

wasabot checks the agent file at startup and stops with one of these messages.

| The message contains | Cause | Fix |
|---|---|---|
| `to start the frontmatter` | The first line is not `---`. | Start the file with a `---` line. |
| `is not closed` | There is no closing `---` line. | Add a `---` line after the last key. |
| `expected "key: value"` | A frontmatter line has no colon. | Write the line as `key: value`, or start it with `#` to make it a comment. |
| `unknown key` | The frontmatter uses a key that does not exist. | Check the spelling against [Keys](#keys). |
| `duplicate key` | A key appears twice. | Remove one of them. |
| `name is required` | There is no `name`. | Add `name: ...`. |
| `prompt after the frontmatter is empty` | There are no instructions after the frontmatter. | Add the instructions. |
| `unknown tool` | `tools` names a tool that wasabot does not provide. | Use a name from [Tools](#tools). |
| `no model` | Neither the file nor `WASABOT_LLM_MODEL` sets a model. | Set `model` in the file or `WASABOT_LLM_MODEL`. |
| `read agent definition` | wasabot cannot read the file. | Check the path in `WASABOT_AGENT_FILE` and the file's permissions. |

## Examples

A minimal agent:

```markdown
---
name: Helper
---
Answer briefly and politely.
```

A complete sample with comments is in [`examples/agent.md`](../../examples/agent.md).
