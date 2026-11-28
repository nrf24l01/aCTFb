# GHCR publication verification

Status: **published and verified from `main` at commit `91d76b6`** (student task progress: server-backed
task progress, solved-card tint and participant completion filters). Ten separate images are publicly
pullable without registry credentials, each with `latest` and an immutable `sha-<full-sha>` tag resolving
to the same digest.

## Current publication (`91d76b6`)

| Field | Value |
| --- | --- |
| Workflow run (push) | https://github.com/nrf24l01/aCTFb/actions/runs/46132838936 |
| Commit (image source) | `91d76b6a1a8a2c8f292fe6941d9d8472827049ed` |
| `main` HEAD at publication record | `91d76b6` — same commit; the docs update that records this publication touches only `docs/`, which the workflow path filter excludes, so no further run or image rebuild is expected |
| Conclusion | success 4/4 (Backend quality, Frontend quality, Integration, Publish images) |
| Run duration | 09:57:05Z → 10:20:45Z (~23.7 min) |
| Immutable tag | `sha-91d76b6a1a8a2c8f292fe6941d9d8472827049ed` |
| Platform | `linux/amd64` only (read from each image config) |
| Registry visibility | public; anonymous manifest + config-blob reads for all ten images (no stored credentials, no tokens printed) |
| `latest` vs `sha-*` | same manifest digest for every image, verified on all ten |
| `org.opencontainers.image.revision` | `91d76b6a1a8a2c8f292fe6941d9d8472827049ed` on all ten |
| `org.opencontainers.image.version` | `sha-91d76b6…` |
| Provenance attestations | none; single OCI image manifests |

| Image | Digest | Compressed |
| --- | --- | --- |
| `actfb-backend` | `sha256:7444b6858747691356cdbce5ab81994dd3933b63d5b01e9d3e9d72e9066d0928` | 20,458,709 B |
| `actfb-frontend` | `sha256:f98317a15cbb98b1c125f96a75feb0675974d260ec61dfeb15365059e2f9e36d` | 23,880,841 B |
| `actfb-notification` | `sha256:a05fa65942511d1f28dbe972629e62c2ebbb11bc354366561e56fdf5f6cc7382` | 12,342,732 B |
| `actfb-retention` | `sha256:1c4828b9e4d2f530abf941f297d4c40e76b2e92dc1abe8d6573faa2cf4c87d5a` | 10,940,563 B |
| `actfb-events-dispatcher` | `sha256:826206e8f14150ecc9e5ee80f52747b423f7db2e174e8991b8450862781cbd3d` | 12,001,243 B |
| `actfb-file-scanner` | `sha256:05f29280e4eaa15d033e4de680258296111078c49552d61ae9ac7feefe4f202d` | 13,359,289 B |
| `actfb-storage-init` | `sha256:230f5d01b7c9ffc9a1a45385bf7f1917ac8d1a5428486ae271990a4e64b07c62` | 10,956,257 B |
| `actfb-plugin-runner` | `sha256:64c9198171e4bb3b1d91b29dbbe6ee324f1f568c1a7b7beeb9a335085acd1a36` | 10,292,879 B |
| `actfb-ad-worker` | `sha256:ddaa11ea2f4a15ac85ab498320d72af8cf795158128b8543261e0148d80c1fef` | 27,023,897 B |
| `actfb-instance-worker` | `sha256:b335e044ee6e5050a6803f6c0f36067045519633215aa7b5aeff84e5ef3e8940` | 13,932,167 B |

Total compressed 155,188,577 B.

Verification method: each package was resolved over HTTPS against `ghcr.io` with an anonymous pull token
obtained per repository; the `latest` manifest digest was compared with the `sha-91d76b6…` manifest digest,
the config blob was read for `os`/`architecture` and the OCI labels, and layer sizes were summed. Raw
output: `/tmp/opencode/student-task-progress/publication/ghcr-verify.json` and `publication.json`. No
credential, cookie or token value is recorded here.

The frontend bundle was additionally checked by reading the published image's blobs anonymously (no
operator pull, no Docker daemon): 93 `js/css/html` entries were scanned and the progress UI shipped —
`Task progress`, `No solved tasks`, `No unsolved tasks`, the `completion`/`SOLVED`/`UNSOLVED` filter
values and `.task-card--solved{… color-mix(in srgb, var(--status-success-ink) 8%, black)}` with its 12%
hover variant. This is a static bundle check, not a deployed-app claim.

This publication ships the student task progress slice. **There is no migration (schema stays at v20), no
dependency, no `config.yaml` and no `.env` change** in this range and no breaking `/api/v2` change (the
progress fields and the optional `completion` filter are additive), so operators only need
`docker compose pull && docker compose up -d` in `deploy/`; nothing new is applied on boot and no new
secret is required.

## Previous publication (`3ac89c0`)

| Field | Value |
| --- | --- |
| Workflow run (push) | https://github.com/nrf24l01/aCTFb/actions/runs/45982934703 |
| Commit (image source) | `3ac89c04fa0f9c41a4263093f099fb9a0a9fa987` |
| `main` HEAD at publication record | `3ac89c0` — same commit; the docs update that records this publication touches only `docs/`, which the workflow path filter excludes, so no further run or image rebuild is expected |
| Conclusion | success 4/4 (Backend quality, Frontend quality, Integration, Publish images) |
| Run duration | 10:32:18Z → 10:57:49Z (~25.5 min) |
| Immutable tag | `sha-3ac89c04fa0f9c41a4263093f099fb9a0a9fa987` |
| Platform | `linux/amd64` only (read from each image config) |
| Registry visibility | public; anonymous manifest + config-blob reads for all ten images (no stored credentials, no tokens printed) |
| `latest` vs `sha-*` | same manifest digest for every image, verified on all ten |
| `org.opencontainers.image.revision` | `3ac89c04fa0f9c41a4263093f099fb9a0a9fa987` on all ten |
| `org.opencontainers.image.version` | `sha-3ac89c0…` |
| Provenance attestations | none; single OCI image manifests |

