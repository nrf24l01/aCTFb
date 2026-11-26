# Desktop workspaces and direct rich-text authoring

Status: approved by the user on 2026-11-26. UX/design owner: main assistant.
Base: `0da549e`. DeepSeek collects facts, implements and tests; it does not choose the UX.

## Intent and constraints

Make large screens useful through adjacent, related work areas rather than a stretched mobile column.
Remove the obsolete outer Write/Preview tabs from all four Tiptap authoring surfaces.
Keep the existing dark Rosé Pine palette, typography, status colors and shadcn-vue primitives.
Keep Markdown storage, syntax highlighting, source protection, form payloads and authorization unchanged.
No backend, database, API, deployment configuration or dependency changes.
No autosave, new dashboard statistics, new provider behavior or parallel editor instances.

## Evidence

The current fixed 212px sidebar leaves 1708px at a 1920px viewport, but the shared page container
caps at 1152px including padding: only 1104px is usable. The task reader splits at a 1024px viewport,
leaving a 360px description beside a 380px action panel. Board/module dialogs cap at 576px and
task dialogs at 672px. Outer editor tabs consume another 155px and unmount Tiptap on Preview.
Measured facts and before screenshots: `/tmp/opencode/responsive-layout-facts.md` and
`/tmp/opencode/responsive-layout-before/`; screenshots were inspected by the main assistant.

## Approaches and decision

1. Increase widths alone: rejected; creates long reading lines and does not improve workflow.
2. Force two columns at a viewport breakpoint: rejected; ignores the persistent sidebar and
   repeats the cramped 1024px task layout.
3. Chosen: bounded wide workspace, with content-width-based columns and purpose-specific rails.
   Preserve a single logical document order on smaller screens.

## Shared geometry

- Page container maximum outer width: **1600px**, including existing 24px desktop side padding.
- Keep the 212px sidebar and existing mobile navigation. Align the shell alert strip to the new cap.
- Keep authentication/profile-like simple forms bounded; wider workspace does not mean wide inputs.
- Use CSS container queries, or an equivalent layout based on the actual available content width.
  Do not calculate breakpoints from `window.innerWidth` in JavaScript.
- Standard main/rail split when usable width is **at least 1100px**:
  `minmax(0, 1fr) 320px`, 24px gap. Task actions use **360px**.
- Main column must retain at least 680px for the task split; below the threshold stack naturally.
- Both columns and their grid/flex ancestors use `min-width: 0`.
- Prose has an approximately 80ch measure, but code blocks and tables can use their panel width.
  Do not constrain all of `.markdown-body` to 80ch and thereby squeeze code/tables.
- No page-level horizontal scrolling. Intentional table/code scrolling stays inside its panel.
- At 200% zoom or narrow viewports, columns reflow instead of shrinking controls.

## Direct authoring

Remove outer Write/Preview markup, imports, preview computations and preview-only state from:
BoardSettingsDialog, ModuleEditorDialog, TaskEditorDialog and AdminSitePage.
Keep exactly one lazy Tiptap instance per form, its Visual/Source control and its counter.
Preserve the single form-owned length error, accessible ID references, and unchanged submission guards.
Do not delete unrelated Tabs primitives just because these consumers stop using them.

### Dialogs

Board/module dialogs: maximum **1200px**. Task dialog: maximum **1360px**.
On desktop retain at least 24px viewport clearance per side; on mobile retain the existing
full-height presentation but ensure exact viewport width, not the measured 382px overflow at 375px.
Use a named authoring container and switch its form body to two columns only at **1040px of usable
body width**: `minmax(0, 1fr) 320px`, 24px gap. Otherwise stack.
One dialog-body vertical scroller; header and footer remain outside it. Do not create separately
scrolling editor and settings columns.

| Surface | Main column | Settings rail |
|---|---|---|
| Board | Name, description editor | Access/audiences, participation, schedule, existing Advanced fields |
| Module | Name, description editor | Format/legacy resolution, audience, schedule, existing Advanced/limits |
| Task | Title, content editor; deployment template and parameters below when applicable | Format, points, category, accepted answers or submission policy, visibility and lifetime limits |

