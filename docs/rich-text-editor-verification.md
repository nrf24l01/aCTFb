# Rich-text Markdown editor — verification record (2026-11-26)

Task 3 of `docs/superpowers/plans/2026-11-26-rich-text-editor.md`, on the approved Task 1/Task 2 base
`cc73480`. This record contains only commands that were run and results that were observed. Nothing
here claims a provider, migration or deployment change: the slice is frontend-only.

## Scope delivered

- The shared `MarkdownEditor` is integrated in the existing Write tab of `BoardSettingsDialog`,
  `ModuleEditorDialog`, `TaskEditorDialog` and `AdminSitePage`; ids, labels, props, emits, payloads
  and Write/Preview tabs are unchanged.
- `BoardPage` and `ModulePage` render their stored descriptions through `renderMarkdown` in the
  existing `PageHeader` subtitle slot (`.markdown-body.markdown-body-inline`); `TaskPage` adds
  `.markdown-body` to its existing description container.
- `MarkdownEditorLazy` (`defineAsyncComponent`) keeps the Tiptap bundle out of public readers and
  shows a small non-editable "Loading editor…" notice while the chunk loads.
- Toolbar buttons are `type="button"`; `Enter` in the contenteditable stays a ProseMirror newline;
  the inline link row applies on `Enter` and never submits the dialog form.
- GFM table `align` survives the shared reader only on `th`/`td` and only for `left`/`center`/`right`;
  `style`, arbitrary `class` and event handlers stay blocked.
- Limits: 50,000 runes for board/module/task descriptions, 131,072 for the site intro, counted with
  `Array.from` (code points) and enforced before the API call.
- `@tiptap/extension-paragraph` 3.35.0 is now a direct pinned dependency (the module already imported
  it). No backend, `/api/v2`, migration, `deploy/` or `config.yaml` file changed.

## Exact gates run

### Unit, typecheck, build

```bash
cd frontend
bun run test:unit -- --maxWorkers=1
bun run typecheck
bun run build
```

Observed on 2026-11-26:

- Unit: **42 test files / 390 tests passed** (Task 2's earlier 295-test and 374-test snapshots are
  superseded; this is the combined final count).
- `bun run typecheck`: clean.
- `bun run build`: passed. Relevant chunks:
  - `dist/assets/MarkdownEditor-KE_ZPIcu.js` — 503.17 kB, **158.89 kB gzip** (lazy).
  - `dist/assets/MarkdownEditorLazy-DWTP1dqF.js` — 9.55 kB, 3.27 kB gzip (wrapper + loading notice).
  - `dist/assets/markdown-rpKa2MRG.js` — 154.27 kB, **51.29 kB gzip** (shared reader).
  - The 17-id highlighting registry and all editor features were kept. The editor chunk is above the
    ~150 kB gzip guide; it is accepted only because it is lazy and absent from the public path.

### Real-Chromium renderer/editor gate (no backend)

```bash
cd frontend
E2E_CHROMIUM_PATH=/usr/bin/chromium bun run test:e2e -- markdown-render.spec.ts
```

Observed: **16 tests passed in each project** — `chromium` (desktop) and `phone` (375 px Chromium).
The phone project is Chromium rather than WebKit so the CI Chromium install is sufficient; the layout
contract is engine-independent. Coverage actually executed:

- highlighted Python code with escaped `<script>` payload and no auto-detection of `brainfuck`;
- DOMPurify removal of `onerror`, `onclick`, `style`, arbitrary `class` and crafted fence attributes,
  while the legitimate `hljs-*` class survives;
- computed highlighter contrast ≥ 4.5:1 against the code background, and not the foreground colour;
- GFM table alignment via real `getComputedStyle().textAlign` (`left`/`center`/`right`) and a crafted
  raw-HTML `align="evil"` that never becomes an attribute;
- styled HTML paste → plain `<strong>`, no `style`/`onclick`/`script`, no `javascript:` link, no XSS;
- Source fallback for legacy raw HTML/task lists, unchanged bytes, no emit, Escape keeps Source;
- visual-override `ConfirmDialog` with Cancel focused and Escape cancelling;
- `Mod-k` inline link row applying on `Enter` without form submission;
- code-point counter and over-limit message;
- toolbar wrapping at 375 px with no document horizontal overflow and local code-block overflow.

### Real authoring gate (native PostgreSQL + real Go backend + real Vite)

```bash
E2E_PG_DSN='postgres://postgres@127.0.0.1:<port>/postgres?sslmode=disable' \
  ./e2e/run-default-profile-e2e.sh
```

Observed on 2026-11-26: default-profile suite **5 passed**, then the frontend specs **34 passed**
(16 renderer tests × 2 projects + `rich-text-editor.spec.ts` × 2 projects), with one Playwright
worker on the shared stack. The runner sets
`ACTFB_E2E=1`, `E2E_API_URL`, `E2E_ADMIN_*` and `E2E_PARTICIPANT_*`, so the authoring spec executes
in CI rather than skipping. The local PostgreSQL was an isolated temporary cluster; no operator
container, volume or the production `127.0.0.1:8080` stack was used.

`rich-text-editor.spec.ts` actually executes:

- board description editing through `BoardSettingsDialog`;
- module description editing through `ModuleEditorDialog` including bold, a Python code block chosen
  from the language picker, and reopening Source to assert byte-exact stored Markdown;
- task description editing through `TaskEditorDialog` including a styled HTML paste (`**x**`, no
  `style=`, no `javascript:`) and a Python code block, then reopening Source byte-exactly;
- an over-limit 50,001-character description that cannot be saved (no PATCH request) and then a
  discard-confirm close;
- site introduction editing through `AdminSitePage`, visible on the public landing page;
- a real participant who joins by code and reads the board, module and task pages with
  `.markdown-body` and `.hljs-keyword` rendered read-only, and no raw Markdown literals;
- public-vs-admin lazy loading: the public landing page requested no `MarkdownEditor.vue` module, and
  opening the board dialog requested it.

## Artifacts

- Desktop screenshot: `/tmp/opencode/rich-text-editor/desktop.png`
- Phone (375 px) screenshot: `/tmp/opencode/rich-text-editor/phone.png`
- Full local runner log: `/tmp/opencode/run-default-final4.log`

Screenshots are generated from the test-only fixture with the real editor and app stylesheet; they
are not committed and are not CI acceptance evidence.

## CI wiring

`.github/workflows/go.yml` already had four jobs. No fifth job or matrix was added. The existing
`integration` job runs `./e2e/run-default-profile-e2e.sh`, which now also executes
`frontend/e2e/markdown-render.spec.ts` and `frontend/e2e/rich-text-editor.spec.ts` against the same
Vite/backend/PG stack with `ACTFB_E2E=1`. The step is unfiltered; `E2E_FRONTEND_GREP` only narrows a
local run and is unset in CI. Failed frontend traces remain under `frontend/test-results`, which is
ignored by git.

## Limits and remaining gaps

- `@tiptap/markdown` is still an early-release package; the semantic round-trip corpus and the
  Source fallback for raw HTML, task lists and unsafe URLs remain mandatory, not optional.
- happy-dom cannot exercise DOMPurify or ProseMirror selection, so sanitisation and selection are
  proved only in Chromium. The browser gate must stay in CI.
- The editor chunk is 158.89 kB gzip (above the ~150 kB guide). Lazy loading keeps it off the public
  path; reducing it would require dropping languages or features, which the approved spec rejected.
- The authoring browser gate needs the real backend/PG stack; it is not part of the frontend-only
  job. If that stack is unavailable, the spec skips locally, and the correct report is "not
  executed" — never "passing".
