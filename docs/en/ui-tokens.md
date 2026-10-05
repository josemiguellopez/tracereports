# UI tokens and components

🌐 **English** · [Español](../es/ui-tokens.md)

The interface is HTML + CSS + JavaScript with no frameworks or libraries (except Chart.js for the
charts). This guide is for anyone adding a component or a new theme.

## Files

| File | Contents |
|---|---|
| `web/style.css` | Base palette, semantic tokens and the report layout |
| `web/themes.css` | The 6 themes: each one redefines only the base palette |
| `web/features.css` | Newer components: locators, replay, chips, drawer, mocks, live indicator, metrics, settings |
| `web/tracereports_features.js` | Reusable controllers (`window.TraceReportsFeatures`) |
| `web/app.js` | The application: state, routing, rendering and the API connection |
| `web/i18n.js` / `web/i18n.en.js` | Language: translates the interface in the DOM / Spanish → English dictionary |

## Two layers of variables

1. **Base palette** (`--bg`, `--surface`, `--text`, `--primary`, `--pass`, `--fail`…): each theme
   defines it in `themes.css`.
2. **Semantic tokens** (`--color-*`): defined **once**, on `body` in `style.css`, from the base
   palette.

The tokens are declared on `body` and not on `:root` on purpose: the theme is applied with
`body[data-theme="…"]`. On `:root` they would be computed from the default palette and would not
change with the theme.

**Rule for new components:** use only `--color-*`, never hard-coded colors or the base palette.
That way the component works in all 6 themes without touching `themes.css`.

| Token | Use |
|---|---|
| `--color-bg` | Page background |
| `--color-surface` | Cards and panels |
| `--color-surface-raised` | Surface inside a card (light code blocks, cells) |
| `--color-surface-hover` | Row and button hover |
| `--color-border` | Borders and separators |
| `--color-text` / `--color-text-muted` | Primary / secondary text |
| `--color-heading` | Headings |
| `--color-primary` / `--color-on-primary` | Main action and its text |
| `--color-accent` / `--color-accent-soft` | AI accent and soft highlight backgrounds |
| `--color-success` / `--color-danger` / `--color-warning` / `--color-info` | Statuses (pass, fail, warning, info) |
| `--color-focus` | Keyboard focus ring |
| `--color-highlight` | Active row or step (replay, locator) |
| `--color-canvas` / `--color-canvas-text` | Replay canvas: dark in every theme so the screenshot stands out |
| `--color-overlay` | Backdrop behind the drawer |
| `--color-code-bg` / `--color-code-text` | Code in mocks and snippets |
| `--color-tooltip-bg` / `--color-tooltip-text` | Tooltips (flaky sparkline, tips) |
| `--shadow-elevated` | Drawer and floating elements |
| `--radius-lg` | Large containers (player, drawer) |
| `--motion-fast` / `--motion-base` / `--ease-out` | Animation durations and easing |

With `prefers-reduced-motion: reduce`, animations (step entry, live indicator pulse, drawer) are
turned off.

### Adding a theme

Copy a `body[data-theme="…"]` block from `themes.css`, change the base palette and add it to the
`THEMES` list in `app.js` (the theme picker is built from it). The semantic tokens recompute on
their own. Specific rules are only needed if the theme changes the typography: Pixel, for example,
enlarges code in `features.css` because its bitmap font reads small.

## Components