| Image | Digest | Compressed |
| --- | --- | --- |
| `actfb-backend` | `sha256:e8f76359c80b270a5eae2f9840289e51e72ff6f854d593ab6bdfda587a73471a` | 20,454,124 B |
| `actfb-frontend` | `sha256:a8be52e1507a2499f0156fa4527d45ede6751476ceacfd561a5e278ece72ffc1` | 23,877,481 B |
| `actfb-notification` | `sha256:6d1f09b8f12bfd36e59e7db987c693d3c94e7a425193a5ba806d3db6290abbc1` | 12,342,732 B |
| `actfb-retention` | `sha256:1b9ab597092ab3ac53cfd14feced5dfc6ddb61cb4aeaeff1635292767eb62262` | 10,940,563 B |
| `actfb-events-dispatcher` | `sha256:b2b9f0a33be4b1f9d591e412fa7477be62bea8585a021f5f6d8310324cd5910d` | 12,001,243 B |
| `actfb-file-scanner` | `sha256:b4aeda7744f0b3103d7af040efcb26ea8849e9621289c0c60c66c9c15b8d881c` | 13,359,289 B |
| `actfb-storage-init` | `sha256:42f7434360c0ab5e30c0388dd492d91bed0959f6991662c8166f788a72ef6c83` | 10,956,257 B |
| `actfb-plugin-runner` | `sha256:c9ee531d5d2c8b5af49a5f150566514046413a0a7bf97acd6f1200f8a93614bc` | 10,292,879 B |
| `actfb-ad-worker` | `sha256:8ff17e908b906948ce64b38259bb9f7003acc3cd471cc979e10c6ce0f3f8275f` | 27,023,875 B |
| `actfb-instance-worker` | `sha256:3d398b0d67112dba1e8ffa3911ac38a8b6fd4f4e6121409f4c205986e6aeb3c1` | 13,932,167 B |

Total compressed 155,180,610 B.

Verification method: each package was resolved over HTTPS against `ghcr.io` with an anonymous pull token
obtained per repository; the `latest` manifest digest was compared with the `sha-3ac89c0…` manifest digest,
the config blob was read for `os`/`architecture` and the OCI labels, and layer sizes were summed. Raw
output: `/tmp/opencode/ui-icons/ghcr-verify.json`. No credential, cookie or token value is recorded here.

This publication ships the UI icon cleanup. **There is no migration, no `config.yaml` and no `.env` change**
in this range and no `/api/v2` contract change, so operators only need `docker compose pull && docker compose
up -d` in `deploy/`; nothing new is applied on boot.

## Previous publication (`bc266be`)

| Field | Value |
| --- | --- |
| Workflow run (push) | https://github.com/nrf24l01/aCTFb/actions/runs/45974501044 |
| Commit (image source) | `bc266beafdb78aeaea22c2d16de3b0037c3b4615` |
| `main` HEAD at publication record | `bc266be` — same commit; the docs update that records this publication touches only `docs/`, which the workflow path filter excludes, so no further run or image rebuild is expected |
| Conclusion | success 4/4 (Backend quality, Frontend quality, Integration, Publish images) |
| Run duration | 09:32:04Z → 09:50:05Z (~18 min) |
| Immutable tag | `sha-bc266beafdb78aeaea22c2d16de3b0037c3b4615` |
| Platform | `linux/amd64` only (read from each image config) |
| Registry visibility | public; anonymous manifest + config-blob reads for all ten images (no stored credentials, no tokens printed) |
| `latest` vs `sha-*` | same manifest digest for every image, verified on all ten |
| `org.opencontainers.image.revision` | `bc266beafdb78aeaea22c2d16de3b0037c3b4615` on all ten |
| `org.opencontainers.image.version` | `sha-bc266be…` |
| Provenance attestations | none; single OCI image manifests |

| Image | Digest | Compressed |
| --- | --- | --- |
| `actfb-backend` | `sha256:6cb218a54d4d4afdc850d04eac45606b3aaea03783a8468f0937813da609bb0d` | 20,454,126 B |
| `actfb-frontend` | `sha256:9229964e5dbd8a65bbaf681061f343b26ee1fdb722619c4f29ef8c7c986260af` | 23,877,220 B |
| `actfb-notification` | `sha256:1fa9bc772fc5c9890a3125cd57cd04721443d6a0f0d8a310284b8386a7982015` | 12,342,732 B |
| `actfb-retention` | `sha256:12c61569398f779511194d6ac0da40d711d4495e61a8a6d2130c24057bcbe33f` | 10,940,563 B |
| `actfb-events-dispatcher` | `sha256:73dae0459cc59a733daa54ee1c67d0730bfee4158e4624b4a491c4feb98d63d9` | 12,001,243 B |
| `actfb-file-scanner` | `sha256:3c77641dff2927583840cc30a6f3cdd0216da129401c08ef4bd2799f02ec7253` | 13,359,289 B |
| `actfb-storage-init` | `sha256:c96f2beef5451946866f26e009d2bd52d2df6619e4f4ab3a574a267400fd4189` | 10,956,257 B |
| `actfb-plugin-runner` | `sha256:4767c35e316435e31579c08db3821f7a41de65269270e35dc1f28d343056be87` | 10,292,879 B |
| `actfb-ad-worker` | `sha256:373c60909c99590b6dbbdbd1d9f7784154561ba11b3cb8198cd5c111b211bf6c` | 27,023,864 B |
| `actfb-instance-worker` | `sha256:8b97083e2c92e95a709c2e9ef5921830c21f6b946a9d7ff7cc8e86234835bca1` | 13,932,167 B |

