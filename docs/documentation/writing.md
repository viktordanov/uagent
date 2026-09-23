# Writing uagent documentation

Describe uagent and its packages directly, in a neutral third-person voice: "uagent runs…" and "The runner package drives…".
Address the reader directly only in instructions.

## Start with the reader

A package README explains what the package provides, how the rest of uagent uses it, and the contract a change must keep.
Start with a plain definition that works for a reader who has not opened the code.
Add a vertical, numbered list of contents near the top when the README has more than two sections.
Keep the detailed contract in the same README. Do not split a package into a short README and a separate reference page.
Use a table only when the reader is choosing between alternatives or looking up a value, such as an exit code or an event type.

## Where things belong

The root README introduces uagent: what it wraps, how to install and run it, its guards, and a catalog of packages.
Package READMEs hold package detail. The root catalog imports each package's `summary` export instead of restating it.
The `stream` README is the contract for programs that drive uagent. Keep every event type and field there.
Facts about unreal-agent-runner behavior live in the `runner` README, with the runner version they were checked against.

## Architecture vocabulary

`domain` is pure: entities, events, the run service, and ports. It imports only the standard library and `uuid`.
Adapters (`runner`, `preflight`, `runstore`, `stream`, `render`, `statedir`) implement ports or transform domain types for a boundary.
`cmd/uagent` is the composition root. Name these layers consistently across READMEs.

## Diagrams

Use a Mermaid diagram only where it explains a flow better than prose. Keep it top-to-bottom with default styling.
Show one relationship or flow per diagram.

## Keep maintenance out of the introduction

Follow `docs/documentation/memoria.md` for review mechanics.
Use section mappings as review hints, never as proof that other prose is unaffected.
Exported summaries are one or two sentences, contain no relative links, and read well out of context.
