# aCTFb operator bundle

This directory contains the prebuilt GHCR deployment files. No Git clone, Go, Bun, source build, or Compose
`build:` section is required.

| File | Purpose |
| --- | --- |
| `compose.yaml` | Backend, frontend, PostgreSQL, Redis, NATS, retention, events-dispatcher, and optional profiles. |
| `config.yaml` | Application settings and secrets: JWT, cookies/origins, flags, keys, limits, retention. |
| `.env.example` | Datasource connections/secrets and Docker-specific settings only. |
| `realip.conf` | Optional empty real-IP include for an external nginx edge; not mounted by default. |

Requirements: Docker Engine with Compose v2, `linux/amd64`, `wget`, and `openssl`.

## Deployment

Follow the root [deployment guide](https://github.com/nrf24l01/aCTFb#deployment) to download the three required
files, generate secrets, set the exact origin, and start the stack. The full setting reference is
[docs/configuration.md](https://github.com/nrf24l01/aCTFb/blob/main/docs/configuration.md).
Advanced TLS, profiles, roles, backup, and limitations are in
[docs/production.md](https://github.com/nrf24l01/aCTFb/blob/main/docs/production.md).

Short start/status commands after `.env` and `config.yaml` are configured:

```bash
docker compose up -d
docker compose ps
curl -fsS "http://127.0.0.1:${HOST_PORT:-8080}/healthz"
docker compose exec -T backend curl -fsS http://127.0.0.1:8080/ready
```

## File handling

- Run from a private `0700` deployment directory. `config.yaml` may stay `0644` inside that directory; the parent
  blocks other host users. The container UID `10001` only needs read access. If hardening with a managed host
  group, `0640` group `10001` is the advanced alternative.
- `.env` contains datasource/Docker values. Keep it `0600`.
- Do not overwrite a configured `.env` or `config.yaml` with the downloaded template during updates.
- Never run `docker compose down -v` unless you intend to delete PostgreSQL/Redis/NATS/plugin volumes.

First admin promotion is operator SQL, not an automatic seed. See the root
[first-admin section](https://github.com/nrf24l01/aCTFb#first-admin). Optional external-edge real-IP trust is
covered in the root guide and `docs/production.md`.