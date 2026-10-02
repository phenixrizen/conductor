# Round 4: wall and crew run terminals do not fill their tiles

Diagnosis only. No tracked file was edited. Measured on HEAD 2c9c220 (bin/conductor, embedded dist), headless Chromium 125
(playwright-core), test server on 127.0.0.1:8088 (fresh HOME and CONDUCTOR_DATA_DIR, stopped by pid, data dir removed).

## Cause

The wall tile (`SessionTile.vue:50`) and the join page tile (`JoinCrewGrid.vue:56`) mount
`<TerminalView read-only fit="scale" compact>`. That mode never fits the terminal to the tile. It draws the PTY's own grid
(80x24) and shrinks it uniformly into the pane. It is a "contain" scale, so whatever the aspect mismatch leaves is blank, split
left and right.

* `TerminalView.vue:106-109` `measure()`: `if (!props.readOnly) fit?.fit()`. A `read-only` tile never calls `fit()`.
  `:315` (initial fit) and `:111-113` (`scheduleResize` returns early when readOnly) say the same. A tile never refits or
  resizes the session.
* `TerminalView.vue:235` `t.connect(measure())`: the hello carries the unfitted xterm default, 80x24.
  `internal/session/local.go:545-551` (`AttachWith`) applies a control-role hello size to the PTY and broadcasts `resize`.
  The wall and run pages attach with the admin token (control), so every tile attach forces its session to 80x24. Measured:
  three sessions at 148x57 became 80x24 the moment the wall opened. (Guests on a view link are role view; their hello is
  ignored.) Nothing else gives a tile a different size: `internal/api/sessions.go:97-103` defaults 80x24, and neither the launch
  modal nor the crew launch passes cols/rows.
* `TerminalView.vue:127-148` `applyScale()`: `k = Math.min(hw / natW, hh / natH)` (:136), then centred with
  `translate(tx, ty) scale(s)` (:145-147). The font is only stepped to a whole pixel size (:138-143), and xterm rounds the cell
  to whole pixels (cell 5x12 at font 8-9, 7x? at 11-12, 8x18 at 14), so the grid's aspect lands between 1.38 and 1.65.
  Even with ideal glyph proportions (0.6em x 1.32em) an 80x24 grid is 1.515:1.
* The pane is wider than that. The tile is a `minmax(0, 1fr)` grid cell (`wall.vue:113-117`, `runs/[run].vue:169-174`) minus a
  31 px header and a 25 px footer, so the pane is 1.76-1.88:1 at 1920x1080 and 1.88:1 on the run page at 1440x900.
  `bestGrid`'s aspect argument (1.6 / 1.4) is only the choice of rows and columns; the cell still stretches.
* It is invisible as a box because `.terminal-host` is painted with the page background (`main.css:102-108`), so the user sees
  text with wide empty margins.

Ruled out (each measured or read):
* No stale fit: resizing 1440 -> 1920 without reload, a fresh load and adding a fourth tile give identical numbers
  (`ResizeObserver` on the host works: `TerminalView.vue:316-325`, wall `watch(grid)` ResizeObserver).
* No `max-width`, fixed width or tile padding (`.terminal-compact .xterm` is 0.25rem = 4 px a side).
* WebGL canvas: not testable here (this Chromium has no WebGL2 even with swiftshader/angle flags, so the addon's `catch`
  falls back to the DOM renderer and no `<canvas>` exists). By source, `addon-webgl` sets the canvas CSS size and
  `.xterm-screen` to the same `css.canvas` width (cols x cell), so the canvas cannot be a separate bound.
* Resize policy: wall tiles are one viewer per session, so no other viewer sets the size. The size is 80x24 because the tile's
  own hello puts it there (see above).

The wall at 1440x900 with 3 or 4 tiles looks fine (0.3 cell unused) only by coincidence: its pane is 570x346 = 1.65:1 and the
font that fits height happens to make a 7x14 cell grid of 1.65:1. Any other viewport or tile count shows the gap.

## Measurements (all tiles in a row identical; px)

Gap = pane width minus drawn `.xterm` width; "cells" = gap / (cell width x scale). Canvas: none (DOM renderer, see above).
`.xterm-screen` = cols x cell width.

