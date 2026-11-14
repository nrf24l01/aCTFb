# Attack+Defense examples

These files are safe, isolated fixtures. Participant code is never executed on
the host; only the trusted checker image runs inside the worker's Docker
sandbox with read-only sanitized checkouts.

- `checker/` — example trusted checker image (pinned Python base digest).
  Build it, publish to your registry, pin the resulting manifest digest, and add
  that exact `name@sha256:<64>` string to `ACTFB_AD_RUNNER_IMAGES`.
- `fixtures/source/` — deliberately vulnerable toy `app.py` (header trust bug).
- `fixtures/source-patched/` — fixed toy source (same API).
- `fixtures/exploit/` — exploit and patch test scripts. The patch script
  reports both `securityFixed` and `regressionPassed`; a PATCH run only awards
  points when both are true and the server's configured points are used.
- `local-gitea/run-acceptance.sh` — starts a unique ephemeral Gitea container
  (pinned official digest, no persistent volume), generates ephemeral admin/PAT
  credentials, creates real private repositories, pushes commits, registers a
  real webhook and runs the opt-in acceptance test.
- `local-checker/run-acceptance.sh` — builds the example checker, publishes it
  to an ephemeral local registry, obtains the immutable manifest digest and runs
  the real Docker isolation acceptance test.

These local scripts are test-only. Production requires a dedicated isolated
worker node, a trusted registry/allowlist, TLS webhook termination and operator
monitoring. They never read production secrets, kubeconfig or aCTFb integration
credentials.

Run from the repository root:

```bash
bash examples/attack-defense/local-gitea/run-acceptance.sh
bash examples/attack-defense/local-checker/run-acceptance.sh
```

See `docs/attack-defense.md` for the full contract and the required boot/events
dispatcher wiring.