Total compressed 155,180,340 B.

Verification method: each package was resolved over HTTPS against `ghcr.io` with an anonymous pull token
obtained per repository; the tag manifest digest was compared between `latest` and `sha-bc266be…`, the config
blob was read for `os`/`architecture` and the OCI labels, and layer sizes were summed. Raw output:
`/tmp/opencode/quieter-student-ui/ghcr-verify.json`. No credential, cookie or token value is recorded here.

Published frontend assets were inspected from the published image layers only (not a full application run):
the static bundle contains `Incorrect flag. Try again.`, `text-status-success-ink` and `Awaiting review`, and
contains neither `Already solved` nor `points awarded`.

This publication ships the quieter student UI. **There is no migration, no `config.yaml` and no `.env` change**
in this range and no `/api/v2` contract change, so operators only need `docker compose pull && docker compose
up -d` in `deploy/`; nothing new is applied on boot.

## Previous publication (`5ac573f`)

| Field | Value |
| --- | --- |
| Workflow run (push) | https://github.com/nrf24l01/aCTFb/actions/runs/45909528261 |
| Workflow run (warm repeat) | https://github.com/nrf24l01/aCTFb/actions/runs/45946460043 |
| Commit (image source) | `5ac573fa864d456101242c866db18480243ca237` |
| `main` HEAD at publication record | `5ac573f` — same commit; the docs update that records this publication touches only `docs/`, which the workflow path filter excludes, so no further run or image rebuild is expected |
| Conclusion | success 4/4 on both runs |
| Immutable tag | `sha-5ac573fa864d456101242c866db18480243ca237` |
| Platform | `linux/amd64` only (read from each image config) |
| Registry visibility | public; anonymous manifest + config-blob reads for all ten images (no stored credentials) |
| `latest` vs `sha-*` | same manifest digest for every image, verified on all ten |
| `org.opencontainers.image.revision` | `5ac573fa864d456101242c866db18480243ca237` |
| `org.opencontainers.image.version` | `sha-5ac573f…` |
| Provenance attestations | none; single OCI image manifests |

| Image | Digest | Compressed |
| --- | --- | --- |
| `actfb-backend` | `sha256:cab864f0a7a1d80de97c6a4b8ad69f5c61b93c0abe5320f1dca1ecc23dc82b86` | 20,454,128 B |
| `actfb-frontend` | `sha256:5b9d29288920ab302d2a08b7a95a40ded7bb7134b7ea38f106a9c11745b34a10` | 23,877,622 B |
| `actfb-notification` | `sha256:a47145c9314dfdd60340115d324fc018182af0ef5e6e969964d90f69503ca763` | 12,342,732 B |
| `actfb-retention` | `sha256:67d04b483ca0cc97c3a537a9ad111bbf90c173ee3464a0966bcb7e85d334cbba` | 10,940,563 B |
| `actfb-events-dispatcher` | `sha256:da25c84752899ca7905ad3a00e0e1f789a206154a9136e56137b726623a4b0aa` | 12,001,243 B |
| `actfb-file-scanner` | `sha256:9ddf45af3b8354b2f979a014696689cf3f68b892db6b9e52e129e466d09032be` | 13,359,289 B |
| `actfb-storage-init` | `sha256:34c2b33ff5990b01ed73c8d20ed517c64fe713cf1c8292c41a3649f0495ba888` | 10,956,257 B |
| `actfb-plugin-runner` | `sha256:20e20b82a4fc83f521e591f13519a9e6778e0b7639da8ca51bbdd188636c0e46` | 10,292,879 B |
| `actfb-ad-worker` | `sha256:f6176cdc25d0b5e3a173cb5f52859e1551bf968d4942b63d32ce219873e5395d` | 27,023,886 B |
| `actfb-instance-worker` | `sha256:a2454abf0c8b88d5dcf0df17992183a2c42b46887c8900bc9d993fd2020bdebc` | 13,932,167 B |

Total compressed 155,180,766 B.

This publication ships the desktop workspace and direct-authoring work. **There is no migration, no
`config.yaml` and no `.env` change** in this range and no `/api/v2` contract change, so operators only need
`docker compose pull && docker compose up -d` in `deploy/`; nothing new is applied on boot.

## Previous publication (`034329b`)

| Field | Value |
| --- | --- |
| Workflow run | https://github.com/nrf24l01/aCTFb/actions/runs/45840313641 |
| Commit (image source) | `034329bffd51123d4c5f7a89fa4852d337d1bdbe` |
| `main` HEAD at publication record | `1f61ab1` — docs only; differs from the image source by `docs/` files, which the workflow path filter excludes, so no second run or image rebuild is expected |
| Conclusion | success (Backend quality, Frontend quality, Integration, Publish images — 4/4) |
| Immutable tag | `sha-034329bffd51123d4c5f7a89fa4852d337d1bdbe` |
| Platform | `linux/amd64` only (read from each image config) |
| Registry visibility | public; anonymous manifest + config-blob reads for all ten images (no stored credentials) |
| `latest` vs `sha-*` | same manifest digest for every image |
| `org.opencontainers.image.revision` | `034329bffd51123d4c5f7a89fa4852d337d1bdbe` on all ten |
| Provenance attestations | none; single OCI image manifests |

