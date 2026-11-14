# example.custom_checker

Separately built, text-only addon checker for an `EXTENSION` module/task. Build:

```bash
go build -o bin/plugin .
```

The manifest declares one versioned checker operation (`checkers[].key`,
`extensionKey`, route, board permission and timeout). Core remains the owner of
the verdict, points, audit and idempotency. Accepted text always awards the
task's frozen points; the plugin's `score` field is never trusted. The plugin
does not execute student code.

Fixtures: `BENIGN_OK` (accepted), other text (rejected), `MALICIOUS_SCORE`
(out-of-bounds score, rejected by core), `TIMEOUT` (honest timeout failure).
