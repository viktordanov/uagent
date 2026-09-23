# Maintain the documentation with Memoria

[Memoria](https://github.com/viktordanov/rs-memoria) connects each README to the files it owns and reports when a README needs review.
This procedure uses Memoria 0.6.0. The managed agent skill (`memoria integrations skill install --target claude`) contains the full review contract.

## When CI fails

The [Memoria workflow](../../.github/workflows/memoria.yml) runs `memoria check` on every push and pull request.
CI never reviews or acknowledges. When it fails, review the pending READMEs locally and commit the updated `memoria.lock`.

## Review loop

Run every command from the repository root. Keep review artifacts outside the repository, or they become review inputs.

```sh
mkdir -p /tmp/uagent-memoria
memoria review --format json | jq '.data.next_action'
```

1. If the next action is `render`, run `memoria render <README>` and read the plan again.
2. If it is `review`, save the manifest: `memoria review <README> --format json > /tmp/uagent-memoria/<name>.json`.
3. Read `memoria guidance <README>`, the review mode, and every covered reason.
4. Read the sources the mode requires, then the whole README. `full_baseline` means every owned source.
5. Edit the prose if it no longer matches the code. After any edit, save a fresh manifest.
6. Acknowledge the exact manifest and token:

```sh
memoria ack <README> \
  --packet /tmp/uagent-memoria/<name>.json \
  --token <data.token> \
  --reviewer <your-label> \
  --result updated \
  --note "<what you verified against this revision>"
```

Repeat until the plan is empty, then run `memoria check`. Review providers before consumers: the root README imports the `stream` and `testing` summaries, so it comes last.
Never edit an import body by hand, never edit `memoria.lock`, and never acknowledge when the plan is empty.

## Adding a README

Add a README only for a reader who needs a contract of its own. Give it a `summary` export, import that summary where the root README mentions the topic, and run `memoria render README.md`.
Then follow the review loop.
