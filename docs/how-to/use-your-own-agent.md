# Use your own agent

Give the assistant your own instructions instead of the built-in ones.

## Before you start

If wasabot has no LLM configured, the bot only echoes messages and ignores the agent file. Set
`WASABOT_LLM_API_KEY` first, or `WASABOT_LLM_BASE_URL` for a local server.

## Steps

1. Copy the sample to a place you control:

   ```bash
   cp examples/agent.md /etc/wasabot/agent.md
   ```

2. Open the copy and edit it. Change `name`, then replace the text after the second `---` line with your
   own instructions. The [agent file reference](../reference/agent-file.md) lists every option.

3. Set `WASABOT_AGENT_FILE` to the file's path:

   ```bash
   WASABOT_AGENT_FILE=/etc/wasabot/agent.md wasabot
   ```

   If you run wasabot in Docker, mount the file and set the variable to the path inside the container:

   ```bash
   docker run -v /etc/wasabot/agent.md:/config/agent.md -e WASABOT_AGENT_FILE=/config/agent.md ...
   ```

4. Restart wasabot. It reads the file only at startup.

5. Open the dashboard and check the **Active agent & model** column. It shows the file name and the
   model, for example `'agent.md' | gpt-4o-mini`.

## If wasabot does not start

If wasabot stops with a message about the agent file, find the message in
[Startup errors](../reference/agent-file.md#startup-errors) to see what to fix.

## Next

Read [How agent instructions work](../explanation/agent-instructions.md) before you write long
instructions.
