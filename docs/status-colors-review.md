# Status colours — independent review

Range: `bc1304758e00460c1c680826fb3bac90af079340..e9dfc29fcbd98bc0567c826ffc0ab38ad82482ad`
(`feat(frontend): give status badges accessible semantic colours`)

Verdict: **APPROVED** — no blocking issues. Eight minor, non-blocking notes below, ordered by value.

## Intent

Replace ad-hoc per-component status colours with one shared status → `{ label, tone }` mapping plus one
tokenised palette, so every badge carries a visible label, a decorative icon, and ≥4.5:1 text contrast,
without touching API, backend, dependencies, layout, or the dark theme.

## Verified against the code (not just the doc)

1. **Coverage of real API statuses.** Every status in `frontend/src/shared/types/api.ts` is mapped:
   `BoardStatus`, `ModuleStatus`, `TaskStatus`, `SubmissionStatus`, join code (`OPEN|CONSUMED|EXPIRED`),
   `FileValidationStatus`, `TaskInstanceSummary` (incl. `NOT_CREATED`), instance (`PROVISIONING|RUNNING|
   RESETTING|DESTROYING|ERROR|EXPIRED`), integration (`CONNECTED|ERROR|UNKNOWN`), vulnerability
   (`FOUND|PATCHED`), AD run (`QUEUED|RUNNING|PASSED|FAILED|ERROR|CANCELED`), plugin
   (`discovered|installed|enabled|disabled|incompatible|unhealthy|failed`, normalised from lowercase) and
   outbox (`pending|delivered|failed|dead`). `CANCELLED` is accepted defensively though the API only emits
   `CANCELED`. Nothing falls through to the humanised-neutral fallback that is actually a first-class state.
2. **Legacy tone compatibility.** One caller still uses the old vocabulary — `TeamPanel.vue:266`
   (`label="Team" tone="ok"`). All eight legacy names are aliased, and each alias is asserted in
   `StatusBadge.test.ts`. No caller passes an unaliased tone, and no caller depends on the removed shape
   encoding.
3. **Canceled / expired are not "processing".** `EXPIRED`, `CANCELED`, `CANCELLED`, `FINISHED`,
   `CONSUMED`, `DISABLED`, `STOPPED`, `DESTROYED` → `muted`; asserted in all three test files with an
   explicit `not.toContain('var(--status-active-ink)')`.
4. **Unknown stays neutral.** `statusTone('SOMETHING_NEW')` / `''` → `neutral`, and the label is humanised
   (`PROVIDER_TIMEOUT` → "Provider timeout", `provider-timeout` → "Provider timeout").
5. **Colour is never the only cue.** The label is a real text node; the icon is `aria-hidden="true"`; the
   former `animate-pulse` is gone (`grep animate-pulse frontend/src` → only `ui/skeleton/Skeleton.vue`).
6. **Contrast, recomputed independently** from the committed hex tokens against each tone's own opaque
   surface: success 7.75, active 8.06, attention 8.37, error 5.13, restricted 6.76, neutral 5.81,
   muted 5.16 — all ≥4.5:1, matching the CSS comment and `status-colors-verification.md` exactly. Because
   the surfaces are opaque hex, these are the real rendered ratios; against the page surfaces the same inks
   measure 6.07–12.81 (`--base`) and 7.22–12.81 (`--black`).
7. **shadcn does not override the palette.** `outline` contributes `text-foreground` / `border-border` /
   `border-transparent` as Tailwind utilities; `StatusBadge` passes `color`, `backgroundColor` and
   `borderColor` as an inline style, which outranks every layered utility. The only `!important` in
   `badgeVariants` is `[&>svg]:size-3!`, identical to the component's own `size-3`. Hover overrides are
   `[a]:`-scoped and the badge renders a `<span>`, so the tint cannot be repainted on hover.
8. **No theme/dependency drift.** Tokens are additive in `:root`; the Rosé Pine surfaces, radii and
   typography are untouched, there is no light theme to re-measure, and `@lucide/vue` was already a
   dependency. Scope in the diff matches scope in the doc.
9. **Doc claims hold.** "Unpushed" is accurate (`main` is ahead 1 of `origin/main`), the `animate-pulse`
   claim is accurate, and the mapping table matches `status.ts` line for line. I re-ran only the three new
   test files: 3 files / 25 tests passed.

## Minor issues (none blocking)

**M1 — `truncate` is inert.** `StatusBadge`'s label span has `min-w-0 truncate`, but the badge itself is
`w-fit shrink-0`, so the span is never width-constrained and can never ellipsize. A pathologically long
unknown status widens the badge instead of clamping. No shipped status is long enough to overflow, so this
is cosmetic: either drop the two classes (they promise clamping that does not happen) or add a `max-w-*`
to the badge. File: `frontend/src/components/layout/StatusBadge.vue:70`.