This publication ships the rich-text Markdown editor: the shared Tiptap editor behind the Write tab of the
board/module/task dialogs and the site page, one sanitising Markdown renderer with syntax highlighting, safe
table typography for readers, and a single form-owned length error. **There is no migration, no `config.yaml`
and no `.env` change** in this range, and no `/api/v2` contract change: stored values stay Markdown strings.
Operators only need `docker compose pull && docker compose up -d` in `deploy/`. The schema is unchanged from
the previous publication (`020_board_join_codes` already shipped), so a deployment moving to this tag applies
nothing new on boot.

CI gate counts for this commit: Backend quality (Go test/vet/build/race) green; Frontend quality
**42 test files / 391 tests**, typecheck and build green; Integration green including the default-profile
browser suite **5 passed** plus the frontend renderer/authoring specs **48 passed** (23 tests × `chromium` and
`phone`, plus 2 authoring projects) against a real native PostgreSQL, real Go backend and real Vite app with
`ACTFB_E2E=1` (no skips).

| Image | Digest | Compressed |
| --- | --- | --- |
| `ghcr.io/nrf24l01/actfb-backend` | `sha256:f735935ac404c226d8ef2bcb64ed77718bd009721bb46b269677f53c5ea116bd` | 20,454,106 B |
| `ghcr.io/nrf24l01/actfb-frontend` | `sha256:9c4c87c00454f84c693f22925cb3e740af2c7e7d77ee4d7f783cb7f245fdc792` | 23,879,177 B |
| `ghcr.io/nrf24l01/actfb-notification` | `sha256:9de5affe81a6b726454f29d068b64cfe33e27cab48d00a5e2ac7fcb14a1529e9` | 12,342,732 B |
| `ghcr.io/nrf24l01/actfb-retention` | `sha256:166b9bfb271056f7a677ae3a6fc142ce338b2cf9056d0df7e2448e6afd2422b3` | 10,940,563 B |
| `ghcr.io/nrf24l01/actfb-events-dispatcher` | `sha256:37de212a8f39cbbc68e7bace86885b3c57885cb1d4bf37c5d14897ab4ed9f81e` | 12,001,243 B |
| `ghcr.io/nrf24l01/actfb-file-scanner` | `sha256:0c23a280c1995efe38c8ac3f29874cd3ef7c3be73a5b88d141a1a761650a94a7` | 13,359,289 B |
| `ghcr.io/nrf24l01/actfb-storage-init` | `sha256:586826d9aae7f302816fc1d553fb2dc8627e7ac3cb6f118200c6c66cc6e74005` | 10,956,257 B |
| `ghcr.io/nrf24l01/actfb-plugin-runner` | `sha256:360c7912ba86d4c0671d9141f6d7795084c52dfb0c570c5049c4b5952b32b58a` | 10,292,879 B |
| `ghcr.io/nrf24l01/actfb-ad-worker` | `sha256:67de6e2abc9bded8974ade2af9f6bf26cf7c39f738d06a6f4495025c7187c2d0` | 27,023,888 B |
| `ghcr.io/nrf24l01/actfb-instance-worker` | `sha256:70014e50fa41b3c9ba0155f6a1d073c0c2f5d68adecabaeebf75a0a8fbc1ac33` | 13,932,167 B |

Published-asset proof (bounded, no operator resources): the published `actfb-frontend` image was pulled,
its `/usr/share/nginx/html` assets extracted to a temporary directory, served read-only on a loopback port and
executed in Chromium. The shipped `markdown-*.js` chunk exports the real shared renderer: a Python fence
renders `class="language-python"` with `hljs-keyword` spans, a GFM table renders inside the generated
`markdown-table-scroll` wrapper with validated `align`, and the shipped stylesheet gives `h1` a larger size than
`p` at weight 600 with `1px` cell borders and no document overflow. The extracted copy, the temporary static
server and the pulled image were then removed; the seven `actfb-prod-*` operator containers were never touched.

## Previous publication (`7f8ada7`)

| Field | Value |
| --- | --- |
| Workflow run | https://github.com/nrf24l01/aCTFb/actions/runs/45794108851 |
| Commit | `7f8ada7556616aa357e0b7e334160c27f876a8f6` |
| Conclusion | success (Backend quality, Frontend quality, Integration, Publish images — 4/4) |
| Immutable tag | `sha-7f8ada7556616aa357e0b7e334160c27f876a8f6` |
| Platform | `linux/amd64` only (`os`/`architecture` read from each image config) |
| Registry visibility | public, anonymous manifest + config-blob reads for all ten images |
| `latest` vs `sha-*` | same manifest digest for every image |
| Provenance attestations | none; single OCI image manifests |

This publication ships three bounded changes: board create/update no longer mints the obsolete legacy
encrypted invite (which is what returned `503` on a deployment without a credential key), the HTTP boundary
logs one actionable cause per failed request, and the admin UI makes the canonical join code the only
automatic code surface. There is **no migration, no `config.yaml` and no `.env` change** in this range: the
`020_board_join_codes` schema shipped in an earlier publication, so operators only need
`docker compose pull && docker compose up -d` in `deploy/`. A deployment still on an older image picks up
`020` through the normal boot migration when it moves to this tag, exactly as before.

