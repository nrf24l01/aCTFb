# Desktop Workspaces Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Remove redundant editor tabs and implement the main assistant's approved desktop layouts.
**Architecture:** Shared bounded workspace geometry and CSS container-query layouts, with one
continuously mounted lazy editor per authoring form. Existing data, service calls and permission
guards remain unchanged.
**Tech Stack:** Vue 3, TypeScript, Tailwind 4, shadcn-vue/Reka, existing Tiptap, Vitest, Playwright.
**Spec:** `docs/superpowers/specs/2026-11-26-desktop-workspaces-design.md`
**Base:** `0da549e35ba0fa4aab811abc96a0411a8482e966`.

## Global Constraints

- Main assistant owns UX. DeepSeek implements the spec and collects evidence, not new design choices.
- Page maximum outer width 1600px. Shared column threshold 1100px usable content width.
- Shared rail 320px; task rail 360px; gap 24px. CSS layout, not viewport-width JavaScript.
- Board/module dialogs max 1200px; task max 1360px; two-column authoring at 1040px usable body width.
- Keep one dialog-body scroller and its existing outside header/footer.
- Remove outer Write/Preview only; preserve Tiptap Visual/Source, source protection, highlighting,
  lazy loading, counter, limits and exactly one form-owned validation message.
- No dependency, backend, migration, API, deploy/config or provider-behavior changes.
- No autosave, invented metadata or weaker RBAC to make screenshots easier.
- Preserve theme/fonts/status colors. Do not globally restyle Markdown or the UI primitives.
- Serial, low-memory work; no Podman; no broad process/container cleanup.
- Local logical commits only until independent review; no push before main approval.

## Review Focus

1. Long editor forms: all fields and save/footer stay reachable with one scroll region.
2. Rich content/source: moving/resizing the editor preserves source, selection and undo history.
3. Preview authorization: no newly mounted submission/history/instance controls or extra requests.
4. Intrinsic widths: long legacy buttons, tables, code, selects and tokens do not widen the page.
5. Actual content width: sidebars and zoom must delay columns instead of squeezing the main panel.

## Task 1: Direct editors and wide authoring forms

**Files**
- Modify `frontend/src/features/admin/components/BoardSettingsDialog.vue`,
  `ModuleEditorDialog.vue`, `TaskEditorDialog.vue`.
- Modify `frontend/src/features/site/pages/AdminSitePage.vue`.
- Modify their four existing `.test.ts` files.
- Modify `frontend/e2e/rich-text-editor.spec.ts`.
- Modify `frontend/src/style.css` only for shared, narrowly scoped authoring geometry.
- Create `frontend/e2e/desktop-workspaces.spec.ts` for measured layout assertions.

**Interfaces**
- Consume the unchanged `MarkdownEditorLazy` string v-model, label/id, max-length, invalid,
  described-by and show-length-error props.
- Produce named content/settings regions with stable `data-slot="authoring-main"` and
  `data-slot="authoring-settings"`, within `data-slot="authoring-layout"`.
- Preserve existing dialog props/emits, submit handlers and request shapes.

- [ ] Write failing tests: outer tabs absent; one editor; source switching/validation still work;
  valid/invalid IDREF behavior unchanged. Update old Preview tests to the actual direct-editor
  interaction, keeping their original validation and safe-rendering intent.
- [ ] Write a failing real-browser test for wide dialog columns, one scroller, reachable footer,
  editor undo/redo and preserving the editor DOM instance across width changes.
- [ ] Run the targeted tests and record the actual failure before implementation.
- [ ] Remove outer tabs and preview-only imports/state. Keep unrelated Tabs primitive files.
- [ ] Implement the four authoring arrangements exactly as the spec's main/settings table defines.
  Keep long runtime parameter controls in main and limits in rail without changing semantics.
- [ ] Add named authoring CSS container queries: 1040px body threshold, 320px rail, 24px gap;
  dialog caps 1200/1360px with viewport clearance. Ensure the phone dialog never exceeds viewport.
- [ ] Run unit suite with one worker, typecheck, build, renderer tests and real editor CRUD
  browser flows against the owned default-profile stack. Do not silently skip legacy coverage.
- [ ] Capture desktop/phone screenshots of the three dialogs and homepage editor; record geometry.
- [ ] Update task report and implementation-plan status as partial; commit this task.

## Task 2: Shared workspace and reader/organizer views

