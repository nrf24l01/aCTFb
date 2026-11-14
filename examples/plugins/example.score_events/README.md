# example.score_events

Separately built aCTFb addon. It receives `submission.judged` through the core JetStream consumer, stores only
plugin-owned deduplicated statistics in its private schema and never mutates core verdicts or scores.

```bash
go build -o bin/plugin .
```

Copy this directory under `ACTFB_PLUGINS_DIR/incoming/score-events`, install with
`core.boards.read` + `core.events.subscribe` approvals, enable it and grant its board permissions explicitly.
See `docs/plugin-system.md` for the event retry/DLQ/replay contract.