| Image | Digest | Compressed |
| --- | --- | --- |
| `ghcr.io/nrf24l01/actfb-backend` | `sha256:bc0e9ad7fc6396d3b1c793fa89aee14882b619c7e366a1ec196b097816f7d5ad` | 20,454,126 B |
| `ghcr.io/nrf24l01/actfb-frontend` | `sha256:8082b68ef5442b5636abdb9dc288a226cbeafac5090f2898edd1f3ba2491dd94` | 23,690,145 B |
| `ghcr.io/nrf24l01/actfb-notification` | `sha256:016132b187c97d71706902a5421a22c5f003bfcb373dbbf8e82b367829e42ccb` | 12,342,732 B |
| `ghcr.io/nrf24l01/actfb-retention` | `sha256:a207a7f29965cbc2bf9e0e9d8e219bde4a85f17c4478d2f18cafbdd9aafc9d96` | 10,940,563 B |
| `ghcr.io/nrf24l01/actfb-events-dispatcher` | `sha256:258221ec32c307766963d2a60033feeebb73acee4f74fc55cde9bef736138cf8` | 12,001,243 B |
| `ghcr.io/nrf24l01/actfb-file-scanner` | `sha256:2f11ee2f9f4382da7a48a2ae0373963aa3371c41eb697248c1a45011b45754a0` | 13,359,289 B |
| `ghcr.io/nrf24l01/actfb-storage-init` | `sha256:22f0fd6d10458ca71abec69640c3ec98e0749ce87db34d8f190c0f2d76482164` | 10,956,257 B |
| `ghcr.io/nrf24l01/actfb-plugin-runner` | `sha256:d09655ba5836bd19be41b6242f5a4b194a076998a04742832fd9545ebc49269b` | 10,292,879 B |
| `ghcr.io/nrf24l01/actfb-ad-worker` | `sha256:27de06e6a46c9a677d287d48c0b3dcacdd49d6033061539f67d1fd7c15a67f6d` | 27,023,889 B |
| `ghcr.io/nrf24l01/actfb-instance-worker` | `sha256:c2f5c6ccd81e64aed95ed4cea2abe1eb099b92c905553bc713e615bc776a1ed2` | 13,932,167 B |

Verified for this publication, anonymously and without registry credentials: each tag resolves to one
manifest, `latest` and `sha-7f8ada7…` share that digest for all ten images, every config reports
`linux/amd64`, and every `org.opencontainers.image.revision` label equals
`7f8ada7556616aa357e0b7e334160c27f876a8f6`.

### Published-image checks actually executed (`7f8ada7`)

A Docker daemon (`29.10.0`) was available for this publication, so the published backend image was checked
directly, in an ephemeral container with its own name, `--network none`, a read-only root filesystem and no
mounted volume — no operator container, network or volume was touched, and nothing was pruned:

```text
docker run --rm --network none --read-only --entrypoint /app/backend \
  ghcr.io/nrf24l01/actfb-backend:sha-7f8ada7556616aa357e0b7e334160c27f876a8f6 admin --help
  → exit 0, lists create/list/promote/demote/reset-password

docker run --rm --network none --read-only --entrypoint /bin/sh <same image> -c 'ls /app/migrations'
  → includes 020_board_join_codes.sql
```

So the published binary really contains the admin CLI and the `020` migration without a database, a config
file or a credential key. The local image was removed afterwards; zero `actfb.instance` containers and zero
verification containers remained.

## Previous publication (`332fdcb`)

| Field | Value |
| --- | --- |
| Workflow run | https://github.com/nrf24l01/aCTFb/actions/runs/45644672697 |
| Commit | `332fdcb422e9eb35d155c07dd28aee270445f00f` |
| Conclusion | success (Backend quality, Frontend quality, Integration, Publish images) |
| Immutable tag | `sha-332fdcb422e9eb35d155c07dd28aee270445f00f` |
| Platform | `linux/amd64` only (`os`/`architecture` in each image config) |
| Registry visibility | public, anonymous manifest + config-blob reads for all ten images |
| `latest` vs `sha-*` | same manifest digest for every image |

This publication ships one frontend change: semantic, accessible status colours (shared status → label/
tone mapping, tokenised `--status-*` palette, visible labels plus decorative icons, ≥4.5:1 contrast,
`EXPIRED`/`CANCELED` no longer rendered as running). No `/api/v2` contract, migration, configuration or
backend behaviour changed, so operators only need `docker compose pull && docker compose up -d` in
`deploy/`; no migration and no `config.yaml`/`.env` edit is required.

