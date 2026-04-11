# Shell-Ball Dashboard Open Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a true desktop interaction so double-clicking the shell-ball opens or focuses the `dashboard` window without breaking existing voice, hover, drag, or locked-voice click behavior.

**Architecture:** Keep shell-ball responsible for deciding whether a dashboard-open gesture is allowed, and keep actual desktop window operations inside the platform layer. Split route-navigation helpers from desktop-window helpers so `security`-style page navigation does not share semantics with predeclared Tauri desktop windows like `dashboard`, then wire shell-ball double-click into the typed desktop-window helper.

**Tech Stack:** Tauri 2 window API, React 18, TypeScript, existing shell-ball interaction controller, node:test shell-ball contract suite.

---

## File Map

- Modify: `apps/desktop/src/platform/windowController.ts`
- Modify: `apps/desktop/src/features/shell-ball/useShellBallInteraction.ts`
- Modify: `apps/desktop/src/features/shell-ball/components/ShellBallMascot.tsx`
- Modify: `apps/desktop/src/features/shell-ball/ShellBallSurface.tsx`
- Modify: `apps/desktop/src/features/shell-ball/ShellBallApp.tsx`
- Modify: `apps/desktop/src/features/shell-ball/shellBall.contract.test.ts`
- Optional cleanup:
  - Modify: `apps/desktop/src/features/dashboard/safety/SecurityApp.tsx`
  - Modify: `apps/desktop/src/features/dashboard/DashboardApp.tsx`

## Interaction Contract

### Gesture Precedence

1. Drag from `host-drag-zone`
2. Long-press voice entry / voice gesture flow on mascot hotspot
3. Locked-voice single click ending `voice_locked`
4. Resting-state mascot double-click opening/focusing `dashboard`

### Event-Boundary Arbitration

The mascot hotspot has separate pointer and click phases today, so this plan must treat them as one logical interaction sequence.

- if a pointer sequence is consumed by long-press voice handling, any later `click` or `dblclick` event from that sequence must be ignored
- the first click of a successful double-click sequence must remain a no-op for dashboard opening
- drag interactions starting from `host-drag-zone` must never reach mascot double-click handling

### Dashboard-Open Gate

Double-click may open/focus `dashboard` only when:

- state is `idle` or `hover_input`
- the pointer interaction was not already consumed by long-press voice flow
- the event originated from the mascot hotspot, not the drag zone

Double-click must not open/focus `dashboard` in:

- `confirming_intent`
- `processing`
- `waiting_auth`
- `voice_listening`
- `voice_locked`

### Single-Click Contract

Single click on the mascot hotspot must not open `dashboard` in resting states.

Existing single-click behavior remains:

- `voice_locked` single click ends locked voice
- `idle` / `hover_input` single click does not open dashboard
- the first click of a successful double-click sequence still does not open dashboard on its own

## Window Controller Contract

Platform layer should separate two concerns clearly:

- route navigation for page-style transitions
- desktop window open/focus for predeclared Tauri windows

Desktop window control should expose one typed API:

- `openOrFocusDesktopWindow(label: "dashboard" | "control-panel")`

Avoid ambiguous parallel APIs that mix routes and window labels:

- `openWindow(label)`
- `focusWindow(label)`

If route navigation helpers remain in the same file, they must stay explicitly separate from desktop window control.

`openOrFocusDesktopWindow("dashboard")` must:

- resolve an existing Tauri window by label
- call `show()`
- call `setFocus()`
- never create a duplicate dashboard instance
- treat a missing Tauri handle for `dashboard` as a configuration/runtime error, not as a silent semantic fallback
- only use browser fallback for true browser-only callers that are not asserting the predeclared desktop-window contract

### Task 1: Split Route Navigation from Desktop Window Focus API

**Files:**
- Modify: `apps/desktop/src/platform/windowController.ts`
- Test: `apps/desktop/src/features/shell-ball/shellBall.contract.test.ts`

- [ ] **Step 1: Write the failing contract test**

Add assertions that `windowController.ts`:

- exposes `openOrFocusDesktopWindow(label)` with a typed desktop label contract
- resolves windows by Tauri label
- calls `show()` and `setFocus()` for desktop behavior
- keeps route navigation separate from desktop-window focus behavior
- prefers an existing labeled window and never introduces a create-new-window path for `dashboard`
- treats a missing `dashboard` handle as an error in desktop mode

- [ ] **Step 2: Run test to verify it fails**

Run: `pnpm --dir apps/desktop test:shell-ball`
Expected: FAIL because the controller still mixes page navigation with desktop window behavior and does not expose the typed desktop API.

- [ ] **Step 3: Write minimal implementation**

Update `apps/desktop/src/platform/windowController.ts` to:

- add `openOrFocusDesktopWindow(label: "dashboard" | "control-panel")`
- keep route-navigation behavior separate for page targets like `security`
- use Tauri window lookup + `show()` + `setFocus()` for desktop labels
- fail loudly when a required predeclared desktop window is missing
- keep browser fallback only for the route-navigation path or explicitly browser-only callers

- [ ] **Step 4: Update touched callers if needed**

If cleanup is chosen, replace only the callers that truly target desktop windows such as `dashboard`; do not rewrite route-navigation callers like `openWindow("security")` into the desktop API.

- [ ] **Step 5: Run test to verify it passes**

