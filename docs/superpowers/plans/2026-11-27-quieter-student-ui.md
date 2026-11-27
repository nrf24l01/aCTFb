# Quieter Student UI Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Remove routine prose and decorative panel nesting from student views.
**Architecture:** Presentation-only template/copy changes and narrowly scoped student-surface styles.
Existing state machines, API calls, shared primitives and desktop geometry stay unchanged.
**Tech Stack:** Vue 3, TypeScript, existing shadcn-vue, Vitest and Playwright.
**Spec:** `docs/superpowers/specs/2026-11-27-quieter-student-ui-design.md`
**Base:** `08a6df4`.

## Global Constraints

- Main owns UX; DeepSeek implements and collects evidence.
- No backend, API, dependencies, configuration or authorization changes.
- No global border removal, new font/theme, new animation or unrelated audit fixes.
- Preserve labels, focus cues, status icons, alerts, retry payloads and destructive confirmations.
- Real awarded points only; never replace the result with assumed task-max points.
- Work serially and keep RAM low. No Podman, operator resources or broad cleanup.
- Local logical commits only before review; no push until main approval.

## Review Focus

1. Just-accepted and already-solved state must not produce duplicate outcomes.
2. Retry must keep the in-memory answer/key, with no new persistence or accidental resubmission.
3. Manual submission constraints remain visible; no raw enum or hidden validation/error state.
4. Borderless links and controls still have clear keyboard/hover affordances.
5. Tests retain behavioral assertions rather than weakening them to accommodate copy changes.

## Task 1: Student presentation cleanup and verification

**Files:** inspect and modify only relevant templates/styles in:
`frontend/src/features/boards/pages/{TaskPage,BoardPage,ModulePage}.vue`;
`frontend/src/features/boards/components/{FlagSubmitForm,ManualSubmissionForm,SubmissionHistory,ModuleCard,TaskCard,InstancePanel}.vue`;
and `frontend/src/style.css` if narrowly scoped rules are needed.
Update their tests and affected existing browser specs; do not alter shared primitive defaults.
Update `docs/implementation-plan.md`; create `docs/quieter-student-ui-verification.md`.

**Interfaces:** preserve all component props/emits, state transitions, requests and payloads.
Consume existing server verdict/score fields. Produce shorter visible copy and unframed content;
no new public interface is required.

- [ ] Read the spec, current source and audit screenshots. Confirm actual point-result fields and
  existing retry behavior before changing templates.
- [ ] Write failing tests for solved-only copy, single accepted outcome, awarded score accuracy,
  pending-review text, duplicate badge removal and preserved errors/retry semantics.
- [ ] Run targeted tests and record RED.
- [ ] Apply the exact copy/hierarchy changes. Prefer existing spacing and semantic status colors.
  Keep meaningful table separators, inputs, focus outlines and important warnings.
- [ ] Run targeted tests, then all frontend unit tests with one worker, typecheck and build.
- [ ] Pre-audit all affected browser assertions, including getByText/toHaveText/toContainText,
  before CI. Update obsolete copy selectors without dropping semantic/behavior checks.
- [ ] Run real student-browser flows on one owned stack: solved/rejected/accepted/manual review,
  network retry, preview, desktop and phone. Verify same API/score behavior and no secret leaks.
- [ ] Capture before/after screenshots and actual border/copy checks at relevant viewports.
  Main will judge visual composition; the implementer reports measurements, not a new UX design.
- [ ] Record exact gates, screenshot paths, limitations and cleanup; commit locally and request review.