| Image | Digest | Compressed |
| --- | --- | --- |
| `ghcr.io/nrf24l01/actfb-backend` | `sha256:1fd48237595afee34c9abca42ad8b4651f4e57b5071d225ad61883c10e79cb9b` | 20,448,093 B |
| `ghcr.io/nrf24l01/actfb-frontend` | `sha256:7f60a206a6adfed105294e69edf43a99f90a6d1dfae47e432deecceaa5a913f1` | 23,690,231 B |
| `ghcr.io/nrf24l01/actfb-notification` | `sha256:30c1699e84439d8f4d03d269e33f5ba72ad14675d04496d989f3c1b904875a72` | 12,342,732 B |
| `ghcr.io/nrf24l01/actfb-retention` | `sha256:9db9952957b86ac8bc708006f37726b2022ef0196ec1344e367a7ced6687c5de` | 10,940,563 B |
| `ghcr.io/nrf24l01/actfb-events-dispatcher` | `sha256:a2b5a887d8b8c9fd21fd94958a4edfdbf2b3aa877fa856b35238e3b9898a303e` | 12,001,243 B |
| `ghcr.io/nrf24l01/actfb-file-scanner` | `sha256:ff3857b1ec0896c50c33e94805eaa30e51c7eedeab01c94812bf9d0a896f8976` | 13,359,289 B |
| `ghcr.io/nrf24l01/actfb-storage-init` | `sha256:67e92043f6572ad122be24c6cfb3525ba2a0b3cb5b93829b5743d317a732044e` | 10,956,257 B |
| `ghcr.io/nrf24l01/actfb-plugin-runner` | `sha256:d9c3b732fc765d1f104f694e087b893d4310d695df39987219332aa9a22f88fd` | 10,292,879 B |
| `ghcr.io/nrf24l01/actfb-ad-worker` | `sha256:ecfb21e8c01ad5349a29a8452ec1cfe92512891b1c130b51536a587d8f0ade81` | 27,023,831 B |
| `ghcr.io/nrf24l01/actfb-instance-worker` | `sha256:bf6ead91a190e8ace25b65dcecdbd7a93a4302d06ac01402466705f465dafd2a` | 13,932,157 B |

Verified for this publication, anonymously and without a local Docker daemon: each tag resolves to one
manifest, `latest` and `sha-332fdcb…` share that digest, every config reports `linux/amd64`, every
`org.opencontainers.image.revision` label equals `332fdcb422e9eb35d155c07dd28aee270445f00f`, and the
published frontend asset layer contains `--status-success-ink` plus the `PENDING_REVIEW` mapping, so the
new UI is really inside the image. No image was pulled or run locally (no Docker daemon here); runtime
behaviour rests on the CI Integration job's production image/runtime and auth/proxy smokes. Evidence:
`docs/status-colors-verification.md`.

## Earlier publication (`925e456`)

Status: **published and verified from `main` at commit `925e456`** (admin module/task preview, default-profile
template catalog, code-only enrollment). Ten separate images are publicly pullable without registry
credentials, each with `latest` and an immutable `sha-<full-sha>` tag resolving to the same digest.

Publication: run https://github.com/nrf24l01/aCTFb/actions/runs/45615311019, image source commit
`925e45665f8622175e74d84eec2bd7dfcf43b978`, immutable tag
`sha-925e45665f8622175e74d84eec2bd7dfcf43b978`. That run's four jobs are green (Backend quality, Frontend
quality, Integration, Publish images) and the integration job actually executed the whole real-PostgreSQL
package list with the DSN, the migration backfill/down-up acceptance (which includes the `020` subtest), the
isolated P0/P1/RustFS integration suites, both browser suites, and the production packaging/auth/runtime
smokes — not a skipped selection.

What this publication ships:

* a live global admin or scoped staff member can read their authorized module and tasks as an organizer
  preview without joining, while every participant-own action (submit, own history, score, instance) still
  requires live participation;
* the admin deployment-template catalog is served with runtime instances disabled, so the default profile no
  longer answers `404` for `GET /admin/deployment-templates`;
* self-enrollment is code-only on every registration mode: a bare board UUID or link never enrolls, public
  boards stay browsable, and the admin join-code panel reveals a code exactly once;
* migration `020_board_join_codes` (hash-only storage, optimistic `revision` CAS).

**Image source commit vs current `main`.** The images are built from source commit
`925e45665f8622175e74d84eec2bd7dfcf43b978`. Any later commit that touches only `docs/` or `README.md` does not
change the images: the CI workflow's `paths` filter excludes documentation, so no rebuild or republish happens
and the tags above remain the current images. Check `git log --oneline <image-source>..main -- ':!docs' ':!*.md'`
before assuming a newer image exists.

Earlier publications (retained for history): `925e456` (admin read, default-profile catalog, code-only
enrollment), `511e039` (organizer UX pass), `29227d7` (YAML application
settings + datasource-only env), `3ef4d5b`/`fea429a` (shared backend/worker image), `f133e3b` (first
one-process-per-container), `2191282` (docs/tests/labels only).

Note that the "images built from source commit `925e456`" paragraph above and the digest table in the
`## Publication` section below describe the `925e456` publication; the current images are the `332fdcb`
ones in the first section.

## Publication