**M2 — two grays are perceptually identical.** `neutral` (#a6a1bd on #2a2836) and `muted` (#9a95b2 on
#282635) differ by ~2 % lightness; the requirement grouped draft/unknown/inactive/expired into one gray
family. Keeping both is harmless and internally consistent, but it doubles a token triple for no visible
distinction. Optional: collapse to a single inactive tone, or push `muted` a step darker.

**M3 — misleading token comment.** `frontend/src/style.css` says the inks are "measured against #191724
tints"; the ratios are actually measured against each tone's own surface (#292d31 … #282635), which is the
correct thing to measure. The numbers are right, the sentence is not. Suggest "measured against each tone's
own surface".

**M4 — borders are very faint.** Border vs own surface is 1.62–2.04:1 (1.91–2.63 vs page base). That is fine
for a decorative outline — meaning is carried by text plus icon, so WCAG 1.4.11 does not apply — but the
`error` and `muted` borders are effectively invisible. Leave as-is unless a designer wants more definition;
this is consistent with the "subtle borders" brief.

**M5 — `statusMeta()` is exported but unused.** `frontend/src/shared/lib/status.ts` exports it, only the
test consumes it. Either use it in `StatusBadge` (`statusMeta(props.status)`) or drop it (YAGNI).

**M6 — precedence rules are untested.** The component implements "`tone` wins over `status`" and "`label`
wins over `status`", but no test covers either, nor an invalid runtime tone (which would render an empty
icon and an undefined `var()`). Also `expect(html).not.toContain('animation')` is a weak assertion. Low
value, but these are the two branches most likely to regress.

**M7 — browser-check scaffolding is untracked.** `e2e/run-status-colors-visual.sh`,
`e2e/status-colors-visual.mjs`, `frontend/status-visual-fixture.html` and
`frontend/status-visual-fixture.ts` exist in the working tree but are not in this commit, while
`status-colors-verification.md` states no browser pass was run. Decide explicitly: commit them (moving the
fixture out of the `frontend/` root) or delete them. Note `tsconfig.app.json` includes only `src/**` and
vitest only `src/**/*.test.ts`, so a root-level fixture is invisible to both and would silently rot.

**M8 — known divergence, deliberately out of scope.** A few non-badge status surfaces still use the older
palette: `RoleBindingsPanel.vue:126` (`border-pine/60 text-pine` for active/expired bindings) and
`TaskCard.vue:23` (`text-pine` "Solved" beside the now-green "Solved" pill). Not part of this slice;
recorded so the divergence is a known follow-up rather than a surprise.

## Not requested, not done

No behaviour, backend, router, layout or theme changes were reviewed as part of this slice because the diff
contains none. No commit, push, or browser run was performed for this review.

---

# Second pass — `e9dfc29..9c7e6c9` (committed harness + polish)

Range: three commits — `f92eeed` (e2e harness), `d2cc392` (tone wording, duplicate solved marker),
`9c7e6c9` (docs). Twelve files, all reviewed. Narrow scope: the harness, the fixture, and the four
review follow-ups. **Verdict: APPROVED** — no blocking issue; three non-blocking nits below.

## Requested checks

1. **No command-substitution leak in the harness — fixed and correct.** `service_start` is now called as a
   plain function with `>/dev/null` (line 35-36), so it runs in the harness shell: `SERVICE_PIDS`/
   `SERVICE_STARTTIMES` are populated in the shell that owns the traps, and `VITE_PID="$SERVICE_LAST_PID"`
   reads the leader PID directly. A `$(...)` wrapper would have made the substitution subshell the group
   leader and left the recorded PID outside the owned group — exactly the failure the header comment
   documents. The only remaining substitutions are read-only (`PORT`, `service_proc_starttime`).
2. **Exact owned group, bounded cleanup.** `set -m` before sourcing gives the leader its own PGID;
   `lifecycle_traps cleanup` is installed before the server starts, so a failure in the readiness loop or
   the Node script still TERM/KILLs only that group. After the browser pass the script stops the group,
   waits, and asserts `kill -0 -- -PID` fails, exiting 1 with a message if anything survived. Cleanup never
   uses `pkill`/`killall` and never signals the harness group. The disclosed three earlier leaked groups
   (616633, 618351, 618945) are gone from this tree — no script or doc reference to them beyond the
   incident note, and `git status` shows no leftover fixture at `frontend/` root.
