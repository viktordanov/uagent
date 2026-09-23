# Writing uagent documentation

Describe uagent directly, in a neutral third-person voice: "uagent runs…" and "The harness stops…".
Address the reader directly only in instructions.

## Start with the reader

uagent is a wrapper around unreal-agent-runner that adds guards and ergonomics while staying true to the runner. Keep that framing visible.
The root README is written for someone who wants to run tasks: why the wrapper exists, how to install and run it, what it adds, and how to use it from Go.
Keep architecture to the short notes in `docs/documentation/architecture.md`; the root README does not catalog packages.
Other READMEs exist only where a reader needs a contract of its own: `stream` for programs that read the event stream, and `testing` for the fixtures and fake runner.
Add a vertical, numbered list of contents near the top when a README has more than two sections.
Use a table only when the reader is choosing between alternatives or looking up a value, such as an exit code or an event type.
Facts about unreal-agent-runner behavior carry the runner version they were checked against.

## Diagrams

Use a Mermaid diagram only where it explains a flow better than prose. Keep it top-to-bottom with default styling.
Show one relationship or flow per diagram.

## Keep maintenance out of the introduction

Follow `docs/documentation/memoria.md` for review mechanics.
Use section mappings as review hints, never as proof that other prose is unaffected.
Exported summaries are one or two sentences, contain no relative links, and read well out of context.