Title/content lead the logical document order. Preserve every conditional field, its current
validation, secret write-only behavior and legacy mode handling. No duplicated controls.
For INSTANCE fields, split existing presentation only where the component boundaries allow it;
do not change inheritance, immutability or provider semantics to achieve a layout.
Keep the editor usable at a minimum 16rem height on desktop; allow content growth inside the body.
Footer: existing Cancel and primary save action, aligned right on desktop and reachable on mobile.

### Homepage editor

Remove the 768px form cap. Use the shared main/320px settings layout at 1100px usable width.
Main: title and introduction editor. Rail: existing Published control and save action with concise
existing help. No invented publishing states, duplicate save buttons or live-preview column.

## Views

### Organizer board

Header and scoped alerts remain full width. Main column is **Modules and tasks**, retaining the
existing full-width task tables within each module. Rail is Join code, concise existing board
information and Lifecycle. Lifecycle becomes a labelled collapsed disclosure; destructive actions
stay inside it with their existing confirmations and permissions. Do not add another settings API.
Legacy invite access stays explicit/advanced, and its button wraps without overflowing mobile.
DOM order: primary module work first, then supplementary management rail. Mobile follows that order.
Keep Create module in the page header; avoid the duplicate section-level primary action.

### Participant board / board list

Board: modules in the main column, participation/standing in a 320px rail when width permits.
Module cards become two columns when their own available width reaches **760px**, otherwise one.
Team management remains a full-width section of the main column, not a squeezed card.
Boards list: browsing content and the existing 320px supplementary rail follow the shared threshold.
Do not invent new counters or move gated actions into otherwise public card content.

### Module

Keep heading, status, format, schedule and preview warnings above the tasks.
Task cards: one column below **720px**, two at 720px, three at **1200px** usable list width.
Use equal track widths and normal row order; no masonry.
Keep Pentest's dedicated entry point and all empty/locked states unchanged.

### Task

Header/alerts full width. Description and attachments in main; participant submission and instance
controls in 360px rail at 1100px usable width. At 1024px viewport with sidebar, stack them.
History remains full-width below the workspace because it is a table, not a narrow activity feed.
Do not make a tall rail sticky or constrain it to an independent scroll region.
Preview users still see no participant forms, instance actions, history or history requests.

### Administration

Admin hub: existing navigation cards in two columns at 720px, three at 1200px.
Templates catalog: two columns at 720px, three at 1200px, using existing content only.
Administrative tables inherit the wider page cap; group existing search/filter/action controls in
wrapping horizontal toolbars. Preserve table semantics and local scroll at smaller widths.
Instances: keep the existing list/detail data and route behavior; do not introduce a new master-detail
interaction in this slice. The wider cap and wrapping filter toolbar are sufficient.
Template manifest editor and feature-specific panels are not being redesigned.

## Acceptance

- No outer Write/Preview tabs remain on the four forms; Visual/Source remains functional.
- Real-browser undo/redo works after editing and after responsive resizing without an editor remount.
- No editor loss, duplicate mount, changed save payload, accidental submit or lost source protection.
- At 1920px a wide page exceeds its old 1104px usable width; at 1440px use columns where the
  usable width allows; at 1024px the task workspace stacks with a full-width description.
- Dialogs show main/settings columns on a sufficiently wide screen and a logical single column
  on smaller screens; all four form variants remain editable and save normally.
- Real DOM geometry, not class-name-only assertions, proves rail widths, columns and card counts.
- At 375px, 1024px, 1440px, 1920px and 200% zoom: no document overflow, obscured footer/focus,
  inaccessible errors or clipped essential controls. Verify active selects and long text.
- Preserve current syntax highlighting, table wrapping, role-scoped preview and code-only enrollment.
- Unit tests, typecheck, build, real browser regressions and the existing four-job CI pipeline pass.
- Independent review and main-assistant screenshot inspection precede publication.

## Known implementation boundaries

Default-profile runtime features can be disabled. Use a component fixture for long INSTANCE form
layout where necessary, label it as such, and do not claim it proves provider execution.
No operator databases, volumes, containers or existing listeners are test fixtures.