3. **No fixtures in `dist/`.** `vite.config.ts` has no `rollupOptions.input`, so the build input stays the
   single root `index.html`; `frontend/fixtures/**` is not an entry and `frontend/public/` does not contain
   it. `frontend/Dockerfile` copies only `/app/dist` into the nginx stage. `find dist -iname '*fixture*'`
   is empty. The fixture is reachable only through the dev server at `/fixtures/status-colors/index.html`.
4. **Fixture is typechecked.** `tsconfig.app.json` now includes `fixtures/**/*.ts`. I verified the program
   actually contains it (`vue-tsc -p tsconfig.app.json --listFilesOnly` lists
   `frontend/fixtures/status-colors/main.ts`) and that `bun run typecheck` exits 0. So the fixture cannot
   rot silently. Note this also means a fixture type error fails `bun run build`, i.e. the production image
   build — intended here (that is what stops rot), worth knowing before the next harness is added.
5. **Unexpected tone → neutral.** Implemented and tested (`tone: 'chartreuse' as never` → neutral ink, no
   `var(--status-chartreuse…)`, icon still present). One residual hole, nit N1.
6. **Label/tone precedence tests.** Both branches covered: `tone` beats `status` while the label still comes
   from the status (`REJECTED` + `ok` → green "Rejected"), and `label` beats `status` (`ACCEPTED` + "Graded").
   `StatusBadge.test.ts` is now 10 tests; I re-ran that file: 10 passed.
7. **`TaskCard` duplicate "Solved" removal is information-preserving.** Backend equivalence holds:
   `internal/boards/service_tasks.go:178` `taskStatus()` returns `SOLVED` iff `task.Solved`, and every list/
   detail path (`service_tasks.go:61,117,144,171`, `service_admin_tasks.go:32,145,280`,
   `service_admin_extra.go:57`) sets `Status = taskStatus(...)` before the mapper copies both `Status` and
   `Solved` verbatim (`http/mappers.go:218,230`). The staff preview path additionally forces
   `Solved = false` *and* recomputes the status, so the two cannot disagree. The card still says "Solved"
   once, now via the green success pill. No test depended on the removed span.
8. **`TeamPanel` category tone.** `label="Team" tone="neutral"` is right: "Team" is a category, not a
   health/lifecycle state, so the success colour was misleading. Neutral resolves through `TONE_ICONS`
   (`'neutral' in TONE_ICONS`), so it renders the neutral ink/surface/border and the circle icon.
9. **Docs match the code.** 65 badges is arithmetically right (7 tones × 19 statuses × 3 surfaces = 57,
   plus 8 legacy = 65); the harness's own gate is `>= 60` (nit N2). The `style.css` comment now says
   "each ink … on its own opaque surface, measured in Chromium", which is accurate. The verification doc is
   honest about the limits (component fixture, not app/API acceptance; implementer could not view images)
   and about the earlier leak incident. My M1/M2/M4/M5 notes remain open and are not claimed as fixed.

## Non-blocking nits

**N1 — `in TONE_ICONS` walks the prototype chain.** `'toString' in TONE_ICONS` (and `constructor`,
`valueOf`, `hasOwnProperty`) is `true`, so such a runtime tone escapes the neutral fallback and renders
`var(--status-toString-ink)` plus a non-component value to `<component :is>`. Unreachable from any typed
call site, but it is precisely the guarantee the comment claims. One-line fix:
`Object.hasOwn(TONE_ICONS, props.tone) ? … : 'neutral'`. File: `StatusBadge.vue:54`.

**N2 — the harness does not assert full fixture coverage.** `check(raw.length >= 60)` plus "every measured
badge has an expected tone" is one-directional: dropping, say, `PASSED` or a legacy tone from the fixture
still passes (removals of `EXPIRED`/`CANCELED`/`FINISHED` *are* caught by the explicit check). Cheap fix:
assert `measurements` covers every `Object.keys(EXPECTED_TONE)` key. Also `tokens[undefined].ink` throws a
raw `TypeError` instead of a readable failure when a new fixture key lacks an expectation.

**N3 — trivial.** `tsconfig.app.json` lost its trailing newline; `StatusBadge.vue`'s comment "Older callers
still pass these names" is now stale (after the `TeamPanel` change no production caller passes a legacy
tone — only tests and the fixture exercise the aliases, which is a fine reason to keep them).

## Evidence I ran myself

`cd frontend && bun run typecheck` → exit 0; `bunx vitest run src/components/layout/StatusBadge.test.ts`
→ 10 passed; `vue-tsc -p tsconfig.app.json --listFilesOnly` → fixture present; `node` import check from
`e2e/` → `playwright-core` resolves (exits 2 with "BASE_URL is required", as designed). No dev server, no
browser run, no build, no commit, no push. `docs/status-colors-review.md` is still untracked.