Run: `pnpm --dir apps/desktop test:shell-ball`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add apps/desktop/src/platform/windowController.ts apps/desktop/src/features/shell-ball/shellBall.contract.test.ts apps/desktop/src/features/dashboard/safety/SecurityApp.tsx apps/desktop/src/features/dashboard/DashboardApp.tsx
git commit -m "feat(desktop-shell-ball): split desktop window focus api"
```

### Task 2: Add an Explicit Shell-Ball Gesture Arbitration Contract

**Files:**
- Modify: `apps/desktop/src/features/shell-ball/useShellBallInteraction.ts`
- Test: `apps/desktop/src/features/shell-ball/shellBall.contract.test.ts`

- [ ] **Step 1: Write the failing test**

Add pure tests for helpers such as:

- `canOpenShellBallDashboard(state)`
- `shouldOpenShellBallDashboardFromDoubleClick({ state, interactionConsumed })`

Cover:

- `idle` => allowed
- `hover_input` => allowed
- all active/voice states => blocked
- any pointer interaction already consumed by long-press voice flow => blocked
- any consumed pointer sequence suppresses later `dblclick` handling for that same sequence

Also add an explicit contract:

- single click in `idle` / `hover_input` does not open dashboard
- the first click of a successful double-click sequence does not open dashboard

- [ ] **Step 2: Run test to verify it fails**

Run: `pnpm --dir apps/desktop test:shell-ball`
Expected: FAIL because the helpers do not exist yet.

- [ ] **Step 3: Write minimal implementation**

In `apps/desktop/src/features/shell-ball/useShellBallInteraction.ts`:

- add a pure helper for dashboard-open eligibility
- add a pure helper for double-click arbitration
- expose enough interaction state to suppress `dblclick` after a consumed long-press/voice sequence
- expose the derived gate so `ShellBallApp` can decide whether to call `openOrFocusDesktopWindow("dashboard")`

- [ ] **Step 4: Run test to verify it passes**

Run: `pnpm --dir apps/desktop test:shell-ball`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add apps/desktop/src/features/shell-ball/useShellBallInteraction.ts apps/desktop/src/features/shell-ball/shellBall.contract.test.ts
git commit -m "feat(desktop-shell-ball): add dashboard open gesture contract"
```

### Task 3: Wire Mascot Double-Click Without Breaking Existing Input Semantics

**Files:**
- Modify: `apps/desktop/src/features/shell-ball/components/ShellBallMascot.tsx`
- Modify: `apps/desktop/src/features/shell-ball/ShellBallSurface.tsx`
- Modify: `apps/desktop/src/features/shell-ball/ShellBallApp.tsx`
- Test: `apps/desktop/src/features/shell-ball/shellBall.contract.test.ts`

- [ ] **Step 1: Write the failing test**

Add assertions that:

- mascot hotspot exposes a dedicated double-click callback
- the surface passes the callback through
- the app uses the interaction gate before calling `openOrFocusDesktopWindow("dashboard")`
- the drag zone remains separate
- single-click handler is still present and distinct from double-click
- consumed long-press voice sequences do not leak into later dashboard-opening `dblclick`

- [ ] **Step 2: Run test to verify it fails**

Run: `pnpm --dir apps/desktop test:shell-ball`
Expected: FAIL because shell-ball has no double-click wiring yet.

- [ ] **Step 3: Write minimal implementation**

Update:

- `ShellBallMascot.tsx` to bind `onDoubleClick`
- keep existing `onClick` intact
- ensure long-press/voice-consumed interactions prevent accidental dashboard opening
- `ShellBallSurface.tsx` to pass the callback
- `ShellBallApp.tsx` to call `openOrFocusDesktopWindow("dashboard")` only when the gate allows

- [ ] **Step 4: Run test to verify it passes**

Run: `pnpm --dir apps/desktop test:shell-ball`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add apps/desktop/src/features/shell-ball/components/ShellBallMascot.tsx apps/desktop/src/features/shell-ball/ShellBallSurface.tsx apps/desktop/src/features/shell-ball/ShellBallApp.tsx apps/desktop/src/features/shell-ball/shellBall.contract.test.ts
git commit -m "feat(desktop-shell-ball): open dashboard on mascot double click"
```

### Task 4: Verify Window Reuse and No Duplicate Dashboard Instances

**Files:**
- Verify only
- Test: `apps/desktop/src/features/shell-ball/shellBall.contract.test.ts` if a pure reuse contract is added

- [ ] **Step 1: Add or extend the contract test for window reuse**

If practical, add a contract asserting the controller uses the existing predeclared `dashboard` labeled handle rather than any create-new-window path when opening `dashboard`.

- [ ] **Step 2: Run shell-ball tests**

Run: `pnpm --dir apps/desktop test:shell-ball`
Expected: PASS.

- [ ] **Step 3: Run typecheck**

Run: `pnpm --dir apps/desktop typecheck`
Expected: PASS.

- [ ] **Step 4: Run build**

Run: `pnpm --dir apps/desktop build`
Expected: PASS.

- [ ] **Step 5: Run desktop app manually**

Run: `pnpm --dir apps/desktop exec tauri dev`

Expected:

- drag from `host-drag-zone` still works
- long-press on mascot still enters voice flow
- locked-voice single click still ends locked voice
- single click in `idle` / `hover_input` does not open dashboard
- double-click in `idle` / `hover_input` opens `dashboard`
- a second double-click while `dashboard` already exists only brings the existing predeclared window to front and focuses it
- no duplicate dashboard window instance appears
- shell-ball / bubble / input windows continue to behave as before

- [ ] **Step 6: Commit only if manual verification required a follow-up fix**

```bash
git status --short
```

Expected: clean working tree; if not, commit with:

```bash
git add <fixed-files>
git commit -m "feat(desktop-shell-ball): polish dashboard open behavior"
```