| Component | Where it shows | Classes |
|---|---|---|
| AI Locator Recommender | Inside *AI Failure Triage* of a test that failed on a broken selector | `.loc-card`, `.loc-failed`, `.loc-item`, `.loc-item.ai-pick`, `.step-loc-flag`, `.bbox-view` |
| Time-Travel Replay | The test's *Replay* tab (when it has screenshots) | `.replay-panel`, `.tt-player`, `.tt-scrubber`, `.tt-step` |
| Flakiness | Test list and test header | `.flaky-chip`, `.cf-tip`, `.cf-spark` |
| Network drift | Test list and test header | `.drift-chip`, `.cf-table`, `.cf-bar` |
| Mock / stub | Failed calls in *Network* and the test's error panel | `.cf-btn-mock`, `.cf-code`, `.cf-masked` |
| Live | Top bar and the floating auto-scroll button | `.live-pill[data-state]`, `.autoscroll-toggle`, `.row-enter` |
| Tips | Tabs, navigation, filters and action buttons | `.cf-tooltip`, `data-tip` attribute |
| Metrics | *Metrics* menu | `.kpi-grid`, `.kpi`, `.m-card`, `.hbars`, `.m-table` |
| AI analysis | *AI* menu | `.ai-top`, `.incident-card`, `.rec-pill`, `.ai-fail` |
| Escalate | *Escalate* menu | `.esc-layout`, `.esc-share`, `.esc-card` (fixed light design: it is shared as an image or email) |
| Settings | *Settings* menu | `.set-card`, `.prov-grid`, `.prov-card`, `.field`, `.ro-banner` |
| Drawer | Shared side panel (bbox, drift, mocks) | `.cf-drawer-root`, `.cf-drawer` |

## `window.TraceReportsFeatures`

| API | Description |
|---|---|
| `copyText(text, button)` | Copies to the clipboard and shows "Copied!" on the button (with a fallback for `file://`) |
| `download(name, content, type)` | Downloads a file generated in the browser |
| `Drawer.open({title, subtitle, body, onClose})` | Opens the side panel and returns the body container. `Drawer.close()`, `Drawer.isOpen()` |
| `sparkline(statuses)` | Mini PASS/FAIL bar chart (oldest to newest) |
| `new TimeTravelPlayer(container, steps, {start, index, speed, onChange, onLightbox})` | Step player: `play()`, `pause()`, `toggle()`, `go(i)`, `setSpeed(1\|2)`, `setSteps(steps, i)`, `destroy()` |
| `new LiveStream(url, {onEvent, onStatus})` | SSE client with reconnection: `start()`, `stop()`. States: `live`, `reconnecting`, `off` |
| `MockGenerator.open(connection, {subtitle})` | Drawer with the stub in Playwright (Python/JS), Cypress and WireMock |
| `maskValue(value, key)` | Masks national IDs (RUT), passwords, tokens and the like |
| `Tips.init()` | Help tooltips for every element with `data-tip` (plus `data-tip-title`, `data-tip-keys` and `data-tip-pos="right\|top"`). Shown on hover and on keyboard focus |

## Accessibility

- The drawer is a modal `role="dialog"`: it traps focus with Tab, closes with Escape and returns
  focus to the button that opened it.
- Replay: `Space` plays or pauses, `←` / `→` change step, `Home` / `End` jump to the first or last.
- The live indicator is `role="status"` with `aria-live="polite"`; auto-scroll is a
  `role="switch"` with `aria-checked`.
- Flaky chips are focusable and show their tooltip on focus too.
- To explain what an option does, use `data-tip`, not `title`: the native `title` is slow to
  appear, unstyled and unavailable on keyboard and touch screens.

## Live indicator

It only appears when the run being viewed is still running:

| State | Looks | When |
|---|---|---|
| `live` | Red, pulsing dot (recording) | Run in progress and SSE connected |
| `reconnecting` | Yellow, "Reconnecting…" | The connection to the server dropped; it retries on its own |
| `stale` | Grey, "No activity" | The run is still open but has received no steps for more than 5 minutes (the test process ended without closing it) |

Below 480 px only the dot is shown.

## Language (i18n)

The interface is written in Spanish in `app.js` and `index.html`. `i18n.js` translates it **in the
DOM** as it renders (text plus the `placeholder`, `title`, `aria-label`, `data-tip`… attributes),
using the dictionary in `i18n.en.js`. Templates don't need to change.

- New UI text → add its translation to `EN.exact` (whole phrase) or, if it carries data (numbers,
  names), a pattern in `EN.patterns`.
- Anything outside the DOM (chart labels drawn on canvas, generated code) uses
  `TraceReportsI18n.t("text", {variables})`.
- User content is never translated: test names, steps, errors, code and the AI's text. Mark any
  other element that must stay as-is with `data-no-i18n`.
