# Conductor design language — The Switch

**Status:** Implemented visual direction in the combined native-assistance PR, pending review.
The selected design replaces the earlier orange dot with a right-leaning
terracotta parallelogram. This does not accept architectural ADRs or adopt the
separate product-vocabulary proposal. It does not grant merge authority.

![Conductor primary logo](../../apps/web/public/brand/conductor-logo.svg)

## Identity

**Conductor — Engineering intent, orchestrated.**

The C consists of two parallel routes. The separate, right-leaning junction
suggests intentional direction and the train origin of the name. Preserve the
double route, angled terracotta terminal and transparent negative-space gap.
The junction is not a dot, arrowhead, diamond, checkmark or live status indicator.

Production artwork uses flat vector geometry. Exclude the texture introduced by
the concept-image previews. Do not add gradients, glows, shadows, metallic effects,
additional nodes or AI sparkles.

The tone is calm, precise, capable and human-directed. Use familiar engineering
language in the product; do not rename workflow objects to railway terminology
just to explain the brand.

## Assets and source of truth

All production assets are in [the public brand directory](../../apps/web/public/brand/).
The SVGs are self-contained paths, with accessible labels, no font binaries,
embedded raster images, scripts or external resources.

| File | Use |
|---|---|
| [conductor-logo.svg](../../apps/web/public/brand/conductor-logo.svg) | Primary horizontal lockup and editable master |
| [conductor-logo-reversed.svg](../../apps/web/public/brand/conductor-logo-reversed.svg) | Light wordmark/routes for dark surfaces; terracotta junction |
| [conductor-logo-mono.svg](../../apps/web/public/brand/conductor-logo-mono.svg) | One-color reproduction; gap retains the junction |
| [conductor-mark.svg](../../apps/web/public/brand/conductor-mark.svg) | Standalone full-detail mark at 32 px and above |
| [conductor-mark-reversed.svg](../../apps/web/public/brand/conductor-mark-reversed.svg) | Standalone mark on dark surfaces |
| [conductor-favicon.svg](../../apps/web/public/brand/conductor-favicon.svg) | Optical 16–24 px variant: heavier single C, separated junction, forest tile |
| [conductor-app-icon.svg](../../apps/web/public/brand/conductor-app-icon.svg) | Square full-bleed icon; consumer/platform supplies the mask |
| [tokens.css](../../apps/web/public/brand/tokens.css) | Shared web palette, typography, spacing and semantic tokens |
| [index.html](../../apps/web/public/brand/index.html) | Responsive static specimen, not a live application screen |

The wordmark retains the outlined Inter Display lettering from the selected
identity. It has no installed-font dependency. Do not reconstruct it with an
approximate live-text font or distribute a font binary with this pack.

The master has `mark`, `routes`, `junction` and `wordmark` IDs. The mark uses a
256-unit grid and a 17-unit nominal route thickness. Its junction path is
`M204 39.5H236L216 59.5H184Z`: 32-unit horizontal edges, 20-unit height and a
20-unit rightward shear from bottom to top. The gap is transparent, not painted
sage, so it works on different backgrounds.

Edit the master and regenerate copies. The optical favicon is defined separately
in the generator; simply shrinking the double-route mark is not optical sizing.

## Palette and semantic colors

| Role | Color | Guidance |
|---|---|---|
| Forest | `#263D35` | Main mark, primary actions, reversed-logo field |
| Terracotta | `#D26B3F` | Brand junction and restrained decorative accent |
| Sage | `#EEF1E9` | Quiet light canvas and reversed lettering |
| Ink | `#18211C` | Primary reading text |
| Muted text | `#526454` | Secondary text on sage or white |
| Accent text / focus | `#A44727` | Darker accent for text and focus outlines |
| Panel | `#FFFFFF` | Light content panels |
| Border | `#BFC9BE` | Decorative separation, not the sole control affordance |

Raw terracotta is artwork, not an ordinary-text color or a default white-label
button background. Use the darker accent-text token on light surfaces. The
checker verifies the project's 4.5:1 minimum for these specific ordinary-text pairs:

| Pair | Approximate ratio |
|---|---|
| Ink on sage | 14.45:1 |
| Muted text on sage | 5.56:1 |
| Accent text on sage | 5.24:1 |
| White on forest | 11.66:1 |

This is not an accessibility certification of the application. Check actual
controls, focus, disabled states and dark-mode combinations when adopting tokens.