| scenario | viewport | tile | pane | PTY | cell | `.xterm` natural | `.xterm` drawn (scale) | `.xterm-screen` w | unused W | unused H |
|---|---|---|---|---|---|---|---|---|---|---|
| wall, 3 tiles | 1440x900 | 572x404 | 570x346 | 80x24 | 7 | 568x344 | 568x344 (1) | 560 | 2 (0.3 cell, 0%) | 2 |
| wall, 4 tiles (one added live) | 1440x900 | 572x404 | 570x346 | 80x24 | 7 | 568x344 | 568x344 (1) | 560 | 2 (0.3 cell, 0%) | 2 |
| wall, 3 tiles, resized 1440 -> 1920 without reload | 1920x1080 | 769x494 | 767x436 | 80x24 | 8 | 648x440 | 642x436 (0.991) | 640 | 125 (15.8 cells, 16%) | 0 |
| wall, 3 tiles, fresh load | 1920x1080 | 769x494 | 767x436 | 80x24 | 8 | 648x440 | 642x436 (0.991) | 640 | 125 (15.8 cells, 16%) | 0 |
| wall, 4 tiles (one added live) | 1920x1080 | 769x494 | 767x436 | 80x24 | 8 | 648x440 | 642x436 (0.991) | 640 | 125 (15.8 cells, 16%) | 0 |
| wall, a session already at 120x30 | 1440x900 | 572x404 | 570x346 | 120x30 | 5 | 608x308 | 570x289 (0.938) | 600 | 0, but 57 high (17%) | 57 |
| wall, a session already at 160x45 | 1440x900 | 572x404 | 570x346 | 160x45 | 3 | 488x323 | 488x323 (1, font floor 5) | 480 | 82 (27 cells, 14%) | 23 |
| run, 3 members + feed | 1440x900 | 572x361 | 570x303 | 80x24 | 5 | 408x296 | 408x296 (1) | 400 | 162 (32.4 cells, 28%) | 7 |
| run, 3 members, resized to 1920 without reload | 1920x1080 | 769x451 | 767x393 | 80x24 | 7 | 568x392 | 568x392 (1) | 560 | 199 (28.4 cells, 26%) | 1 |
| run, 3 members, fresh load | 1920x1080 | 769x451 | 767x393 | 80x24 | 7 | 568x392 | 568x392 (1) | 560 | 199 (28.4 cells, 26%) | 1 |
| join page (guest, view role), 3 members | 1440x900 | 464x288 | 462x230 | 80x24 | 4.81 | 393x224 | 393x224 (1) | 385 | 69 (14.3 cells, 15%) | 6 |
| join page, resized to 1920 | 1920x1080 | 624x288 | 622x230 | 80x24 | 4.81 | 393x224 | 393x224 (1) | 385 | 229 (47.6 cells, 37%) | 6 |
| control: `/sessions/<id>` (fill mode) | 1440x900 | n/a | 823x818 host | 99x47 | 8 | 823x818 | n/a | 792 | 15 (1.9 cells) after 16 px padding | 0 |
| control: `/sessions/<id>` (fill mode) | 1920x1080 | n/a | 1216x998 host | 148x57 | 8 | n/a | n/a | 1184 | 16 (2.0 cells) after 16 px padding | 0 |

Expected rounding is under 1 cell. Wall at 1920x1080: 16 cells. Run page: 28-32 cells (26-28% of the pane). Join page: 14-48 cells.

Experiment (no code change; PTYs widened over a WebSocket controller to what a tile-driven fit would ask for). The gap
closes as the column count approaches the pane's:

| run page | PTY | unused W |
|---|---|---|
| 1440x900 | 80x24 | 162 px (32.4 cells) |
| 1440x900 | 100x24 | 62 px (12.4) |
| 1440x900 | 104x24 | 42 px (8.4) |
| 1440x900 | 110x24 | 12 px (2.4; font 8-9, cell 5 px) |
| 1920x1080 | 80x24 | 199 px (28.4) |
| 1920x1080 | 96x24 | 87 px (12.4) |
| 1920x1080 | 104x24 | 31 px (4.4); about 111 columns would be flush |

Secondary finding in the full view (fill mode): `FitAddon.proposeDimensions` reserves 14 px for a scrollbar whenever
`scrollback != 0` (`options.overviewRuler?.width || 14`). `TerminalView.vue:272` passes scrollback 5000, but the scrollbar is
hidden (`main.css:135-148`), so the full page loses about 14 px (1.75 cells) at the right edge: 15-16 px unused measured.

## Screenshots

The terminal and the page share a background, so the annotated shots add a dashed red outline and pink tint to the pane
(`.terminal-host`) and a blue outline to `.xterm`. The pink area is the gap.

Directory: `r4fill/shots/` (evidence captured during the investigation, not kept; the names below say what each showed).

* annot-wall-1920x1080-3.png: wall, 62 px of pink on each side of every tile (16%).
* annot-run-1440x900-3.png: run page, 81 px each side (28%).
* annot-run-1920x1080-3.png: run page, 26%.
* annot-join-1920x1080-3.png: guest join page, 37%.
* annot-run-1920x1080-3-ptys-108x24.png: the same run page with the PTYs widened to 108 columns: the pink is gone.
* Plain (no annotation): wall-1440x900-3.png, wall-1920x1080-3-fresh.png, wall-1920x1080-3-resized.png, wall-1920x1080-4.png,
  wall-1440x900-4.png, wall-1440x900-4-resized-ptys.png, run-1440x900-3.png, run-1920x1080-3-fresh.png,
  run-1920x1080-3-resized.png, run-1440x900-exp-{100x24,104x24,110x24}.png, run-1920x1080-exp.png, join-1440x900-3.png,
  join-1920x1080-3.png, session-full-1440x900.png, session-full-1920x1080.png.