| Field | Value |
| --- | --- |
| Workflow run | https://github.com/nrf24l01/aCTFb/actions/runs/45615311019 |
| Commit | `925e45665f8622175e74d84eec2bd7dfcf43b978` |
| Conclusion | success (Backend quality, Frontend quality, Integration, Publish images) |
| Platform | `linux/amd64` only |
| Provenance attestations | none; single OCI image manifests |
| Registry visibility | public, verified by anonymous manifest and config-blob reads for all ten images |
| `latest` vs `sha-*` | same digest for the backend (and each image's tags resolve to one manifest) |

| Image | Digest | Compressed |
| --- | --- | --- |
| `ghcr.io/nrf24l01/actfb-backend` | `sha256:958542235d19ae510aa108d389f8e8a3a3441366b98b7295f928c9755187a3ae` | 20,448,089 B |
| `ghcr.io/nrf24l01/actfb-frontend` | `sha256:bfe92464d2b210331423685bfda96e6853d9564999fbbbf5e7dd0d1333db04da` | 23,689,786 B |
| `ghcr.io/nrf24l01/actfb-notification` | `sha256:3ffa88f05321fc864823e26026e7c7044290cf2b59f4f92dea7ad6c7bdffcd7a` | 12,342,732 B |
| `ghcr.io/nrf24l01/actfb-retention` | `sha256:8d30f6c6b6116821c66584b07b91afec523e326271284a9e9d876edcff83c988` | 10,940,563 B |
| `ghcr.io/nrf24l01/actfb-events-dispatcher` | `sha256:26aae3c161c6ec47faa9bd717e1becb9db7e5aa42ff135ab40374d90f6fe8267` | 12,001,243 B |
| `ghcr.io/nrf24l01/actfb-file-scanner` | `sha256:1874553ed9ea1f7fa43e0cb8245655f388a61d894416d9d04d2ef4ccd0542d13` | 13,359,289 B |
| `ghcr.io/nrf24l01/actfb-storage-init` | `sha256:d99a67b5dba9aae57b6b75e944c19207b643210c77547f28b19b77c04e2585b9` | 10,956,257 B |
| `ghcr.io/nrf24l01/actfb-plugin-runner` | `sha256:4d284c9a847c1415e893ce2e51d322594f3d26d2eec2793fcd5fc08a1639a9b6` | 10,292,879 B |
| `ghcr.io/nrf24l01/actfb-ad-worker` | `sha256:f74491577592f3b3abd4818c18ccae56fff5c97d620458c17383678e1425f5ad` | 27,022,953 B |
| `ghcr.io/nrf24l01/actfb-instance-worker` | `sha256:75c9e2e8d504ef4920912a5b608a382f3f4865dc8c55a49aee96837588d8d086` | 13,932,157 B |

## What was verified for `925e456`, and how

Scope note: this section documents the `925e456` publication only. The current
publication's evidence is in the `7f8ada7` section at the top.

Verification is split by what each environment can actually prove. No claim below is
inferred from a different commit.

**1. Registry-side, anonymous, no Docker daemon required (performed for `925e456`).**
Each of the ten packages was resolved over HTTPS against `ghcr.io` with an anonymous
pull token: the tag manifest digest, the config blob's `os`/`architecture`, the OCI
source labels, and the layer count/size. All ten resolved as `linux/amd64` single
manifests whose `org.opencontainers.image.revision` equals
`925e45665f8622175e74d84eec2bd7dfcf43b978`, and the backend's `latest` and
`sha-925e456…` tags resolve to one digest (`sha256:958542235d19…`). The backend config
blob also shows the expected runtime shape: `Cmd ["/app/backend"]`, user
`10001:10001`, workdir `/app`.

**2. CI, with a real Docker daemon (performed for `925e456` in run `45615311019`).**
The Integration job built all production images and ran the packaging contract,
production image runtime, and production auth/proxy smokes against them. It also ran
both browser suites, the whole real-PostgreSQL package list with the DSN, the
migration backfill/down-up acceptance including the `020` subtest, and the isolated
P0/P1/RustFS integration suites. The Publish job then pushed the images.

**3. Not performed for this publication: a local `docker pull` / `docker run` of the
published tags.** This review environment has no Docker daemon, so no local pull or
local container execution is claimed here. The previous publication (`5edd6d5`) had
that evidence, including uncompressed `docker image inspect .Size` figures and the
cleanroom lifetime smoke (migrations through `019`, `admin create --password-stdin`
from the published binary, instance start/extend/reset/expiry with 0 live endpoints,
leases and provider containers). Those results apply to `5edd6d5`, not to `925e456`.
For `925e456`, the published-image behaviour is covered by the CI Docker smokes in
point 2 and by the registry verification in point 1.

Known limit worth stating plainly: the participant-own instance authorization change
in this range is proven end to end by a real-PostgreSQL test with a trusted recording
fake provider (worker reached `Destroy` with the exact provider id, ports and leases
released, all operations terminal) plus the HTTP route chain. That is the code path
the change touches. It is not a claim that a physical container was destroyed by the
published binary in this environment.

## Public pull verification (previous publication, `5edd6d5`)

The commands below were executed for the `5edd6d5` publication with an empty temporary
`DOCKER_CONFIG`. They were **not** re-executed for `925e456` because this environment
has no Docker daemon; see the split above.

```bash
docker pull ghcr.io/nrf24l01/actfb-backend:latest
docker pull ghcr.io/nrf24l01/actfb-frontend:latest
docker pull ghcr.io/nrf24l01/actfb-notification:latest
docker pull ghcr.io/nrf24l01/actfb-retention:latest
docker pull ghcr.io/nrf24l01/actfb-events-dispatcher:latest
docker pull ghcr.io/nrf24l01/actfb-file-scanner:latest
docker pull ghcr.io/nrf24l01/actfb-storage-init:latest
docker pull ghcr.io/nrf24l01/actfb-plugin-runner:latest
docker pull ghcr.io/nrf24l01/actfb-ad-worker:latest
docker pull ghcr.io/nrf24l01/actfb-instance-worker:latest
```

The same check resolved each immutable `sha-*` tag anonymously.

## One process per worker image

The production Dockerfile has one shared Go build stage and separate final targets. Each target copies exactly
one binary and only the files it reads. The backend image contains only the backend, migrations, email templates,
config, and the non-root plugin data root. It contains no worker binaries, Git, Docker CLI, source, compiler, or
caches. Only `ad-worker` includes `git` and Docker CLI. `plugin-runner` and `instance-worker` use the Go Docker
API and have no CLI.

| Worker | Image | Profile/dependencies |
| --- | --- | --- |
| retention | `actfb-retention` | default; PostgreSQL only |
| events-dispatcher | `actfb-events-dispatcher` | default; PostgreSQL + NATS |
| notification | `actfb-notification` | `email`; NATS + SMTP + templates |
| file-scanner | `actfb-file-scanner` | `files`; PostgreSQL + NATS + S3 + ClamAV |
| storage-init | `actfb-storage-init` | `files`; one-shot, `restart: "no"` |
| plugin-runner | `actfb-plugin-runner` | `plugins`; trusted Docker API + loopback endpoint |
| ad-worker | `actfb-ad-worker` | `ad`; trusted Docker CLI + git |
| instance-worker | `actfb-instance-worker` | `runtime`; trusted Go Docker API |

Only the backend has a healthcheck (`/ready`). Worker images have no healthcheck and no HTTP endpoint. The
socket is mounted only into trusted worker services; the backend never receives it. Compose uses the dedicated
proxy bridge, required exact origin, Host port preservation, and XFF overwrite from the prior review fixes.

## Focused review closure

- `deploy/.env.example` includes the files backend flag/scan key, credential key/version, pinned AD runner
  image field, actual plugin runner URL (`http://127.0.0.2:8092`), and generation commands for JWT, encryption,
  scan, service keys, and the runner token. Optional profiles remain off by default.
- Production docs now list per-profile prerequisites and explicitly state that a profile flag alone does not
  enable the backend feature or provision credentials. Optional workers are not claimed as production-verified.
- PostgreSQL worker overrides are available in Compose: `RETENTION_POSTGRES_USER/PASSWORD`,
  `EVENTS_POSTGRES_USER/PASSWORD`, `SCANNER_POSTGRES_USER/PASSWORD`, `AD_POSTGRES_USER/PASSWORD`, and
  `RUNTIME_POSTGRES_USER/PASSWORD`. Unset values fall back to the existing shared role, so baseline behavior is
  unchanged and no fake roles are created.
- The docs explicitly warn that `POSTGRES_USER` is the initdb superuser and that the current baseline is a
  single-role, non-least-privilege setup. Retention and scanner scopes are linked to their actual docs; the
  events dispatcher is scoped to `events_outbox`; AD/runtime exact grant matrices are not documented and must be
  derived/reviewed rather than granted all tables.
- README now says only the AD worker image contains Git/Docker CLI and lists the default events/retention images.
- Packaging static tests now select only the `FROM runtime AS backend` stage, reject worker binaries there,
  assert exactly nine runtime targets, and validate the shipped env template and per-worker role overrides.

## Actual tests

- `docker buildx build --target <target>` for all nine Go targets plus the frontend, all `linux/amd64`.
- Packaging contract: **311 checks passed** with all ten images. Each image was inspected for architecture,
  non-root user, exactly one app binary, no other worker binaries, static/stripped/no `NEEDED` binary, no
  toolchain/source/cache, and per-target tools. Backend only has `/ready`; workers have no healthcheck.
- All worker entrypoints reached their expected validation path: notification (SMTP disabled), events
  (PostgreSQL), file-scanner (files disabled), storage-init (S3 endpoint), plugin-runner (token), AD/instance
  (credential key). Retention parsed its flags and completed a real `--once` pass against PostgreSQL.
- Runtime smoke: real PostgreSQL 18 migrations through `16`, backend `/ready`/`/health`, frontend `/healthz`,
  SPA deep link, and `/api` proxy.
- Auth/proxy smoke: exact HTTPS origin with non-default port, login → refresh → logout, wrong origin 403,
  four distinct backend client IPs, independent rate-limit identities, attacker XFF rejected, non-root plugin
  volume writable.
- `go test ./...`, `go vet ./...`, `go build ./...`, `go test -race ./...`; frontend typecheck and 68/68 tests.
- For the `5edd6d5` publication the integration job also ran the real-PostgreSQL feature packages (including
  the instance-lifetime suite) and migrations through `019` on a clean schema; the browser step in CI ran the
  core `p1-core` spec and skipped the 18 full-stack specs that need a local Docker Engine and fixture images.
  The full 19-spec suite was run locally against this same source (19/19 passed, 0 new containers or volumes,
  core and Vue artifact hashes unchanged), and a separate cleanroom run of the published images exercised the
  lifetime API end to end.
- Quality gates: actionlint, shellcheck warning-level, bash syntax, and `docker buildx bake --print` with ten
  targets all passed.
- CI ran the existing Playwright/full integration coverage. No test browsers or containers remain.

## Tradeoffs

The standalone images total more registry bytes than the old shared image, but each running container is much
smaller and has a much smaller attack surface. The backend fell from 205,295,938 B to 56,149,571 B uncompressed.
The AD worker remains the largest worker at 69,429,576 B because it alone carries Git and Docker CLI. The
frontend is unchanged at 55,091,714 B. Workers run on the same Docker host under the operator's trust boundary;
this does not make untrusted host code safe. The default PostgreSQL role remains the initdb superuser for all
database workers; least-privilege worker roles are an operator provisioning task and are not created by Compose.
The common Alpine runtime intentionally keeps `ca-certificates`, `tzdata`, and `curl`; `curl` is not used by
worker health (workers have none), but it remains the approved runtime/health contract for the backend and a
small accepted layer tradeoff. Plugin-enabled production still has the separate asset-origin caveat.
