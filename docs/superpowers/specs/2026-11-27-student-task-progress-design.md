# Student task progress

The user approved all four recommendations: solved-card highlighting, module progress,
completion filters, and a board task-progress summary. Main owns these UX decisions;
DeepSeek implements and gathers evidence.

## Approaches

- Client counts over the first 100 task rows are cheap but incorrect and are rejected.
- Fetching every module/task page to calculate counts adds requests and races; rejected.
- Use additive server-backed aggregates and server-side filtering with existing pagination.
  Main selects this approach to meet the approved correctness and privacy requirements.

## Presentation

- Solved task cards receive a very subtle existing-success-color tint, no new border,
  and the existing readable Solved label with a Lucide check-circle SVG. Do not duplicate
  status badges/icons. Keep solved cards navigable and retain clear hover/focus states.
- Module cards for participant-readable task-bearing modules show a thin, unanimated
  progress bar and one short label, e.g. `3/8 solved`. Completed nonempty modules also
  show a check-circle; no explanatory paragraph or additional panel.
- Module task lists show the same unfiltered module total, followed by
  `All / Unsolved / Solved` controls. Default All. Filtering happens on the server,
  before counting/pagination. Support Load more; reset pagination when the filter or
  route changes, prevent stale requests replacing newer state, preserve errors/retry.
- The board module area shows one compact `Task progress` summary and bar. This covers
  currently accessible discrete tasks, not scoreboard points or lab/AD completion.
- Zero tasks is `0/0 solved`, empty track, never Complete or 100%. Unavailable progress
  is omitted, not fabricated as zero. Do not make upcoming/disabled module cards green.
- Existing theme, fonts, responsive grids, input borders and sparse copy remain.
  Use existing semantic success ink, SVG icons only, no emoji, gradients or new motion.
- Main's implementation details: reuse the existing Solved StatusPill (it already
  contains CircleCheckIcon), rather than adding a second check. Solved card background
  mixes existing success ink at 8% with black; hover at 12%. Bars are 4px high,
  semantic success ink over the existing muted track, with a readable 12px fraction.
  Filter hit targets are at least 44px; wrap without horizontal page overflow.
- Progress has an accessible name, numeric semantics and visible fraction; controls
  have selected-state semantics, keyboard support and phone-sized hit targets.
  Normal text contrast is at least 4.5:1; meaningful progress graphics at least 3:1.

## Domain and privacy

Board → permitted TASKS/EXTENSION module → visible task → current USER/TEAM subject's
ACCEPTED submission. Distinct tasks, not attempts; zero awarded points still count.
PENDING_REVIEW, CHECKING, REJECTED and ERROR do not count as solved.

Reuse live participant/access-group/audience/team policies. Never derive current subject
from client UUIDs. No own-progress projection for anonymous/unjoined/staff-preview,
revoked-access, or TEAM-without-team subjects. A participant who is also staff must not
gain counts from management-visible sibling modules, hidden tasks, or audiences.

Use the same availability gate as participant ListTasks: currently active board/module,
enabled module, visible task. Upcoming/disabled/unreadable resources do not contribute
to denominators or leak their task counts. Explain this scope in the API contract.
This does not broaden finished-board content access or change scheduling policy.
PENTEST and ATTACK_DEFENSE use separate workflows, not fake task percentages.

## Contract evolution

Extend canonical Swagger /api/v2 additively, regenerate Go models, preserve current
response envelopes and default list behavior. New progress fields are optional;
old clients can continue to omit them. Prefer a shared TaskProgress object with
nonnegative integer solvedTasks and totalTasks, solvedTasks <= totalTasks.
Provide progress on BoardMe, ModuleListItem and Module as appropriate; module list
aggregation must be batched and board totals must cover all permitted modules, not
only the first page. Add one validated completion query to participant task lists.
No migration or dependency is expected; justify either before adding it.

## Acceptance and non-goals

Test individual/team subjects, zero-point acceptance, manual pending→accepted,
duplicate submissions, >100 tasks/modules, empty modules, visibility/audience/access
revocation, team changes, staff preview, cross-parent IDs and invalid filters.
Progress stays unfiltered when the task list is filtered. Return/reload after acceptance
refreshes card, module and board data; no local-storage progress cache.

Run Go tests/vet plus real-PG aggregate/security tests and migrations; frontend
unit/type/build; real desktop/phone journeys with before/after screenshots and
375/1024/1440/1920 overflow/focus/contrast checks. Independent review precedes push;
CI and anonymous GHCR verification precede publication claims.

Do not fix unrelated onboarding/team-key/draft issues, change scores, submissions,
RBAC, runtime providers, operator configuration or user-authored Markdown.