Brand and operational status are separate. Never animate or recolor the junction
to imply approval, execution, verification or health. Use dedicated status tokens,
explicit text and supporting facts. Keep dedicated operational statuses and their explicit labels when applying the palette.

## Typography and layout

Use Inter where available, with the system sans-serif fallback. Use monospace for
code, commands, paths, IDs and technical evidence, not all interface copy. A remote
font service must not be required.

Prefer a 4 px spacing basis with 8, 12, 16, 24 and 32 px steps, restrained 6 px panel
corners, quiet borders and clear hierarchy. Keep panels flat instead of using heavy
decorative shadows. Show one visually primary next action within each work area.
Keep design prose readable and evidence compact but legible.

The browser workbench imports these tokens for its light canvas, flat panels,
buttons, typography and focus treatment. Dark-panel tokens remain reference values,
not a completed application dark mode.

## Placement and accessibility

Preserve aspect ratio. Reserve at least one route thickness of clear space around
the visible artwork. Prefer horizontal logos at 240 CSS px or wider; use the mark
with accessible text in tighter spaces. Inspect the actual placement.

Use the full mark from 32 px upward and the optical favicon below that. Preserve
the gap. Do not stretch, rotate, recolor one route, replace the terminal, introduce
a second accent or put the mark on a busy photograph.

An external image naming the app should use `alt="Conductor"`. A duplicate icon
beside visible Conductor text should use `alt=""`. SVG title/description elements
do not replace the embedding image's alt text. Namespace IDs when inlining several
copies on one page.

`currentColor` inherits when the monochrome SVG is inline. External SVG images
have their own document and ordinarily default to black; they do not inherit a
parent's text color. Use the explicit reversed asset on a dark surface.

Keep the tagline as readable page text rather than baking it into compact logos.
The TUI uses a compact ASCII approximation of the parallel-route C and separated
parallelogram beside its Conductor heading when terminal dimensions allow it.
Smaller terminals retain a plain heading. Text statuses remain explicit. Inline
images, truecolor, Unicode ornaments and a specific font are not prerequisites.
This approximation does not replace the vector source of truth.

The TUI uses the owner's ten-line ASCII reference, preserving its block C and
separate parallelogram:

```text
############     ________
     ####         ##    /       /
   ###    #########    /_______/
  ##    ###
 ##    ##
 ##    ##
  ##    ###
   ###    ###########
     ####
        #############
```

The name and tagline sit beside the mark when width permits. The complete mark
appears only after space is reserved for the wrapped identity/revision facts,
footer and at least six content rows. It fits an ordinary 80 by 24 browse screen;
a detailed view at that height keeps the plain heading. Taller terminals show the
full mark alongside inspected work. No squeezed replacement or cropped logo is used.
Package review and release views share this layout rule. Branding never replaces
permission, approval or evidence labels, and the SVG remains the vector source.

## Reproduction and validation

From the repository root:

```sh
python3 scripts/brand_assets.py
python3 scripts/brand_assets.py --check

# Optional raster output. Requires CairoSVG in the caller's environment;
# this is not a Conductor runtime dependency. No fonts are loaded or shipped.
python3 scripts/brand_assets.py --check --png-dir /tmp/conductor-brand-png
```

The check verifies generated-file consistency, allowed passive SVG content,
palette, selected text-pair contrast, favicon wiring and specimen references.

Optional exports include transparent logos/marks, 16/24/32/48 px favicons, and
180/512/1024 px app icons. The 180 px icon supports a future Apple touch-icon
integration; this PR does not add a touch-icon link or application manifest.

Serve the specimen without application dependencies:

```sh
python3 -m http.server 8765 --bind 127.0.0.1 --directory apps/web/public
# Open http://127.0.0.1:8765/brand/
```

Inspect desktop and narrow widths, light/forest backgrounds, 16/24/32/48 px marks,
header scale, transparency, gap separation and monochrome reproduction. Do not
use the textured generated concept boards as production artwork.

## Integration and review scope

The original asset pack was based on `main` at `832d9df` and proposed in PR #38
from `design/switch-parallelogram-brand` at `a94a895`. At the owner's request, that
branch was pulled into the native Design assistance branch with its commits retained.
The combined implementation adopts the web design tokens and logo in workflow
screens and adds responsive ASCII terminal branding. The browser workflow and
terminal acceptance checks cover the integrated presentation.

The combined PR is stacked on PR #37's guided Change authoring; #38 is superseded
by the combined review. PR #36's broader vocabulary proposal remains separate and
Proposed. This guide does not accept architectural ADRs, select a project license,
establish trademark clearance or grant merge/deployment authority.