* Raw data: shots/measurements.json, shots/measurements2.json. Scripts: r4-fill.js (wall, run, full page),
  r4-extra.js (join, experiment), r4-annot.js (annotated shots), r4-clobber.js (hello resets PTYs to 80x24). Data and
  scripts were evidence captured during the investigation, not kept.

## Why it cannot be solved by scaling alone

Scaling is contain-only and keeps the grid's aspect. For a pane of 1.76-1.88:1 the best possible 80x24 result still leaves
14-19% unused. The only way to fill the width is more columns, and only a controller can give the PTY more columns.

## Proposed fix (not implemented)

1. `TerminalView.vue`: separate "no input" from "may resize". Add a tile mode, for example `fit="tile"`:
   * Font fixed (tile prop, about 11 px; no `MIN_FONT`/`MAX_FONT` stepping), `scrollback` 0 for tiles so FitAddon reserves no
     14 px gutter, `disableStdin` on (tiles are `pointer-events-none` already).
   * `measure()` (:106-109), the first `fit.fit()` (:315) and `scheduleResize()` (:111-113) fit the pane in tile mode even
     though no input is allowed, and the hello (:235) carries the fitted pane size, never the unfitted 80x24. Guard it: connect
     only once the host has a non-zero size, since `proposeDimensions` returns nothing before then.
   * On `welcome` (:174-182) remember `msg.role`. Role `control`: stay fitted and send `resize` (:237 likewise). Role `view`
     (the join page): keep today's scale path (the server ignores a guest's resize, `ws_viewer.go:129` answers `read_only`).
   * Keep `applyScale` as a safety net with `s = min(k, 1)` and no font stepping: when another controller resizes the session
     (`resize` message, :186-188) the tile still shows the whole screen instead of clipping.
2. `SessionTile.vue:50`: `fit="tile"` (drop `read-only` or replace it by the new no-input prop), plus `:font-size` and
   `:scrollback="0"`. `JoinCrewGrid.vue:56` keeps `fit="scale"` (guests are view-only). For that page the only remedy left is to
   shape the tile to the grid, for example `auto-rows` replaced by a fixed aspect, since the 18rem rows make a 2.7:1 pane at 1920.
3. `main.css:121-132`: the `.terminal-scale` rules (absolute, auto height, transform origin) stay for the tile; the fit measures
   the host (`.terminal-host`), not `.xterm`, so they do not interfere. The `.terminal-compact` padding (4 px) is already
   accounted for by FitAddon.
4. Optional, same file: in the full view pass `overviewRuler: { width: 1 }` (or measure without the 14 px) to recover the
   hidden scrollbar's gutter; untested.
5. No server or protocol change. Behaviour to accept and note: a tile now sets the session size to the tile's (latest
   controller wins, as the full view does). Today every admin tile attach already sets it to 80x24, so a full view open in
   another window is disturbed either way. Opening a session full and returning to the wall refits it; a wall and a full view
   of the same session in two windows fight, as two full views do.

## Verification (headless, extending the investigation's r4-fill.js)

For each wall tile and run member tile at 1440x900 and 1920x1080, fresh load, resized without reload, and after a live fourth
session or member:
* `pane.clientWidth - xtermRect.width < 1 cell` and `pane.clientHeight - xtermRect.height < 1 row` (today: 16 / 28 cells).
* `/api/sessions` `cols` equals the terminal's DOM columns (`.xterm-screen` width / cell width), and is not 80 where the pane is
  wider (the run page at 1440 should land near 90-100 columns at font 11).
* Seed a session at 148x57 before opening the wall: it must be resized to the tile's size, never to 80x24 by a stale hello
  (r4-clobber.js asserts this).
* Guest join page: still scaled, no `read_only` error toast, PTY sizes unchanged by the guest's attach.
* Resize the PTY through a second controller (WebSocket): the tile shows the whole screen (scale safety net), then refits when
  the window is resized.
* Unit: a pure helper for the tile geometry (pane, cell, padding, gutter -> cols, rows) with a vitest next to
  `web/app/utils/wall.test.ts`; `npm --prefix web run typecheck`; `npm --prefix web test`; `make web-build` then
  `make build-go`, since the checks run against the embedded dist.
* Limitation: this Chromium has no WebGL2, so the WebGL renderer path is read from source, not measured; verify once in a
  real browser.
