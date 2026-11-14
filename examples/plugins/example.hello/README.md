# example.hello

Separately built aCTFb addon used by the P5 acceptance test. It demonstrates a participant page, an admin settings
page, its own backend API, its own board-scoped permissions and a private PostgreSQL schema.

```bash
go build -o bin/plugin .
```

Copy this directory under `ACTFB_PLUGINS_DIR/incoming/hello`, validate it, install it with the approvals from
`manifest.yaml`, enable it, then grant `plugin.example.hello.view`/`manage` explicitly to a user for one board.
See `docs/plugin-developer-guide.md`.
