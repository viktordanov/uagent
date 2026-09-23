# Memoria section IDs

Section IDs identify a concern within a README. Keep the ID stable when a heading changes.
Use the same ID for the same concern across packages.

| ID | Concern |
| --- | --- |
| `overview` | What the package provides and where it fits |
| `setup` | Construction, configuration, and dependencies |
| `usage` | Operations, inputs, and results |
| `contract` | Guarantees, limits, and caller responsibilities |
| `lifecycle` | Processes, cleanup, and shutdown |
| `testing` | How the package is tested and which fixtures it uses |

Use only the sections that help. Add a specific ID, such as `events`, when one topic needs its own review target.

Write the explanation first, then map it to the files that support it:

```markdown
<!-- memoria:section id="usage" files="runner.go decode.go" -->
## Run a request

Explain the operation and its result.
<!-- /memoria:section -->
```

Paths are literal, relative to the owning README, and must name files that README owns. Do not use globs.
Several sections can refer to the same file. A section starts with a Markdown heading; the `overview` marker can include the title.
