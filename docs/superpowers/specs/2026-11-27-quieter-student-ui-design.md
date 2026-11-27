# Quieter student UI

Main-owned design for the user's request: less explanatory text and fewer borders, especially
the paragraphs below “Already solved”. This is a small presentation cleanup, not a new theme.

## Direction

Choose spacing, headings and existing status colors over nested outlined panels.
Do not remove all boundaries globally: inputs, focus indicators, dialogs, code/table readability
and destructive confirmations keep their functional affordances.

## Exact scope

- Student task, board/module cards, submission forms/history and student runtime-panel copy.
- Preserve desktop/mobile geometry, dark palette, fonts, syntax highlighting and shadcn primitives.
- No backend, API, dependencies, configuration, authorization or other student-audit bug fixes.
- Keep all actions, validation, request/response behavior and server-derived status values.

## Copy

- Existing solved task: concise **“Solved”**, without paragraphs about server storage or extra score.
- Just accepted: one **“Accepted · +N points”** outcome when the response contains awarded points;
  otherwise “Accepted”. Never assume the task's maximum points were awarded.
- Do not simultaneously show “Already solved” and a new accepted outcome.
- Wrong flag: **“Incorrect flag. Try again.”** Keep the real response/error classification.
- Pending manual review: **“Awaiting review”**, not a raw `PENDING_REVIEW` paragraph.
- Remove repetitive routine explanations about hidden answers, client-side authorization,
  idempotency, Swagger endpoints, backend permission checks and character limits that do not
  affect the current action. Keep required submission constraints and actionable errors.
- Preserve retry of the same in-memory flag/idempotency key; do not retain it in browser storage.
- Keep access/availability explanations, limits, network error actions, file-scan status and
  irreversible-action warnings where the student needs them to decide or recover.
- Do not expose flags or new data fields while reducing explanatory copy.

## Visual hierarchy

- Task description: no outer rectangular frame and no redundant “Description” header strip.
- Submission controls: remove the outer panel and boxed header; retain visible field labels and
  the primary action. Routine success/solved/review feedback is a compact text/icon line, not
  another bordered box inside the panel.
- Keep errors visible with existing semantic alert behavior; do not silently hide error states.
- Submission history: one visible “Your submissions” heading; remove nested outer frames and
  repeated visible captions. Keep semantic table headers and useful horizontal row separators.
- Board/module/task links: reduce four-sided card outlines; keep generous row spacing,
  clear hover feedback, meaningful status colors and visible keyboard focus.
- Remove the extra grey “Solved” badge from TaskPage when its status pill already says Solved.
- Other student sections may lose decorative outer/header borders, but do not remove form,
  table-cell, editor or status borders globally. No changes to organizer layouts in this slice.

## Acceptance

- Desktop and phone screenshots show materially fewer decorative boxes and shorter routine copy.
- Solved, accepted, rejected, pending-review, network-error/retry and preview paths remain usable.
- Statuses never depend on color alone; alert semantics, labels and keyboard focus remain.
- No clipped controls or document overflow at 375/1024/1440/1920 widths.
- One student action still yields the same API call/payload, verdict, history and score.
- Unit/type/build gates plus real student-browser checks and existing full-stack selectors pass.
- Main inspects screenshots; independent review precedes push/publication.