**Files**
- Modify `frontend/src/components/layout/PageContainer.vue` and `AppShell.vue`.
- Modify `frontend/src/features/boards/pages/{BoardsPage,BoardPage,ModulePage,TaskPage}.vue`.
- Modify `frontend/src/features/admin/pages/{AdminBoardPage,AdminHubPage,AdminTemplatesPage,AdminInstancesPage}.vue`.
- Modify `frontend/src/features/admin/components/AdminModuleSection.vue` only where wrapping
  headers/actions or preserving local table scroll requires it.
- Modify `frontend/src/style.css` for named workspace/card-grid container rules.
- Modify/add the existing relevant page unit tests and `frontend/e2e/desktop-workspaces.spec.ts`.

**Interfaces**
- Preserve PageContainer's existing slot API; add an explicit bounded variant only if a narrow
  existing form needs it. Auth routes retain their existing separate narrow layout.
- Shared regions: `data-slot="workspace"`, `workspace-main`, `workspace-aside`.
- Module/task collections retain original links, item order, statuses and permission predicates.
- No new fetch or mutation call is part of this task.

- [ ] Write failing geometry tests against actual pages: usable width expands at 1920; task stacks
  at 1024 with sidebar; cards count by distinct left/top coordinates; admin module work appears
  beside Join code instead of below it on wide screens.
- [ ] Add regression assertions for preview no-own requests/controls, legacy-code button at 375px,
  lifecycle disclosure and the existing publish/archive/delete confirmation/permission behavior.
- [ ] Run to establish RED.
- [ ] Raise page and shell alert-strip cap to 1600px; retain narrow purpose-specific forms.
- [ ] Implement named CSS content-width workspace queries with the spec's rail sizes/threshold.
  Use `minmax(0,1fr)` and `min-width:0` on children; preserve independent table/code local overflow.
- [ ] Apply card tracks: board modules 2 at 760px list width; module tasks 2 at 720px and 3 at
  1200px; hub/templates 2 at 720px and 3 at 1200px. Normal row order, no masonry.
- [ ] Reorder AdminBoardPage DOM to main module work then rail; keep scoped alerts outside the
  grid. Place Join code and lifecycle in rail, with Lifecycle collapsed and labelled; keep
  confirmations and explicit legacy access. Remove duplicate Create module action.
- [ ] Apply the task 360px rail only at 1100px usable width; full-width history below; remove an
  unnecessary empty rail for preview only if it does not change the visible access explanation.
- [ ] Group existing admin filters/actions into wrapping toolbars; do not create new interactions.
- [ ] Keep prose approximately 80ch through scoped text-block rules, not a max-width on code/tables.
- [ ] Run all frontend unit/type/build gates and actual default-profile browser tests.
- [ ] Capture representative 1920/1440/1024/375 screenshots, including participant and preview.
  Record field bounds, track counts, scrollers and zero document overflow.
- [ ] Update report/implementation status and commit this task.

## Task 3: Acceptance matrix, CI coverage and documentation

**Files**
- Modify `frontend/e2e/desktop-workspaces.spec.ts`, relevant existing e2e specs as required.
- Modify `e2e/run-default-profile-e2e.sh` only to include the new real-backend spec in the existing
  owned-stack invocation. Keep the existing four workflow jobs and all previous acceptance tests.
- Modify `docs/frontend.md`, `docs/implementation-plan.md`.
- Create `docs/desktop-workspaces-verification.md`.

**Interfaces**
- Consume existing lifecycle helpers and first-party test sessions, not production operator data.
- Produce reproducible measurements/screenshots and complete browser coverage for the approved views.

- [ ] Assert desktop/phone editor tab removal and functional Visual/Source, toolbar/language
  controls, source byte preservation, undo/redo, Unicode limits and actual save/reopen payloads.
- [ ] Assert the responsive matrix at 375/1024/1440/1920 and 200% zoom: usable panel sizes,
  no document overflow, all controls/focus/footer reachable, local table/code overflow only.
- [ ] Check long FLAG/MANUAL/INSTANCE form layout. If runtime-disabled limits the actual API flow,
  use a labelled component-layout fixture for INSTANCE fields; do not claim provider execution.
- [ ] Ensure no page navigation or resize adds duplicate editors, duplicate network requests,
  hidden-form focus targets or participant-own requests in organizer preview.
- [ ] Run the entire existing default-profile suite plus new desktop spec on one owned stack and
  the real Chromium renderer/authoring projects. Keep CI execution enabled, not skip-gated.
- [ ] Run frontend unit suite, typecheck and build; capture final counts/chunk behavior.
- [ ] Write verification with exact commands, before/after dimensions, screenshot paths, limitations,
  main UX rulings and cleanup evidence. Mark publication pending until approved and actually green.
- [ ] Commit tests/docs and request whole-slice independent review. Do not push.
