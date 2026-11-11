# Changelog

All notable changes to TraceReports. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and the project uses
[Semantic Versioning](https://semver.org/) (pre-1.0: minor versions may change behavior).

## [Unreleased]

## [0.8.0] - 2026-11-11

Python client 0.6.0, JavaScript client 0.5.0 and Java client 0.5.0; the Go client does not change. Validated
with a team that runs TraceReports in Docker on Windows with suites of 16 workers.

### Security

- **Sign-in page: no redirect to another site through `next`**: a `next` such as `/<tab>/evil.example` passed the
  check and the browser, which drops tabs and newlines from URLs, went to `//evil.example`. `next` is now
  resolved like the browser does and must be on the same origin.
- **JavaScript client: the cancel control file goes in a fresh private folder** (`mkdtemp`, mode 0600) instead of
  a guessable name in the temporary folder.
- Values from requests are kept on one log line in the new log entries (cancel, sign-in, slow requests, retention).
- Fixed a data race when the router was built from several goroutines (tests only: the server builds it once).

### Fixed (Live: the map card under the fixed cards, the anomaly cut to one line)

- **The card of a test on the execution map is never covered**: it now opens in the browser's top layer, so the
  fixed cards of the map (ingest, anomaly, lanes) no longer hide it; it opens below and to the right of the
  point, or above/left when it does not fit in the window.
- **The anomaly card shows the error in two lines** instead of one cut short, breaking at `/`, `.`, `_`, `:`
  or `]` of the locator, with "…" at the end; the full text is in its tooltip.

### Changed (Maintenance: choose any number of days)

- **Keep** and **Clean up now…** share one days picker: 1, 2, 3, 7, 14 and 30 days, 2, 3 and 6 months, 1
  year (and everything, for Keep), plus **Other…** to type any number of days (1 to 3650). In Clean up now the
  preview follows what you type, and the cutoff date and the oldest run are shown next to it.
- **A cleanup cut halfway is recorded**: "Last cleanup" said 0 runs after a retention that deleted 875 and then
  failed; it now shows what was deleted and the error.

### Fixed (deleting and compacting failed with "disk full" while the disk had space)

- **SQLite's temporary files now go next to the database** (`<DATA_DIR>/tmp`), not to `/tmp`. In the container
  `/tmp` is a 256 MB tmpfs: deleting runs with a lot of evidence (the statement journal of the DELETE), the
  automatic retention and compacting a database of several GB (VACUUM) needed more than that and failed with
  "database or disk is full", shown as "the server's data disk is full" while the data disk had hundreds of GB
  free. Leftover temporary files of a previous run are removed at startup.

### Changed (Maintenance: compacting the database apart from deleting)

- **Deleting no longer compacts the database right away**: compacting (VACUUM) rewrites the whole file; on a
  database of several GB it took minutes with the only writing connection held (clients waited, "Deleting…"
  stayed on screen) and needed as much free disk as the database. Now a cleanup or bulk delete answers when the
  runs are deleted and leaves the compaction pending; it runs by itself when no run is in progress and the
  clients have sent nothing for 15 min (at most once every 20 h), or by hand with **Compact now…** in
  Maintenance > Storage, which says how much it would give back, how much free disk it needs (and does not
  start without it) and whether runs are in progress, and shows "Compacting…" without a second start. What was
  deleted is still overwritten with zeros right away (secure_delete).
- A cleanup or bulk delete that fails halfway still removes the files of the runs it already deleted.

### Fixed (Maintenance: cleaning up a big database)

- **Deleting runs no longer scans the whole idempotency table for each run**: deleting a run deletes its
  idempotency keys (one row per client request, with its response), and that column had no index. On a database
  of several GB every cleanup or bulk delete held the only writing connection for minutes. New index
  `idx_idempotency_run` (built once at the first start, which can take a moment on a big database).
- **Cleanups and bulk deletes go in batches of 25 runs**, each in its own transaction with a passive checkpoint
  in between: the clients keep writing during a long cleanup and the WAL is reused instead of growing with
  everything deleted at once (it could fill a tight disk).
- **"Clean up now" shows it is working**: a spinner, a status line ("deleted in batches… it may take a few
  minutes"), the days, Cancel and the top button locked, and a second click does not send it again. An error
  keeps the preview on screen and says the cause and its reference.

### Changed (Maintenance: client connections and errors)

- **Client connections no longer is an endless list**: a summary on top says how many executions had delivery
  problems, how many slow requests, stalls and rejected credentials (a click opens that list); each list is folded
  and shows 10 rows with "Show more", and what is open stays open on refresh. Runs with delivery problems are
  **grouped by execution** (with 16 workers one execution left dozens of identical rows): how many runs, what
  happened and on which machines, and the execution opens to show its runs.
- **"internal error" now says what to do and how to find it**: every failed request answers with a reference that
  is also in the server log next to the full error; a full data disk, a busy database (503 with Retry-After), a
  data folder that cannot be written or a damaged database file are named, in the UI's language. Nothing of the
  database goes out to the browser.

### Changed (Live: clicking the execution map)

- **A click shows at once**: the chosen particle gets a ring that opens and fades right away, before its detail
  arrives. With a slow server, people clicked again and the second click counted as a double click that took
  them out of Live.
- **Two quick clicks on a new particle only choose it**; a double click opens the test only on the particle
  already chosen. **Ctrl/⌘ + click** opens any test directly, like a link, and the side panel says so next to
  **Open the test**. The map's hint and help line now say "Ctrl + click: open the test".

### Fixed (heavy suites: tests shown as Interrupted, the server waiting on uploads, Live showing "Loading…")

Seen at a client with 16 workers: the stall dumps showed the database free and the server waiting for the
bytes of network uploads (batches of up to 8 MB of JSON) that came in slowly, while the client retried them in
full after 10 s. Each worker sends in order, so the test results waited behind those uploads; when `end_run`
ran out of time the run closed with them still running and the tests showed as Interrupted (the local report
had them fine).

- **The client sends test results before heavy evidence** (Python): creating and closing tests, steps,
  screenshots and closing the run go first; network, DOM, console and attachments after. A slow upload waits
  for its retry on its own instead of holding the queue.
- **Compressed uploads**: the server accepts `Content-Encoding: gzip` (with a limit on the inflated size) and
  says so in every response; the Python client compresses JSON over 8 KB only after seeing it, so a new client
  still works with an older server. `TRACEREPORTS_GZIP=0` turns it off.
- **Network batches of up to 2 MB** (were 8 MB) and each queued upload waits according to its size before
  retrying, so a big batch is not uploaded again while the first copy is still arriving.
- **Heavy reads no longer block the clients**: Maintenance > Storage (it took the only database connection
  for up to 20 s) and the Live view now read through read-only connections (SQLite WAL), so opening them during
  a suite does not make the clients wait to save results.
- **Live no longer goes blank with "Loading…"**: while the execution was being refreshed the view was emptied,
  and a click on the map during a slow answer showed only "Loading…". What is on screen stays until the new
  data arrives.

### Added (server stall watchdog)

- **Stalls are caught while they happen**: the slow request log only writes when a request ends, so a server
  held by one task (the single database connection taken) left no trace until it let go. A watchdog checks the
  requests in progress every 5 s; when several have been waiting for more than 10 s it logs `server stalled`,
  saves once a dump of what every server task is doing with the state of the database connection
  (`DATA_DIR/diagnostics/`, the last 10) and, when it ends, how long it lasted and how long requests waited for
  the database. **Maintenance > Client connections** lists the stalls; admins download the dump. Seen at a
  client: 16 workers starting at once waited 40 s for the database while a background task held it.

### Fixed (Maintenance: closing or deleting runs in bulk)

- It could take a while with nothing on screen, so people clicked again: now the button says "Closing…" with a
  spinner, a status line says what is happening, the form, the checkboxes and the bulk buttons are locked, and a
  second click (or Enter) does not send it again.

### Changed (client feedback, 10-11: notifications, the execution map, the side panel)

- **New execution notifications, one per execution**: a launcher that opens one run per file (or many
  parallel workers) used to raise the notification with every run, and "Not now" came back with the next one.
  Now it is one notification per execution ("An execution started · 12 runs"); "Not now" mutes that execution,
  **Mute 15 min** mutes all of them (with Undo), and a **bell** in the top bar always shows whether they are on
  or muted (minutes left) and turns them back on, also per execution. Muted items expire by themselves (15 min /
  12 h). The "run created" event now carries the run's name and execution, so a burst of runs is never missed.
- **Execution map**: "now" is drawn in the text color (dashed), not in the red of failures; with a filter, the
  dimmed particles can no longer be hovered, clicked or reached with the arrow keys, and the click area is a bit
  larger; the hover card takes the color and icon of the test's status, with its duration, how it compares
  with normal (highlighted when slow), times and where it ran.
- **No flicker when switching tests in the side panel**: it used to empty the panel and replay the trace row by
  row on every pick. Now the header shows the chosen test at once, the previous body stays dimmed with a thin
  progress bar until the new one arrives, the row-by-row entry only plays when the panel opens, tests already
  seen show instantly (cached) and hovering a test in the map prefetches it. Measured switching tests with the
  panel open: 12-18 restarted animations and empty frames → 4 and none.

### Added (Maintenance > Client connections)

- **How the evidence arrived, visible in TraceReports**: the Python client reports it when it closes a run
  (created late because the server did not answer, the cause, the address it connected to, retries, lost
  events, machine and version). A run that arrived late says so on top of its report, and **Maintenance >
  Client connections** lists those runs, the slow requests the server saw (from the `slow request` log) and,
  for admins, the addresses with rejected credentials. API: `delivery` in `PATCH /runs/{id}/finish` and
  `GET /maintenance/connections`.

### Fixed (client feedback, 10-11: "POST /api/v1/runs failed: timed out" at random)

- **A short stall no longer costs the run** (Python client): when the run could not be created because the
  server did not answer, the client records locally as before, and at the end, if the server answers, it
  uploads the recording by itself (`tracereports push`, idempotent): the run reaches its execution with its
  tests. `TRACEREPORTS_OFFLINE_PUSH=0` turns it off. Creating the run now retries for up to 30 s (was 15 s);
  a server that is down (connection refused) still switches to local at once.
- **The warning says what happened**: "the server did not answer within 30 s", "wrong token" or "server down
  or unreachable", instead of "server down or wrong token" for everything.
- **Slow requests are logged** (server): any request over 2 s is logged with its method, path, status and
  duration (`slow request`, never the query). A client timeout without that line means the request never
  reached the server (Docker Desktop's port forwarding, a proxy, the network).
- **Where it got stuck**: the client's timeout warning says where it connected and how long it waited
  ("connected to 10.0.0.25:8080 in 3 ms, no answer in 10 s"), and a connect timeout says how long it tried.
  `scripts/watch-health.ps1` checks `localhost:8080/healthz` every 10 s on the Windows machine that runs
  Docker and logs failures and slow answers: with the server log it tells TraceReports, Docker on that
  machine and the network apart.

### Added (AI: failures in cascade are one incident)

- **Failures in cascade are one incident**: when the first test of a file fails and the later tests of
  that file fail because of it (their error names it, or says that data is missing or that the step was
  skipped because a previous one failed), the run diagnosis shows one incident *In cascade* with the cause
  of the first test, instead of one incident per test. A test that passes in between breaks the chain. A
  first test that ran far longer than the rest is shown as hung, and a page that was blank at the failure
  (DOM snapshot with nothing visible, or a single-color screenshot) is added as evidence. The tests of a
  cascade no longer count as independent failures for "the environment was probably down".
- **Local rules engine: `blank_page`** (`INFRA_ERROR`): a page that never finished loading is not reported
  as a broken selector.

### Changed (AI: incident grouping)

- **Error signature without the line number**: the same error from several lines of a file is one
  incident (the location lists the lines). The lines of a chained Python exception ("During handling of
  the above exception…") no longer become the title nor join unrelated errors. Incident keys change
  once: runs analyzed before this version are not matched as recurrences of the new ones.

### Changed (Live, round 4: lighter, no repeats, group by error, readable numbers)

- **Search no longer freezes the page**: the table redraws once you stop typing (140 ms), not on every key,
  and it draws 100 rows at a time ("Show 100 more (n hidden)"; a cut group says "and n more in this group").
  A 1,100-test execution used to draw every row at once.
- **No repeated information**: "Recent activity" and the field's "Latest signals" repeated the LIVE ticker,
  and the field's PROGRESS box repeated the header; they are gone.
- **Group by error**: one row per cause (the signature with its selector shortened, e.g. "TimeoutException
  in fa-plus-circle") with the tests it hit, the most affected first and "No failure" last.
- **Trace Buddy shrinks in Live** so it does not cover the table (full size on hover or when its chat is
  open); the page title shows the execution; the ticker items no longer take Tab stops.
- **Visual fixes**: multi-line tooltips keep their lines, close minimap marks merge into one mark with a
  count, test links no longer hide "_" under the underline, "1 new" / "1 already failing" in singular,
  better contrast for "first time" and the ticker times, backend p95 under one second in ms (it said "0 s"),
  and error signatures with a long XPath show the shortened selector (the full one in the tooltip).
- **Times in 24 h and numbers as written in each language**: "×1,7" and "1,3%" in Spanish, "×1.7" in English.

### Changed (Live, round 3: what filters shows, new failures, error details, closing summary, shortcuts)

- **A chosen status now shows in the timeline**: it used to only fade the other bars, so choosing Pass or Fail
  looked like nothing happened. Now the matching bars stand out, the lanes without a match fold into one line
  ("9 lanes without matches · show"), the minimap counts only the matches and the title says "3 of 234 tests
  match". Lane names keep one color; a lane with problems carries a counter with its explanation, and hovering
  a lane highlights its tests in the table.
- **New failures**: for each failed test, its history says how it ended in its previous execution: "New" if
  it passed (a regression of today), "Already failing" if not. Shown in Needs attention, the table and the
  error groups; "new" is a filter of its own.
- **Needs attention** has its own search box and status filter (the summary chips), without touching the table.
- **Grouped errors** open in place with the tests they belong to (name, file, worker, time, new or not); the
  magnifier filters the table, the map and the timeline by that error.
- **The side panel** shows the test's lane, with what ran before and after it.
- **Closing summary**: when the execution ends, a card with its duration, results, new failures and slow
  tests, and "Escalate the failures" / "See the report".
- **No more flicker, and a lighter page**: Live rebuilt the whole view (11,000 elements in a 1,200-test
  execution) with every event, restarting its animations and freezing the page a third of the time. It now
  patches only what changed (unchanged elements stay the same nodes), formats times with a cached formatter
  (creating one per call was the most expensive part), measures the ticker only when it changes, draws narrow
  timeline bars without text, and the field redraws its canvas only when it moves. Measured with a running
  1,200-test execution over 20 s: frozen time 7.8 s → 1.5 s, longest freeze 449 → 125 ms, restarted
  animations 687 → 100, 27 → 55 frames per second.
- **Grouped errors show their new failures while closed** ("1 new · 2 already failing"), signatures with new
  failures go first, and inside, the new ones are listed first with their mark; long lists show 10 and "see all".
- **Keyboard**: J/K go to the next/previous test that needs attention (opening its details), O opens the
  test, P pauses, C toggles a compact mode (no diagnosis cards nor charts, for a small screen or a TV, and
  remembered), ? shows the shortcuts; a double click on a row opens the test.

### Added (Live: the execution field, an observability console)

- **Execution field**: the execution as a torus of particles that reads like a clock, and every bright
  particle is a real test. The angle is time (start on top, clockwise, pauses compressed) and each worker or
  process is one turn around the tube; the particle sits where the test ended (or where it is, on the "now"
  hand), grows with how slow it was against normal, and failures glow. The tube turns slowly on itself (time
  never moves) and stops under the mouse so a particle is easy to hit: hover shows it with its trail, a click
  or Enter opens its detail panel, the arrow keys walk the tests. The rays around it are how many tests ran at
  each moment, colored by how the ones that ended there finished; the faint dust only gives the torus volume
  and is not clickable. Around it: channels (tests, failures, network, workers) that filter or group, the
  real ingestion (events of this execution received through `/api/v1/stream`, with the last minute in 5 s
  bars; "active" only then, never for a finished or silent execution), progress, the dominant anomaly
  (filters by its error signature), latest signals and error pressure. Each stream event fires a pulse from
  its channel to its test. One canvas that survives the view's redraws, at 30 fps at most, stopped
  off-screen, with "Pause", and static with `prefers-reduced-motion`. The grid of squares stays as the
  alternative view.
- **The field can be handled**: drag to rotate and tilt the torus, Shift + drag to move it, zoom with the
  buttons, the keys or the wheel (Ctrl + wheel in the page, the plain wheel in full screen), full screen with
  F (Esc returns), Reset (0) back to the usual view. A double click on a particle opens its test; a click
  keeps opening the side panel. The ticker events at the top open their test too (the ticker stops under the
  mouse).
- **Pause from the field**: "Pause view" (or Space) freezes the field and the data to look into something,
  while ingestion goes on in the server: the field says "PAUSED · ingestion goes on: N new events", the count
  rises live, and "Resume (+N)" catches up.
- **"Another execution started" for every file of the same execution**: runs did not carry their
  `execution_id` in `/api/v1/runs` and `/api/v1/runs/{id}` (only Live read it), so a launcher that opens a run
  per file triggered the notice with each file, covering the screen. Runs now carry `execution_id` and
  `planned`, and the notice only fires for a different execution.
- **The exported report carried a fixed list of files**: a script added to the interface would be missing
  from the ZIP. A test now checks that every script and stylesheet `index.html` loads is exported.

### Changed (client feedback, 09-11, run 1084: the Live view)

- **Visible answer to every click**: "All / With problems" and "Group by" were loose chips and buttons that
  gave no sign for a moment, so it looked like nothing happened. They are now segmented controls whose
  indicator slides to the choice in the same frame as the click; the table, map and timeline dim with a thin
  progress bar while the list is recomputed, and a status line says what is shown ("Showing 6 of 234 tests ·
  Only with problems · Grouped by file", with the number counting). Measured with the 270-test suite and
  with 327 and 518 tests: click to first paint 5-17 ms, click to the redrawn list 82-281 ms.
- **"P1, P2…" did not say anything**: they are lanes inferred when the client does not report a worker. They
  are now "Process 1, Process 2…", with the explanation in their tooltip and a legend under the timeline
  (also for "×1.7 = 1.7 times normal"). "vs p50" is now "vs normal" and the health strip says "typical ·
  slow duration" with what p50 and p95 mean.
- **Long test names**: the ellipsis goes in the middle, so the start (`test_004_`) and the end, which tell
  one test from another, stay visible; full name in the tooltip and a copy button; the Test column widens by
  dragging its edge (or with the arrow keys) and "Names on 2 lines" shows them whole. Also in the Gantt bars.
- **Timeline without empty space**: the axis starts at the first test, not at the start of the execution;
  long pauses with no test running are compressed to a narrow cut (≈) that says how long they lasted; a
  "now" line moves with the execution; Ctrl + wheel zooms around the pointer, "Fit to window" goes back;
  idle lanes can be hidden.
- **Tooltips everywhere**: every control, icon, column, chip and status says what it is and what it does, on
  hover (300 ms) or on keyboard focus, with its shortcut when there is one; a unit test checks that no button
  of the view is left without one.
- **Icons instead of words**: an own status icon set (same grid and stroke) for Pass, Fail, Warning, Skip,
  Running, Turtle (a drawn turtle, not the word or an emoji), No signal and Interrupted, also inside the Gantt
  bars and on the minimap; icons for grouping, zoom, fit and folding.
- **More life, without noise**: a ticker with the latest events (stops on hover), a clock, the execution map
  (one square per test, a click opens it), sparklines in the health strip, a moving shine on running bars and
  the progress bar, a pulse of the new color when a test changes status, entering rows slide in, a slow pulse
  for "no signal" and 150-250 ms transitions; everything off with `prefers-reduced-motion`.

### Fixed (client feedback, 09-11, run 1084: Inspector)

- **Inspector: icons were missing, or showed as loose words**: the structural snapshot did not carry
  pseudo-elements nor fonts, so Font Awesome icons (drawn in `::before`) were not there, Material ligatures
  showed their word ("notifications", "vaccines") and inline `<svg>` were an empty box. The Python client now
  draws each icon-font glyph in the page with the font it already loaded and sends it as a small PNG, once per
  distinct icon (`glyphs`, referenced by `icons` on each node); inline `<svg>` travel with their colors and
  `<use>` resolved; same-origin images up to 64 px too. The server keeps only real PNGs within a budget and
  rebuilds each SVG from a whitelist of shapes and paint attributes (no scripts, events, links, styles or
  `url()`); the viewer shows them as images, never as HTML. On a page modeled on the client's (Font Awesome 6,
  Material Icons, an SVG sprite and a PNG icon), the Inspector went from 0 to 23 icons drawn, +4 ms of capture
  and +23 KB per page. Older snapshots and the JS and Java clients keep the boxes as before.

### Fixed (client feedback, 06-11: Inspector)

- **Inspector: elements on top of each other and black areas**: it painted by tree depth and ignored
  opacity, so a Bootstrap modal stayed behind the page and its backdrop came out solid black. It now paints
  like the browser stacking contexts (z-index and document order), applies the opacity of the ancestors,
  clips nodes to their scroll panels and wraps multi-line text. The Python client captures zIndex, overflow
  and line breaks; older snapshots fall back to document order.
- **Inspector: "the selector could not be extracted" when the backend failed**: when a backend call failed
  just before (no answer, 5xx, 408, 429) the locator report was empty, so the Inspector blamed the error
  text. The report now carries the selector and that call (method and endpoint, never the query), with no
  replacements; the Inspector says "the backend answered 504 just before the failure" and suggests no
  look-alike element. The locator card and the "broken locator" mark still stay hidden in that case.
- **Inspector: the element count did not change between pages**: it showed the count of the first page
  built; it now shows the one of the page being viewed.
- **The exported report (ZIP) had no Inspector**: the pages now go in dom/ (they open from file://), capped
  at 64 MB with failure pages first, then WARNING pages, then steps; the Inspector says what did not fit.
  Compare with the last PASS is disabled there, with the reason.
### Added (client feedback, 06-11)

- **Personal data in DOM snapshots**: RUTs, phone numbers and e-mails are masked in the page snapshot
  (node text, text attributes, the locator elements, URL and title) as `<rut>`, `<telefono>`, `<correo>`;
  the locator attributes (id, class, data-test*, placeholder, aria-label) stay and dates are left alone. `TRACEREPORTS_REDACT_PII=off` turns it off.
- Python client: `TRACEREPORTS_DOM_STEPS=on-problem` keeps the step pages in memory and sends them only
  when the test fails or had a WARNING, plus a baseline of passing tests when the server has none from the
  last 7 days. `DOM_STEPS=1` keeps the pages of every test, also the passing ones, and `DOM_ON` does not
  limit it: now documented.
### Added (client feedback, 06-11: Live)

- **Cancel the whole execution**: the cancellation is kept per execution for 24 h, so the runs a launcher
  opens later (a run per file, queued processes) are cancelled too; Live says it stops when the current
  test ends.
- Python client: `TRACEREPORTS_PLANNED=off` stops the plugin from reporting planned tests, for a custom
  launcher that creates a run per file and reports each one (documented with the bridge pattern).

### Changed (client feedback, 06-11: Live redesign)

- **Live, built for large executions** (a suite of 270 tests in 86 runs made the lanes take three
  screens): a compact execution selector; status tiles with large counts that filter the table (Fail,
  Turtle, No signal…) and a progress bar with the mix of states; "Only with problems" (fail, interrupted,
  turtle, no signal), on by default with more than 20 runs or lanes; group by file, worker or status,
  ordered by severity, with the files without problems in one line; search over test, file, worker and
  step, which keeps focus and text while the view refreshes.
- **Timeline**: a minimap of the whole execution (tests running at each moment, stacked by status) where
  dragging zooms the lanes, plus −, + and Fit; with "Only with problems", the lanes without problems fold
  into one line. Without `worker` (a run per file) the lanes are reused like processes: each run takes the
  first lane that is free, so 86 runs in 12 processes show as 12 lanes; lane names drop the common prefix.
- **Side panel**: clicking a test (row or bar) shows its status, worker, duration against p50, the last 5
  steps and the latest screenshot without leaving Live, kept up to date; Escape closes it and "Open the
  test" goes to the detail. The last step is one line without URLs (the full text is in the tooltip).
- Phone: tiles in three columns, only the minimap, the table as cards.

### Added (Live: observability)

- **Execution health** under the tiles: failure rate of what finished (and the difference in points against
  the previous execution of the same project), duration p50 · p95 and pace (tests per minute, the last 5
  minutes while it runs).
- **Diagnosis cards**: errors grouped by signature (the first line without Playwright's call log and with
  variable numbers as N, the selector kept; a click filters by it), the tests slower than their history (and
  the ones with retries), the **backend of the execution** (endpoints of all its runs, errors first, with p95
  and a link to a test's network; new `GET /api/v1/live/endpoints?key=`) and the recent activity.
- **Facets** beside the table (status, error, file, worker) with their counts, several at once; the active
  filters show as chips that are removed with a click. The table sorts by status, name, duration or against
  p50 (a flat list; the third click groups again).
- **Anomalies on the minimap**: a ✕ for each failure and a ! for a test without signal, where they happened;
  they open the test.
- **The test trace** in the side panel (a waterfall like APM): its steps and its backend calls on one
  timeline, failed calls in red with their status and the moment of the failure marked; static resources
  only on request; tabs for the last steps and the screenshot.
- **Pause**: the view stops updating to analyze it (the data keeps reaching the server); resume catches up.
- **Charts**: results over time (columns stacked by status), the cumulative failure rate (a line, with the
  previous execution as a dashed reference) and the duration distribution (a histogram with p50 and p95
  marked). Every column and point has its tooltip (also from the keyboard) and "Show data" opens the table.
- **Execution picker**: each execution with its start time, how long ago it started or ended, whether it is
  running, recent (< 1 h), from today or earlier, a progress bar and its failures and tests without signal.
  With more than 8 executions it has a search box and groups them by age; arrows, Enter and Escape work.
- **Motion**: the view rises in on first load, a count that changes counts up and pulses, a test whose
  status changed flashes, new activity slides in, a new failure drops onto the minimap, the side panel slides
  in and the trace bars grow. Only what changed animates (the view redraws on every event) and nothing moves
  with "reduce motion".

### Added (sign-in page)

- **Sign-in page** (`/login/`) instead of the browser's HTTP Basic box: user, password (with show/hide and a Caps
  Lock warning), **Remember me**, a single message for a wrong user or password, a countdown when the address is
  blocked, and back to where you were (with its `#`) after signing in; never to another site.
- **Sessions**: an `HttpOnly`, `SameSite=Strict` cookie (`Secure` with HTTPS); the database keeps only a SHA-256 of
  the token. It expires after 30 min without use or 8 h anyway (`TRACEREPORTS_SESSION_IDLE_MIN`, `_MAX_HOURS`); Remember
  me keeps it 7 days (`_REMEMBER_DAYS`). Requests the page makes on its own do not keep it alive. Changing the
  password ends every session. Sign-in failures share the brute-force brake with HTTP Basic and the tokens, and
  another site cannot sign a browser in (same-site check). The admin and the read-only login keep their roles.
- **In the app**: the user and **Sign out** in the top bar, a warning 2 min before the session expires (**Stay
  signed in**) and, when it expires, back to the sign-in page saying so. A 401 of the UI no longer opens the
  browser's login box; API clients still get the HTTP Basic challenge and tokens work as before.
- **The page builds itself** (like a design tool with several people on it): seven agents, each with its cursor,
  take the tasks whose dependencies are done and work in parallel: rulers, guides with their coordinate,
  rectangles dragged with their size and painted from the grid agent's palette (a drop of color travels from the
  swatch), the grid agent sketching the trace of gw0…gw5 with the color of each status, text typed letter by
  letter, the QA sweep, and a final render that turns the painted mock into the real
  page; smoke where each piece lands and
  Trace Buddy telling what happens ("my team of agents is building your sign-in") and how many agents built it.
  Any key, click or paste finishes it at once (nothing that moves blocks signing in); it does not repeat in the same
  tab nor with "reduce motion". Animations with Motion (Framer Motion for plain JavaScript, vendored in
  `web/vendor/motion`).
- **Skip it, or never see it again**: while the page builds, a bar at the bottom (like a design tool's
  collaboration bar) shows the agents, the progress and **Skip** with its `Esc` shortcut, plus **Don't show again**,
  remembered in that browser; **↻ replay** next to the artboard name brings it back.
- **Sign out asks first**: the session button is the last one in the top bar and opens a small confirmation
  (Cancel has the focus; Esc or a click outside cancel it).
- **Trace Buddy talks**: a speech bubble with a header, a brief "typing…" and highlighted words. Showing the
  password gives him a fright (he jumps back, hands up) and then a cheeky look (red slanted eyes, little horns,
  rubbing his hands) with a line like "Did you just show me the password?"; hiding it again, he covers his eyes and
  says he is not looking. He greets the user by name (escaped) when moving on to the password.
- **Look**: a dark canvas with rulers where six agents (the workers) run tests and leave their trace, the last
  failure selected as in a design tool; Trace Buddy sits on the card, follows what you type, covers his eyes while
  the password is hidden, peeks when the pointer goes to the eye and looks when it is shown. Layers move with the
  pointer; nothing moves with "reduce motion".

### Changed (client feedback, 09-11: Live for monitoring)

- **Needs attention** on top: the tests that failed, stopped sending, were interrupted or run slow (turtle),
  with file, worker, duration and ×p50; one click opens the detail. Without any, one line says so.
- **Filters in the link**: the chosen statuses and the search go in the URL (`&f=fail,warn`, `&lq=`), so a
  shared link opens already filtered. An **All** chip; status chips at 0 stay visible but disabled.
- **Search shortcut**: `/` focuses the search box from anywhere in the view; `Esc` clears it, then leaves it.
- **One table, aligned**: fixed column widths for every file and a **Worker** column when the client sends it;
  **Expand all / Collapse all**. Rows keep their order while their status stays the same (no jumps on every
  event); when a test moves, its row slides to its new place (FLIP) instead of jumping.
- **Readable last step**: one line, XPaths shortened to what identifies them
  (`//div[contains(@class,'modal-dialog')]` → `modal-dialog`) with the full text in the tooltip, and the
  technical dump (Selenium/Chrome `Stacktrace … (No symbol) [0x7ff…]`, Python, Java) moved to a "stack trace"
  badge that opens it below the row.
- **Readable timeline**: the lane label is short and fixed (`gw3`, `P1`) with the current file next to it and
  every file in the tooltip; a bar shows its name only when it fits, has a gap from the next one, a shape
  for its status (✕, !, ▲, turtle stripes) and a tooltip with name, file, status, duration, ×p50 and start →
  end. Clicking a bar opens the detail and scrolls to its row, highlighted. A filter dims the bars that do
  not match.
- **Past executions in Live**: an execution that already left the Live list (it ended more than 2 h ago)
  opens with the same view, by its key (`x:<execution_id>`, `g:<first run>`) or by any of its runs
  (`exec=r:<run id>`), marked as earlier in the picker. **Observability** in the top bar (not "Live", which the running-run notice already uses) opens the run being
  looked at (and the other runs of its execution). An unknown key falls back to the newest execution.

### Fixed (client feedback, 06-11: Live)

- A run without tests yet came as `tests: null` and broke Live.

Python client 0.5.2 (pending release); the other clients do not change.

### Added

- **Live**: what is running now, per execution (runs grouped by `execution_id` or, without it, by context
  and time): progress and ETA with the tests the client collected, *turtle* tests (slower than their p90 or
  twice their median) and tests *without signal* (nothing for `TRACEREPORTS_NO_SIGNAL_MIN` minutes, server
  clock), lanes per worker and a table per file and test with what needs action first and the last step.
  Optional alerts to Teams/Slack (`TRACEREPORTS_LIVE_ALERTS`, `TRACEREPORTS_ALERT_TURTLE`,
  `TRACEREPORTS_ALERT_FAILURES`). API: `GET /live`, `GET /live/execution`, `POST /runs/{id}/plan`.
- **Cooperative cancel**: *Cancel execution…* in Live (admin) marks the running runs; Python clients with
  `TRACEREPORTS_REMOTE_CONTROL=1` see it between tests (`GET /runs/{id}/control`), skip the rest with the
  reason and close the run. Nothing is killed.
- Python client: `execution_id` (`TRACEREPORTS_EXECUTION_ID` or the CI build of Azure DevOps, GitHub Actions,
  GitLab or Jenkins), the planned tests from the pytest plugin (also with xdist) and
  `TRACEREPORTS_DOM_ON=fail,warning` for the page of each WARNING step.
- JavaScript client: `execution_id`, the planned tests from the Playwright Test reporter and, with
  `TRACEREPORTS_REMOTE_CONTROL=1`, the cooperative cancel (the reporter asks the server and a fixture skips
  the tests left). Java client: `execution_id`, the planned tests from the JUnit test plan (a launcher
  listener registered on its own) and the cooperative cancel in the JUnit 5 extension.
- **Inspector: compare with the last PASS**: what is gone, what is new and what changed against the page of
  the same step in the test's last PASS (`GET /tests/{id}/dom/baseline`). WARNING pages are marked.

- **Inspector**: a test tab that rebuilds the page at the moment of the failure from a structural
  snapshot, so a missing element no longer forces reproducing the failure. A swarm of cursors draws the
  page (or *Show instantly*), the test cursor walks the step pages, and the selector that failed is
  marked: a ghost and the most similar element when it does not exist, or the element itself when it
  exists (covered, disabled or too early). Inspect any element, try XPath or CSS on the page, overlay the
  screenshot, and right click for **QA / Automation**: robust XPath (`translate` + `contains`, case and
  accent insensitive), XPath relative to a stable container, stable CSS, code for Selenium (Python, Java)
  and Playwright (Python, JS), a Page Object constant, an explicit wait, and *use as replacement* of the
  failed selector.
- Python client: the failure snapshot is now the whole page structure (`TRACEREPORTS_DOM=basic` keeps the
  old one) and `TRACEREPORTS_DOM_STEPS=1` also keeps the page of each step with a screenshot (once per
  different page, up to 40 per test). Masked elements keep their box but not their text; typed values
  never travel.
- API: `GET /tests/{id}/dom`, `GET /tests/{id}/dom/steps` and `GET /tests/{id}/dom/steps/{seq}`; tests
  report `has_dom` and `dom_steps`. Step snapshots and their labels are encrypted at rest and rotated
  with `tracereports evidence rekey`.
- **Python client timeouts from the environment**: `TRACEREPORTS_TIMEOUT` (seconds per JSON request,
  default 2) and `TRACEREPORTS_RUN_TIMEOUT` (creating and closing the run, default 10, never shorter than
  `TRACEREPORTS_TIMEOUT`), also as `TraceReports(timeout=..., run_timeout=...)`. Creating and closing the
  run now wait up to 10 s per attempt by default instead of 2 s, so a busy server is not taken for a
  down one; all the attempts together take at most 15 s (or one `TRACEREPORTS_RUN_TIMEOUT` if it is
  longer), so a server that accepts and never answers does not hold every process for 30 s.
- **Storage in Maintenance**: the disk of the data folder (in use, free, a warning from 80 % that says how
  much TraceReports takes), the evidence stored (database and files), runs and since when, the growth per
  day and how long the disk would last, what each project takes and files no run uses.
- **Retention from the interface**: *Keep* 30, 60, 90, 180, 365 days or everything, applied every 6 hours
  without a restart (`TRACEREPORTS_RETENTION_DAYS` still wins). **Clean up now…** shows what would be
  deleted and the space it would free before deleting, and the last and next cleanups are shown. API:
  `GET /maintenance/storage`, `GET /maintenance/storage/preview`, `PUT /ui/maintenance/retention`,
  `POST /ui/maintenance/retention/run`.
- **Escalate → PDF**: prints only the report on a clean A4 page (the browser's *Save as PDF*), named
  after the report number.

### Changed

- **Escalate reads as a formal report**: a header with the report number (`ESC-<run>-<test>`, the same for
  the same failure) and when it was generated, a facts table (severity, audience, run, environment,
  result, run date, test, suggested owner), an executive summary, numbered sections and a footer
  "Generated with TraceReports · date · Internal use", in a sober navy palette with color only on the
  severity (the top stripe, the executive summary, the section numbers and bullets: green, amber, orange
  or red). The text, chat, Markdown and email formats carry the report number too.
- **Run it locally without `git checkout`**: the command to reproduce a test no longer starts with
  `git checkout <commit> &&` (it failed with local changes and left a detached HEAD); the run's commit is
  a note to use only if your copy is on another version. Each test also gets the command for its whole
  file or class, first when the error says the test needed an earlier one (empty data, a step skipped
  because a previous one failed): alone it fails for another reason. Same in the test detail, Escalate
  (Development: one shell block with a comment per command), tickets, the Lab capsule and MCP.

### Fixed

- **Ghost runs (Python client)**: when the server took longer than the client timeout to create the run,
  the timeout opened the circuit, the retry never went out and the client switched to local recording,
  while the server did create the run, which stayed *Running* with 0 tests. A timeout waiting for the
  answer is now retried with the same `Idempotency-Key` (the server returns the same run, never a second
  one) and opens the circuit only when the retries run out. A refused or timed-out connection (server
  down) still opens it at once: without a server the client records locally as fast as before.
- **Test history squares**: opening an older run from a square showed only the runs up to it, so you could
  not move forward again; the strip now also shows the later runs (`GET /tests/{id}/history?newer=N`), with
  the one you are viewing outlined.
- **"Watch live" from Metrics** (and from Search, Maintenance or Settings, also choosing a run in Recents)
  loaded the run behind the view and nothing changed on screen; it now opens the run's tests.
- **Recents** did not update while open (a run finishing, a test result, runs deleted in Maintenance): it
  now follows the live events, at most once every 1.5 s, without F5.
- **Locator drift said "There is no page snapshot"** when the snapshot did arrive and simply no element
  fit. The card now tells the cases apart: no snapshot; a snapshot without replacements (a text XPath reads
  as a content or flow check); or a selector that reached the server cut. The broken selector is taken
  whole from the trace when the stored error message (cut at 300 characters) ended halfway, and an XPath
  written between quotes in a team's own message ends at its closing quote. `GET /tests/{id}/locator` adds
  `has_dom`, `dom_elements` and `failed_selector_truncated`.
- **Blank screenshot in Escalate**: when every screenshot of a test is blank (the browser never showed
  the page), the card showed a big white box that looked like an image that failed to load. It now shows
  *The page was blank at the failure* with a link to the screenshot; the summary no longer says the
  screen changed nor asks to update a selector (it explains that the page never loaded), and neither the
  email, the Teams/Slack message nor the ticket attach a blank image.
- **AI escalation with fields the model did not fill**: a small model (e.g. `llama3.1:8b`) returned the
  name of a field as the evidence (`"title"`) and repeated the title as the executive summary. Fields left
  empty, filled with a field name or copying the title are now taken from the template built from the
  evidence; what the model did write stays.
- JavaScript and Java clients: the page snapshot no longer carried masked elements at all (their text,
  label and placeholder went as they were); now they show `•••`, like the Python client.
- Python client: the page snapshot no longer carries masked text (`[data-tracereports-mask]`,
  `input[type=password]`, `TRACEREPORTS_SCREENSHOT_MASK`) inside the text of a container that has an id or
  a role; it shows `•••` instead. Same in the JavaScript (Playwright and Selenium) and Java clients.
- **Test history squares lost newer runs**: the strip was kept for the whole session, so a test opened
  before another run finished kept its old history; going back to it from a square hid the newer runs
  (click the red square and the yellow one disappeared). It is fetched again when a test or run finishes.
- **New-run notice missed**: a run that started while the page was loading or reconnecting was never
  announced; on connecting, the page now announces the running runs it did not know.
- **Trace Buddy asked the same question in a loop** ("which framework?") and could not answer about the
  DOM. The chat now gets the test's tool and language (detected from the run and the error), the page at
  the failure from the DOM snapshot, or the last page the test kept (what the failed selector matched and the clickable element around it,
  loading indicators such as skeletons or spinners and overlays, each with its XPath and CSS), and the
  Inspector; an answer that repeats an earlier one is retried and, if it repeats again, replaced by an
  answer built from the snapshot (the loading indicator and the explicit wait in the test's tool). A
  question about the DOM, a selector or a wait shows *Open in the Inspector*.
- **Selector not found in a team's own messages**: an XPath written without a cue word
  (`TimeoutException en (//i[contains(@class,'fa-plus-circle')])[1]`) is now taken, with its index.

### Security

- **FixLab on a shared server asks before calling the internal network**: the hosts a resend may reach
  come from the network calls the tests upload, so a call planted by whoever has the ingest token could
  aim at an internal system and be resent by an admin without noticing. Now a host on the internal network
  (a private IP or the server's own machine, also a name that resolves to one) that is not in
  `TRACEREPORTS_LAB_HOSTS` shows *This call goes to the internal network* with *Cancel* and *Send anyway*
  before anything is sent; without confirming, the connection is also refused when it is made (DNS
  rebinding). Hosts in `TRACEREPORTS_LAB_HOSTS`, the base URL you type, the server's own machine and the
  CLI (`repro`, `verify`, MCP) do not change. `TRACEREPORTS_LAB_STRICT=1` refuses those hosts instead of
  asking. API: `/ui/lab/send` and `/ui/lab/verify` answer `409 {code: "internal_host", hosts}` and accept
  `confirm_internal`.

## [0.7.0] - 2026-11-04

JavaScript client 0.4.2 (code scanning fixes only); the Python, Java and Go clients do not change. Validated
with a team that uses TraceReports in Docker with a local Ollama model.

### Added

- **QA verdict for runs without failures**: Escalate no longer reports a green run as a plain pass. Rules
  review what each test delivered and say whether it **passed clean**, **with observations** (aborted calls,
  4xx answers, console errors, warnings, skipped tests, tests much slower than their history) or **with
  risks** (passed only on retry, a server error the test did not catch, uncaught JavaScript exceptions,
  unstable history; severity medium). The card, the Teams/Slack/Markdown text and the email show the
  verdict and each signal, with next steps. With an LLM, a finished green run gets a summary of what was
  validated (from the test names, descriptions and steps); the model does not choose the verdict, the
  severity or the evidence, and a text that describes a failure is discarded.
- **Interrupted status**: tests cut before they finished (`Ctrl+C`, a crashed worker, the inactivity close
  or a close by hand) are `INTERRUPTED`, not `FAIL`: they are out of the pass rate, flaky and the release
  gate, are not diagnosed by the AI, and the run stays *Warning*, incomplete. Earlier interrupted failures
  are migrated once.
- **Close a stuck run**: a running run with no data for 5 minutes shows *No activity for X min* with
  *Close run…* (admin), which asks for a reason and records who closed it (`POST /ui/runs/{id}/close`).
- **Maintenance view**: the runs with problems across projects (no activity, running, incomplete, closed by
  hand or for inactivity), filtered by project, environment, branch and age, to close or delete them in
  bulk; the inactivity threshold of the automatic close is set there without a restart
  (`TRACEREPORTS_STALE_RUN_HOURS` still wins). See [Configuration](docs/en/configuration.md#abandoned-runs).
- **Comparison you can read**: *this run* next to the base, a plain-language summary, a before → now table
  per test (started failing, fixed, still failing, status changed, same) and the tests that ran in only one
  of them; pick the base by searching #id, date, branch or state, with the tests each candidate shares.
  *Explain it* asks Trace Buddy about the comparison.
- **Recents**: each run shows its state, #id, project, environment and branch, a results bar, when it ran
  and how long it took, with a filter, tabs for failed and running runs, and keyboard navigation.

### Changed

- **Warning status**: a test that passes with a `WARNING` step is stored as `WARNING` (a pass after a retry
  stays `PASS`).
- **Escalate copy**: *Teams* and *Slack* copy formatted HTML (bold, lists, code) instead of Markdown that
  showed its asterisks; *Copy image* writes the image to the clipboard, also over plain HTTP on the network
  where the browser has no Clipboard API. The share bar has the app icons.
- **Trace Buddy chat**: answers as a senior QA Automation engineer, gives the new selector, wait or code
  first, and asks the model for plain text (small local models cut answers at the first quote of a code
  sample inside JSON). Custom waits that log a bare XPath are classified as a broken locator.

### Fixed

- **Escalate on a run without failures**: the AI was asked to explain "a failed test" and a small model
  invented one (an invalid RUT, a login service down); it no longer happens, and the invented summaries
  cached before are not served.
- **Comparison with another suite** said "no changes" when the runs shared no test, and a test that went
  from `PASS` to `SKIP` was listed as the same.
- **Trace Buddy probe**: waits up to 2 minutes for a local model (Ollama, OpenAI-compatible) that is still
  loading, instead of failing after 30 s.
- **Auto-scroll toggle** hidden behind Trace Buddy; **Release** cards that overflowed with long feature
  names.
- **Code scanning**: the code highlighter of the mocks and cURL panels could hang the tab on a long text
  with many dots and no closing quote (and mishandled strings with an `n`); the view taken from the URL
  can no longer reach an inherited method; the generated passwords have no modulo bias; the search filter
  label is escaped. JavaScript client: trimming the server URL and building the offline folder name no
  longer use regexes that are quadratic on many slashes or dashes (same results).
- **Client READMEs on PyPI and npm**: the links to the guides (offline mode, local report, the language
  switch) were relative to the repository and led nowhere on the package pages; they now point to GitHub.
  Every URL is also on a line of its own, so a viewer that shows the README as plain text no longer glues
  the closing `)).` to it (`selenium.md)).` returned 404).

## [0.6.2] - 2026-10-30

Python client 0.5.1, JavaScript client 0.4.1 and Java client 0.4.1. The server is the same as 0.6.1; it is
released again so the server and client versions stay together.

### Changed

- **Python, JavaScript and Java clients**: their console messages (the pytest plugin, the Playwright reporter,
  the JUnit extension, `python -m tracereports.resend`, warnings and errors) speak English and only use ASCII,
  so the Windows console no longer shows `atenci?n`. The Go client already did. The texts written into the
  report itself do not change.

## [0.6.1] - 2026-10-30

Server only: the clients did not change (Python 0.5.0, JavaScript 0.4.0, Java 0.4.0 and Go stay as they are).

### Fixed

- **Windows, `http://localhost`**: with `TRACEREPORTS_BIND=127.0.0.1` or `localhost` (what `tracereports demo`
  sets) the server listened only on IPv4. Windows tries `::1` first, so every client connection waited ~2 s
  before falling back: the tests ran several times slower and the pytest plugin could lose evidence. Both
  values now also listen on `::1`, still only this machine (12 tests against the demo: 30.6 s → 0.23 s).
- The demo recordings no longer show the temporary path of the machine that recorded them in their stack
  traces.

### Security

- Each release also attaches its SLSA provenance as `tracereports_X.Y.Z.intoto.jsonl`, so it can be verified
  with `gh attestation verify --bundle` without GitHub's attestation API (see SECURITY.md).
- The CI and release workflows pin every action by commit, the Docker base images by digest and the Python
  build and test tools by hash; the Gradle wrappers are checked against Gradle's official checksums on every
  push.
- The project reached the OpenSSF Best Practices **passing** level; its badge is in the README.

## [0.6.0] - 2026-10-29

Server 0.6.0 with Python client 0.5.0, JavaScript client 0.4.0 and Java client 0.4.0 (they cover
sensitive data before screenshots and keep their recordings private).

### ⚠️ Breaking: the server no longer listens on the network without credentials

Before, a server with no `TRACEREPORTS_TOKEN`, `TRACEREPORTS_INGEST_TOKEN` nor UI login only logged a
warning, and anyone who reached the port could read and change the reports. Now (*fail-closed*):

- without credentials and without `TRACEREPORTS_BIND`, it listens **only on this machine** (`127.0.0.1` and
  `::1`): `go run ./cmd`, the binary on a laptop, `tracereports demo` and `selftest` work as before;
- without credentials and with `TRACEREPORTS_BIND` on the network (`0.0.0.0`, `::`, an IP), it **does not
  start** and says what to set;
- in **Docker** (official image), without credentials it **does not start**: inside the container it always
  listens on every interface, even when the port is published only on `127.0.0.1`.

**Migration:**

1. Generate a token: `openssl rand -hex 32` (or `python -c "import secrets; print(secrets.token_hex(32))"`).
2. Put it in the server's `.env` (`TRACEREPORTS_TOKEN=...`), or pass it with `docker run -e TRACEREPORTS_TOKEN=...`;
   optionally add `TRACEREPORTS_UI_USER` / `TRACEREPORTS_UI_PASSWORD` to protect the reports too.
3. Give the same token to your test clients and CI (`TRACEREPORTS_TOKEN`, or the restricted
   `TRACEREPORTS_INGEST_TOKEN`).
4. Only in a closed lab network, `TRACEREPORTS_INSECURE=1` restores the previous behavior (with a warning).
### Added

- **Chat with Trace Buddy**: with an LLM configured (Ollama, Gemini, Claude, OpenAI…), it takes free questions
  in a box at the bottom of its panel (Enter sends, Shift+Enter breaks the line) and answers with the evidence of
  the test or run being viewed and the last turns of the conversation, in the interface language, only about
  testing and TraceReports. Through the new `POST /api/v1/ui/ai/chat`: admin only, everything through the
  redaction (a password typed in the question does not reach the model), the provider allowlist, up to 60
  questions per hour per server, and every call in AI usage as "Questions to Trace Buddy". With the local rules
  engine there is no box: it cannot chat.
  Asking how to reproduce a call or for a mock brings real artifacts below the answer, built from the report and
  not by the model: the failed call as a cURL (secrets as `$VARIABLES`; Postman imports it) and a mock of the
  failed backend for WireMock, Playwright or Cypress, with Copy. The model gets the test's failed calls as cURL
  with the start of each response, knows TraceReports' tools (Copy as cURL, Generate mock, the Lab) and speaks
  in first person. The READMEs introduce Trace Buddy with an animated GIF where he says, with a gesture each, that
  there is no telemetry, that he is self-hosted and that your data is never shared; two of his thoughts in the app
  remind it too.
- **Why a test has no screenshots**: with `TRACEREPORTS_SCREENSHOTS=failures`, a test that did not fail and has
  no screenshots says that this server only keeps the failures' ones (with `off`, every test says so), instead
  of looking like the images were lost. `GET /api/v1/config` reports the policy, and Trace Buddy gets a "Why
  are there no screenshots?" question when the server drops any.
- **Language of the AI diagnoses** can be set in the `.env` with `TRACEREPORTS_AI_LANGUAGE` (`es`, `en`, `auto`),
  which is the only way when `TRACEREPORTS_SETTINGS_LOCKED=1`; what is chosen in Settings still takes
  precedence. `auto` now follows the team's default language and only falls back to each test's language
  when there is none, so a Spanish-speaking team gets Spanish diagnoses even with English test names.
  Changing the team language applies at once. Settings → Artificial intelligence says which language the
  diagnoses are written in and where it comes from, and the log says `diagnosis language lang=…` on start.
  A diagnosis in another language than the interface says so, in its card and in Trace Buddy: one click
  re-analyzes it, or (when Settings is editable) sets that language for every new diagnosis and re-analyzes
  it; with Settings locked it says which `.env` line to add.
- **Trace Buddy**, a QA assistant on a floating button (bottom right): a small QA engineer in a hood, in 3D
  (built in code with three.js, loaded only for whoever sees it and after the page is ready; without WebGL it
  shows its picture). Its face lives on the visor: it blinks, follows the pointer with eyes, head and body,
  talks when it answers, nods at a question and changes expression and gesture with what happens (scratches its
  head while the AI works, thumbs up with star eyes on success, hand to the visor on a problem). Left alone it
  fidgets, gets bored and falls asleep; hovered for a while it blushes. Now and then it says something in a
  thought bubble about what is being viewed (at most every ~90 s, only with the panel closed; the × silences it
  for the session). On a theme change it changes clothes step by step behind a pixelated censor: the old hood
  flies off, the new clothes come piece by piece and the new hood drops in. It only opens when you call it and
  can be hidden (Settings → Appearance brings it back). Its first message tells whether the configured AI
  answers (provider, model and how long it took, or why not in plain words), through the new
  `POST /api/v1/ui/ai/probe`. Without an LLM it answers from what TraceReports already knows: explains the
  failed test you are viewing (or opens the first one), what to check first in the run, how to connect your
  tests and whether the installation is secure. Admin only (the read-only login does not see it), hidden
  when AI is off; animations follow `prefers-reduced-motion`.
- **Change the `.env`** (`docs/en/docker.md`, `docs/es/docker.md`): how to edit it by hand or with a one-line
  command that replaces a variable without repeating it or adding a BOM, why `docker compose up -d` applies it
  and `restart` does not, the log line that confirms each change, that Settings takes precedence over the
  `.env`, **recipes to pick your flavor** (each a single command: diagnoses in your language, Ollama, Gemini
  with the key asked for instead of pasted, back to the local engine, maximum privacy, every screenshot, AI off;
  they combine) and how to back it up outside the project. The step-by-step guides set
  `TRACEREPORTS_AI_LANGUAGE` in the first `.env` and gained an optional Ollama step. The one-line `.env` commands in the Docker guides now work also when the file is
  left with a single line.
- **Install with Docker, step by step** (`docs/en/docker-step-by-step.md`, `docs/es/docker-paso-a-paso.md`): a
  guide for people new to Docker or the terminal, from installing Docker to the first report, with what to
  expect at every step, screenshots, real outputs and the fixes for common errors; linked from the READMEs
  and the Docker guide. A companion guide, **Try with Docker without touching your installation**
  (`docs/en/docker-isolated-trial.md`, `docs/es/docker-prueba-aislada.md`), runs another version next to an
  existing one (own project, image and volume), checks everything and puts the existing one back at the end.
  The Docker guide's "First start" now creates the `.env` with the token and login
  before `docker compose up -d` (before, the container refused to start on the first try).

- **Similar incidents**: each classified failure is indexed by its signature (TF-IDF in pure Go, no AI), and the
  diagnosis of a new failure shows up to three already classified failures of the same project that look at
  least 60 % like it, with their verdict and comment (`GET /api/v1/tests/{id}/similar`).
- **Local LLM without Ollama**: optional `docker compose --profile llm` with llama.cpp server (no published
  port, model mounted read-only, nothing downloaded at runtime) and a guide for llama.cpp, llamafile, LM Studio,
  vLLM and LocalAI (`docs/en/local-ai.md`).
- **Local diagnosis engine, no LLM** (`AI_PROVIDER=builtin`, the new default): a versioned catalog of
  deterministic rules inside the binary explains why each test failed (backend 5xx, 429 or no answer,
  DNS/refused/TLS, 401/403, changed selector, assertion with expected and actual values, uncaught JavaScript
  error, flaky test, environment down) and writes the run summary, in Spanish or English. It uses no network,
  no model and no API key, so the diagnosis works from the first `docker run` and test data never leaves the
  server. The UI labels it **Local engine** with the rule that concluded it. With an LLM configured, the engine
  runs first and the model gets its (redacted) conclusion to confirm or correct it
  (`TRACEREPORTS_AI_BUILTIN_HINT=0` turns that off). `tracereports report` uses it without `--ai`, and
  `tracereports selftest` reports its hit rate over the recorded scenarios (minimum 90 %).

### Changed

- **Security check → Network exposure** explains the Docker case: inside the official image the server always
  listens on the container network, so the warning now says that a port published only on `127.0.0.1` (the
  official `docker-compose.yml`) is already reachable from that machine only, and how to filter inside Docker
  too with `TRACEREPORTS_ALLOWED_CIDRS` and the container network. It stays a warning: from inside the
  container the published port cannot be seen. The Docker guides gained an optional step to turn it green
  (look up the network, save it in the `.env`, check for `401` vs `403`, undo).
- **Without an AI key, failures are now diagnosed by the local engine** instead of not at all. To keep the
  previous behavior (no diagnosis), set `AI_PROVIDER=off`. Existing LLM setups do not change.
- CI runs the Go tests with the race detector and checks that the server still builds without CGO.
- The CI, the release binaries and CodeQL build with the latest patch release of Go 1.27 (like the Docker
  image) instead of the minimum in `go.mod`: the binaries carry the standard library's security fixes.
- A pre-release tag (`v0.6.0-rc1`) is published as a pre-release, takes its notes from `[Unreleased]` and
  no longer moves the image's `latest` tag.
- **Settings → Security check** has a new layout: a summary ring with the verdict, what needs fixing first
  (critical before warnings, with the fix in view), and the checks in order grouped by topic in a compact grid
  with an icon each; click one to see its detail. Titles and details follow the interface language (the API
  and `doctor --security` keep their English output), and **Check again** re-runs the checks.

### Fixed

- With no runs yet, going from Settings, Metrics or Search to Tests, Categories or any other run view
  left the previous screen showing: the "No runs yet" message is back.
- Settings → AI usage said "Ollama answers" for the local rules engine, which does not use Ollama: it now
  says that it runs inside the server.
- **Open in Lab** stayed on "Preparing the Lab…" for a call with nothing masked (no password, token nor
  cookie): the plan sent its values as `null`. It now opens and says the call is resent as it is; any other
  unexpected plan shows an error instead of hanging.
- A side panel (Lab, mock, compare) closed right as it opened (Escape or a very fast double click) came back
  open.
- On a phone, long test names and errors with no spaces (`test_…[chromium]`,
  `playwright._impl._errors.TimeoutError`) pushed Categories, Errors and Analysis sideways: they now wrap.

### Security

- **Docs fixes**: `TRACEREPORTS_SCREENSHOT_MASK` is read by the test clients, not the server (removed from
  the server profile of the hardening guide); `doctor --security` now names the full
  `TRACEREPORTS_AI_ALLOWED_PROVIDERS` variable; the Docker guide explains `TRACEREPORTS_TRUSTED_PROXIES`
  when the proxy runs on the same server as the container and `TRACEREPORTS_ALLOWED_CIDRS` is set; duplicated
  rows of the threat model removed.

- **Browser protections**: every response carries a Content-Security-Policy (only this server's scripts,
  the inline ones of `index.html` by hash), `X-Frame-Options: DENY`, `nosniff` and
  `Referrer-Policy: same-origin`. **Cross-site writes are rejected** (403 when the browser says the request
  comes from another site), so a web page can no longer post fake runs to a local server; test clients
  and CI are not affected. **Failed logins are throttled**: 10 wrong passwords or tokens from an address in
  15 minutes give 429 for the rest of the window. The live stream accepts at most 500 connections, and the
  server now has read (15 min) and idle (2 min) timeouts against slow clients.
- **Read-only UI login** (`TRACEREPORTS_UI_VIEWER_USER` / `TRACEREPORTS_UI_VIEWER_PASSWORD`): the rest of
  the team sees every report but never sends results, changes Settings nor uses the UI actions, not even
  from the same machine. The production profile of the hardening guide includes it.
- **The AI key can no longer be redirected**: a key saved from Settings or given by the environment is
  only sent to the API URL it was configured for. Changing the URL (when saving or in "Test connection")
  requires typing the key again, otherwise 409. At startup, a saved URL that differs from the
  environment's no longer takes the environment key.
- **Private files outside the server**: `tracereports report` and the offline recordings and mirror of
  the four clients (Python, JavaScript, Java, Go) now create their folders with `0700` and files with
  `0600` on Linux and macOS (before: readable by every user of the machine). A recording holds the
  evidence before server-side redaction. To publish a static report with a web server, grant read access
  on purpose (`chmod -R a+rX`).
- **`repro`, `verify` and the MCP stay on the test's hosts**: a resend from your machine (with the secrets
  of `TRACEREPORTS_LAB_<NAME>`) only goes to the hosts the test called, those of `TRACEREPORTS_LAB_HOSTS`
  and, in the CLI, the URL passed with `-base`. The base URL an agent proposes over MCP never opens a new
  host. They also read from the server with the smallest credential available (ingest token, read-only
  login) instead of the admin ones, and the token no longer gets overwritten when a UI login is also set.

- **`tracereports doctor --security`**: the hardening checklist as checks (tokens, UI login, network
  exposure and CIDR allowlist, AI provider and allowlist, Settings lock, retention, encryption at rest,
  process user, `DATA_DIR` permissions, outbound destinations, redaction, screenshot policy) with OK / WARN /
  FAIL and what to do; exit code 1 when a critical check fails, `-json` with a stable contract, `-url` to
  ask a running server. Also at `GET /api/v1/security/posture` (admin only) and in **Settings → Security
  check**. The production profile of the hardening guide now includes `TRACEREPORTS_ALLOWED_CIDRS`,
  `NOTIFY_DETAIL=minimal` and encryption at rest, and passes every check.
- **Threat model** (`docs/*/threat-model.md`): assets, actors, surfaces, mitigations and residual risks,
  including the caveats of encrypted SQLite VFS (#41). **API stability policy** (`docs/*/api-stability.md`):
  stable, experimental and internal levels, deprecation with `Deprecation`/`Sunset` headers from 1.0.
  **Criteria for 1.0** (`docs/*/release-1.0.md`) measured by `scripts/release_criteria.py` and a weekly
  workflow. `SECURITY.md` now has patch times by severity and a coordinated disclosure window;
  `GOVERNANCE.md` and `MAINTAINERS.md` describe decisions and how to become a maintainer.

- `tracereports selftest` checks that the screenshots recorded with the Python (Playwright) and Java (Selenium)
  clients show the demo shop's member card (`data-tracereports-mask`) covered.
- **Evidence encrypted at rest, opt-in** (`TRACEREPORTS_ENCRYPT_AT_REST=1` with `TRACEREPORTS_DB_KEY` or
  `TRACEREPORTS_DB_KEY_FILE`): new screenshots, Playwright traces, videos and DOM snapshots are stored with
  authenticated AES-256-GCM (64 KiB chunks, HKDF subkeys per use). The UI, ZIP export and tickets read them
  as before (videos keep Range requests). Fail-closed: turned on without a valid key, or with the key file
  inside `DATA_DIR`, the server does not start. `tracereports evidence status|encrypt|rekey` encrypts the
  evidence kept before and rotates the key (`TRACEREPORTS_DB_KEY_PREVIOUS`). Off by default: nothing changes.
  The SQLite database itself is not encrypted (a pure-Go encrypted SQLite cost too much in the spike):
  keep `DATA_DIR` on an encrypted volume.

- **Go client**: `MaskSelector()`, `MaskColor` and the `CoverScript`/`UncoverScript` scripts to take screenshots
  with sensitive data covered (the playwright-go example uses them), and `Test.Screenshot` follows
  `TRACEREPORTS_SCREENSHOTS=all|failures|off`.
- **Hardening**: SQLite `secure_delete` plus `VACUUM` and a WAL checkpoint after each retention purge, so
  purged evidence is not left in the database file; `DATA_DIR` is `0700` (an older, wider folder is
  tightened) and screenshots and artifacts are written `0600`; the server warns at startup when
  `TRACEREPORTS_RETENTION_DAYS` is not set. `docker-compose.yml` runs read-only with a `tmpfs` `/tmp`,
  `cap_drop: [ALL]`, `no-new-privileges` and a healthcheck (`tracereports health`, new, asks the new
  unauthenticated `GET /healthz`); the image has the same `HEALTHCHECK`. New
  [security hardening guide](docs/en/security-hardening.md).
- **AI provider allowlist** (`TRACEREPORTS_AI_ALLOWED_PROVIDERS=builtin,ollama`): a key of a provider outside
  the list is ignored (with a warning), Settings only offers the allowed ones and rejects the rest, "Test
  connection" never calls them and an unknown name stops the server. At startup the server logs every
  external destination its configuration sends data to (AI and its status page, Teams, Slack, trackers, Lab
  hosts), by host and scope.
- **Minimal notifications** (`NOTIFY_DETAIL=minimal`): Teams and Slack get only the run name, counts, status
  and the report link, without test names, errors, backend URLs or AI text. It also applies to the weekly
  summary (numbers only) and escalations (severity and link).
- **Source IP allowlist** (`TRACEREPORTS_ALLOWED_CIDRS`, IPv4 and IPv6): requests from other networks get `403`
  on the UI, the API and the screenshots, even with a valid token; loopback is always allowed.
  `X-Forwarded-For` / `X-Real-IP` are only trusted from `TRACEREPORTS_TRUSTED_PROXIES`, so a spoofed header does
  not get around it. An invalid value stops the server.
- **Screenshot policy on the server** (`TRACEREPORTS_SCREENSHOTS=all|failures|off`): with `failures` only the
  screenshots of a failed step or test are kept (traces and videos only of failed tests); with `off` no image,
  trace or video is kept. A refused upload answers `403` before anything is written, so no file is left on disk.
  It also applies to Allure imports and `tracereports report`.
- **Python client: screenshots with sensitive data covered.** Before each screenshot it covers
  `input[type=password]`, every element with `data-tracereports-mask` and the CSS selectors of
  `TRACEREPORTS_SCREENSHOT_MASK` (Playwright: native `mask`; Selenium: temporary boxes removed afterwards, even
  when the screenshot fails; no screenshot if it cannot cover). `TRACEREPORTS_SCREENSHOTS=all|failures|off`
  decides which screenshots are sent. New helpers `playwright_screenshot(page)` and `selenium_screenshot(driver)`.
- **Java client: screenshots with sensitive data covered.** `SeleniumEvidence.screenshot` and
  `PlaywrightEvidence.screenshot` cover `input[type=password]`, `data-tracereports-mask` and
  `TRACEREPORTS_SCREENSHOT_MASK` before capturing (Playwright `mask`; Selenium temporary boxes, always removed),
  and `TraceTest.screenshot` follows `TRACEREPORTS_SCREENSHOTS=all|failures|off`.
- **JavaScript client: screenshots with sensitive data covered.** The Playwright fixtures take the failure
  screenshot with `mask` (`input[type=password]`, `data-tracereports-mask`, `TRACEREPORTS_SCREENSHOT_MASK`) and
  the reporter uploads it instead of Playwright's unmasked automatic one; Selenium's `screenshot(driver)`
  covers the same elements with temporary boxes. `TRACEREPORTS_SCREENSHOTS=all|failures|off` also governs the
  traces and videos the reporter uploads. New helper `playwrightScreenshot(page)`.
- **Signed releases**: binaries (through `checksums.txt`) and the Docker image are signed keyless with
  cosign, carry an SPDX SBOM and a SLSA provenance attestation. How to verify them: `SECURITY.md`.
- **govulncheck** fails CI when reachable code has a known vulnerability; **CodeQL** analyzes Go,
  JavaScript/TypeScript, Python, Java and the workflows; **OpenSSF Scorecard** runs on `main` (badge in the
  README); a release rehearsal builds, signs and verifies everything on the pull requests that change it.

## [0.5.0] - 2026-10-16

Server only: the clients did not change (Python 0.4.0, JavaScript 0.3.0, Java 0.3.0 and Go stay as they are).

### Added

- **Search inside errors and endpoints**: the run search (and Ctrl+K) also finds a run by the error message of
  any of its tests (`pool exhausted`) and by the method and URL of a failed network call (`POST /api/orders`).
  Only failed calls are indexed; an existing database is reindexed once on the first start.

- **Suggested selectors in Selenium**: every suggestion also comes as Selenium code for Python, Java and JS, with
  the most natural `By` (CSS, `ID`, `NAME`, or XPath for a button, link or field by its text or label; XPath
  literals with both kinds of quotes use `concat()`). The locator card switches between Playwright and Selenium
  and between Python, JS / TS and Java; by default it follows the test (Selenium when the error is Selenium's)
  and the language of the run.

### Fixed

- Locator suggestions: a broken selector that names a button (`#add-to-cart-btn`, `.submit-button`) prefers
  buttons, landmarks and containers (`nav`, lists, regions) are not proposed for their children's text, and
  without a tag hint interactive elements go first. On OrangeHRM the PIM link now comes first for the broken
  `#menu_pim_viewPimModule`, and in the demo the Add to cart button before the "Added to your cart" message.
- A panel opened right after closing another one no longer stays open but invisible.
- English UI: the labels of a call's General block (Method, Status, Start…), an AI provider error with its
  hint (a quota, no credits), the time before the failure in an incident's evidence and the clients'
  "Screenshot at failure" step were still in Spanish.
- `tracereports push` and `report` read a recording in streaming instead of loading it whole: pushing a 300 MB
  recording went from a 386 MB memory peak to 40 MB (`report` from 693 MB to 446 MB; the rest is the report
  itself, which carries the evidence).

## [0.4.0] - 2026-10-15

Client versions released with it: **Python 0.4.0** (PyPI), **JavaScript 0.3.0** (npm) and **Java 0.3.0**. The Go
client did not change.

### Added

**Selenium: network, console and suggested selectors**
- The clients read Chrome's and Edge's performance and browser logs, so Selenium tests get the **Network** tab
  (with the bodies of the APIs and errors, through CDP), backend incidents, the **Lab** and the **Console** tab,
  like Playwright. One line where the driver is created:
  - Python: `tracereports.selenium.selenium_options(options)`; with `pytest --tracereports` the plugin finds the
    driver in the test's fixtures and sends each test's network and console, and the screenshot and DOM when it
    fails. Without pytest, `report_selenium(driver, cr)`.
  - JavaScript: `seleniumOptions(options)`; `withDriver` now also sends the network and console. `cr.test` gets
    `onStart` and `onEnd` hooks.
  - Java: `SeleniumEvidence.options(options)`; `SeleniumEvidence.attach` now also sends the network and console
    (`CdpNetwork` turns the log into connections).
- Suggested selector for Selenium with pytest: the locator is read from the failing line of the traceback
  (`By.CSS_SELECTOR, "#menu"`), since a `TimeoutException` does not name it.
- The demo's Selenium scenarios now carry the browser's network.
- [Selenium guide](docs/en/selenium.md) (English and Spanish), linked from the client guides, their READMEs and
  the examples, which now create the driver with the helper.

## [0.3.0] - 2026-10-15

The clients did not change: Python 0.3.0, JavaScript 0.2.0, Java 0.2.0 and the Go client keep working with it.

### Added

**Try it in a minute**
- `tracereports demo`: the server with runs recorded with the real clients (pytest + Playwright, Playwright Test,
  Selenium + JUnit 5, `go test`, REST and a JUnit XML import; 12 nights of history plus tonight's suites) and a demo
  shop whose page is a **playground**: break or fix its backend to try the Lab, and send any scenario or all of them,
  with the requests sent, the checks and a link to the run. Temporary, only on this machine and isolated from your
  configuration. The playground is animated with [Motion](https://motion.dev) (MIT, bundled: works offline).
  It diagnoses with AI when your environment or `.env` has a provider key (only the AI variables are read from
  the `.env`; `-ai=false` turns it off) and says so in the guide it prints.
- `TRACEREPORTS_BIND` chooses the address to listen on (`127.0.0.1` = only this machine).
- `tracereports selftest`: plays the scenarios recorded with the real clients (pytest + Playwright, Playwright Test,
  Selenium + JUnit 5, `go test`, REST, JUnit XML) against a temporary server or `-url`, and checks what TraceReports
  shows (statuses, incidents, masking, screenshots, suggested selectors, flaky tests, local commands, the Lab).
  Exit code 1 on a failed expectation. The same check runs in CI (`go test ./internal/demo`). With `-url` it sends
  `TRACEREPORTS_TOKEN` to a protected server.
- The Lab finds the session token in a fresh login answer when the recorded one has no body (a page that
  navigates right after signing in).

**Command line**
- `tracereports version` (also `-version`, `--version`); release builds report their version (also to MCP
  clients, which got `dev` before).
- `tracereports report -ai` takes the AI provider from the `.env` too, like the server and the demo.

**Allure**
- `tracereports push` also takes an `allure-results` folder (or its ZIP) and JUnit XML: it imports them into
  the server, where every failure gets the AI diagnosis (`-name`, `-project`, `-branch`, `-environment`, and the
  token of `-token` / `TRACEREPORTS_TOKEN`). Before, only `curl` with a ZIP.
- The demo, the playground and `selftest` include an Allure scenario: the allure-results of a pytest +
  Playwright suite that only uses Allure, recorded against the demo shop.
- [`examples/python-selenium-allure`](examples/python-selenium-allure): pytest + Selenium + Allure, sending the
  allure-results or adding the plugin to the same run (screenshots and steps to both).
- [Allure guide](docs/en/allure.md), tested with the results of allure-pytest, allure-playwright and
  allure-junit5 from [allure-examples](https://github.com/allure-examples).

**FixLab: Request Lab**
- **Open in Lab** on a failed call (Network tab) or a backend incident resends the call to see
  whether the failure still happens. The Lab rebuilds a fresh session from the recording (repeats
  the recorded login: cookies, or its JSON token as `Bearer`) and reads anti-CSRF tokens live from
  the page; you only type the secrets nobody can guess. Edit the request like in Postman, point it
  to another environment (`http://localhost:3000`) and get a verdict against the run: reproduced,
  no longer fails, session missing, different error or no connection. It warns when the original
  response looks made up by the test (no `date` nor `server` headers).
- **Save as a check** (the call plus the expected status and texts) and **Verify** on the incident:
  runs its checks and records **Verified: fixed** or **still failing**, with the before and after.
- **Capsule** (JSON with the context, the test command and each call with its checks) and two new
  commands: `tracereports repro <capsule|URL>` resends the calls from your machine and
  `tracereports verify <capsule|URL>` runs the checks, exit code 1 if one fails (CI). The report
  copies both commands. Values with `-var`, `TRACEREPORTS_LAB_<NAME>` or a hidden prompt.
- `tracereports mcp`: MCP server for Claude Code and other agents (`list_runs`, `get_incidents`,
  `get_test`, `reproduce`, `verify_fix`). Read-only on TraceReports; secrets stay in the MCP
  server's environment and never reach the agent.
- **Export** a Lab request to Postman (collection with the login script), Bruno (`.bru`) or `.http`
  (VS Code REST Client), keeping the `{{variables}}` and no values.
- API: `GET /api/v1/network/{id}/lab`, `GET /api/v1/network/{id}/capsule`,
  `GET /api/v1/runs/{id}/lab/capsule`, `POST /api/v1/ui/lab/send`, `POST|DELETE /api/v1/ui/lab/checks`,
  `POST /api/v1/ui/lab/verify` and `GET /api/v1/runs/{id}/lab`. Same access rule as the UI
  actions; on a shared server only the hosts the test called and `TRACEREPORTS_LAB_HOSTS`;
  link-local addresses always blocked; `TRACEREPORTS_LAB=off` disables it. Typed values are never
  stored and the answer is masked before it is shown. Design: [docs/rfc/fixlab.md](docs/rfc/fixlab.md).

**Docs**
- A [command line reference](docs/en/cli.md): every `tracereports` command, its flags, exit codes and which
  configuration (environment or `.env`) it reads.
- A [quickstart](docs/en/quickstart.md) to start the demo's services step by step, and guides for
  [FixLab](docs/en/fixlab.md) and for the [demo, playground and selftest](docs/en/demo.md), in English
  and Spanish, linked from the READMEs, installation, configuration, API and CI.

### Fixed

- A failure whose element never showed up because the backend failed right before (no answer, 5xx, 408, 429) is no
  longer diagnosed as a broken selector: no misleading suggestions, neither on the screen nor in the AI prompt.
- The broken selector is the one Playwright was waiting for, not an earlier getter the traceback quotes.
- Without network evidence (Allure or JUnit imports), a failure the AI attributes to the backend or the
  infrastructure no longer shows a "broken selector" card with nothing to suggest.
- Allure import: a label or parameter with a number, boolean or null value (`allure.id(123)`, parametrized
  tests) no longer rejects the whole import.
- The command to run a test locally for Allure imports: pytest gets a node id (`pkg/module.py::test`, not the
  Allure `pkg.module#test`), and allure-playwright (`file:line`) and allure-junit5 (`package.Class.method`) get
  one too.
- `tracereports secrets -h` prints its usage instead of an error; `tracereports help` lists `version` and
  describes `push` and `report` with their Allure and JUnit inputs.
- `tracereports demo -keep`: the kept data is no longer deleted by the cleanup of old demos 12 hours later.
- Without AI, a failed test no longer asks only for `GEMINI_API_KEY`: it points to Settings → AI or `AI_PROVIDER`
  (in the demo, to the `.env`), and the message is translated in English.

## [0.2.0] - 2026-10-09

Client versions released with it: **Python 0.3.0** (PyPI), **JavaScript 0.2.0** (npm),
**Java 0.2.0** and **Go `client/go/v0.2.0`**.

### Added

**Find and share**
- **Search** view (`GET /api/v1/runs/search`, `/runs/facets`): search runs by name, project, branch,
  commit or test name; filters as removable chips, saved searches, shareable URLs, a preview panel
  (incidents, failed tests, release decision, changes) and compare from the list.
- **Ctrl/Cmd+K** command palette: runs, tests of the open run and views. **Recents** menu in the header.

**Without a server, and a report on your PC**
- `tracereports report` builds the static HTML report from a recording, a JUnit XML file or Allure
  results; `tracereports push` uploads a recording later.
- Clients record the evidence locally when the run cannot be created (server down or wrong token).
- `TRACEREPORTS_OFFLINE=both`: send to the server **and** keep a local copy of every run, one folder
  per run (`TRACEREPORTS_OFFLINE_BASE`, named after the run), with its HTML report built at the end.
- The clients download the report binary from GitHub Releases when it is missing (SHA-256 verified,
  per-user cache; `TRACEREPORTS_BIN_DOWNLOAD=0` turns it off).
- Exported reports include **Escalate** (template summaries) and the **Release** decision.
- Guide: [Local report on your PC](docs/en/local-report.md).

**Imports and CI**
- `POST /api/v1/import/junit` and `/import/allure`.
- `tracereports pr-comment`: the run summary on GitHub pull requests and GitLab merge requests.
- `TRACEREPORTS_INGEST_TOKEN`: a restricted token for CI that cannot change Settings.

**For QA and developers**
- Quarantine of known flaky tests (owner, reason, expiry), test owners from CODEOWNERS-like rules
  and failure verdicts remembered across runs.
- Playwright trace and video per test (Trace Viewer link), browser console capture in every client.
- Command to rerun a failed test locally at its commit; compare a failed call with the last time the
  test passed; links from failed calls to backend logs and traces.
- Technical detail for developers in escalations and tickets.

**For the team**
- **Release** view ("can we ship?") with a configurable gate (`TRACEREPORTS_RELEASE_GATE`).
- Tickets in GitHub, Jira and Azure DevOps from Escalate, without duplicates.
- Weekly quality summary to Teams/Slack (`TRACEREPORTS_WEEKLY_SUMMARY`).
- Settings → **AI usage**: calls, tokens, response times, errors by reason and the provider's status;
  Escalate shows how long the AI answer took and why it retried.
- Webhook deliveries are stored and retried; abandoned runs are closed as incomplete
  (`TRACEREPORTS_STALE_RUN_HOURS`).

### Changed

- Run, notification, PR comment and escalation texts describe the real state of a run: in progress,
  incomplete, skipped or with warnings are no longer presented as successful.
- The pass rate is `PASS` over the tests that ran, the same everywhere (a `WARNING` is not a pass).
- The automatic AI budget (`TRACEREPORTS_AI_MAX_PER_RUN`) counts one diagnosis per test result.
- Network evidence is sent in batches by size (8 MiB / 200 calls); `body_size` is in UTF-8 bytes and
  `body_truncated` marks a body that arrived already cut (also in the TypeScript types).
- The header run selector became **Recents** plus **Search all**.

### Fixed

- Security and robustness fixes from several external audits, among them: recordings can only
  replay evidence calls; secrets are masked in exported JSON, long or escaped keys and values cut
  at the end; credentials are scrubbed from AI and tracker errors; ticket creation never duplicates;
  late or stale web responses no longer overwrite the current selection; cached escalations follow
  the evidence; endpoint drift keeps hosts apart; generated mocks are valid code.
- AI usage counts every provider attempt (schema fallback, SDK retries, connection test).

### Upgrade notes

- **Back up `DATA_DIR` first**: the first start adds tables and columns, and builds the search index
  (it can take a few seconds on a large history; later starts are immediate).
- An environment variable defined **empty** now wins over the project's `.env` in the clients, as on
  the server.
- Do not set `TRACEREPORTS_OFFLINE_DIR` in an everyday `.env`: it is the folder shared by the
  processes of one run. Use `TRACEREPORTS_OFFLINE_BASE` for one folder per run.
- Escalation summaries cached by 0.1.0 are written again once (the cache now follows the evidence).
- If the server asks for it on start: `TRACEREPORTS_AI_BUDGET_RECOVERY` or `TRACEREPORTS_LEGACY_DB`
  (see [Configuration](docs/en/configuration.md)).

## [0.1.0] - 2026-10-04

First public release: self-hosted report server (single Go binary with SQLite), web UI with six
themes, AI diagnosis (Gemini, Claude, OpenAI, compatible APIs, Ollama), escalation summaries,
Teams/Slack notifications and clients for Python, JavaScript/TypeScript, Java and Go.

[Unreleased]: https://github.com/josemiguellopez/tracereports/compare/v0.8.0...HEAD
[0.8.0]: https://github.com/josemiguellopez/tracereports/compare/v0.7.0...v0.8.0
[0.7.0]: https://github.com/josemiguellopez/tracereports/compare/v0.6.2...v0.7.0
[0.6.2]: https://github.com/josemiguellopez/tracereports/compare/v0.6.1...v0.6.2
[0.6.1]: https://github.com/josemiguellopez/tracereports/compare/v0.6.0...v0.6.1
[0.6.0]: https://github.com/josemiguellopez/tracereports/compare/v0.5.0...v0.6.0
[0.5.0]: https://github.com/josemiguellopez/tracereports/compare/v0.4.0...v0.5.0
[0.4.0]: https://github.com/josemiguellopez/tracereports/compare/v0.3.0...v0.4.0
[0.3.0]: https://github.com/josemiguellopez/tracereports/compare/v0.2.0...v0.3.0
[0.2.0]: https://github.com/josemiguellopez/tracereports/compare/v0.1.0...v0.2.0
[0.1.0]: https://github.com/josemiguellopez/tracereports/releases/tag/v0.1.0
