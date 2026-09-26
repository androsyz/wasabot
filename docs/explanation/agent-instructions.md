# How agent instructions work

This page explains what the model receives, so you can decide what belongs in your instructions. For the
file format, see the [agent file reference](../reference/agent-file.md).

## What the model receives

For every customer message, wasabot sends the model three kinds of input, each as a separate message:

1. **Your instructions**, from the agent file, as the system message. If you set `language`, wasabot
   appends one line about the default language.
2. **The conversation**: recent messages of that chat, the customer's and the assistant's.
3. **Tool results**, when the model called a tool.

wasabot never adds customer text to your instructions. A customer writes only in the conversation part.

## What you control, and what you don't

You control what the model is told and which tools it can use. That gives you three guarantees:

- The model can use only the tools you list. A customer cannot make it use another one.
- Customer text cannot edit your instructions, because it never becomes part of them.
- A tool that fails returns its error to the model as text, so one failed tool does not stop the reply.

You do not control what the model does with a customer's message. A model can still be persuaded by
clever wording in the conversation, so treat your instructions as guidance and not as a security
boundary. Do not put secrets in them, and enable only tools that are safe to run when a customer asks.

## Writing the instructions

wasabot adds nothing special to your instructions, so the guidance from your model provider applies.
Look for its prompting guide.

Two things are specific to WhatsApp:

- Customers read replies on a phone. Ask for short, plain text without markdown.
- The model sees only recent messages of a chat, not its whole history. Put facts that must always be
  available, such as opening hours, in the instructions and not in earlier messages.
