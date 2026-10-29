# Trademarks

Who owns the marks in this repository, and what may be done with them. The
Apache License 2.0 (`LICENSE`) covers the code; it does not cover the marks.

## The marks

- The name "Conductor", as the name of this software.
- The junction mark, the Switch: a C of two parallel routes with a separate,
  right-leaning terracotta junction (`docs/design/brand.md`), and the
  lockups, the app icon and the favicon that carry it: the seven SVGs in
  `web/public/brand/` (`conductor-logo.svg`, `conductor-logo-reversed.svg`,
  `conductor-logo-mono.svg`, `conductor-mark.svg`,
  `conductor-mark-reversed.svg`, `conductor-favicon.svg`,
  `conductor-app-icon.svg`).
- The RockSolid Labs name and logos: `web/public/sponsor/` and
  `desktop/static/sponsor/`, three PNGs each (`rocksolidlabs-logo.png`,
  `rocksolidlabs-logo-reversed.png`, `rocksolidlabs-mark.png`).

## The owner

RockSolid Labs, Inc. (rocksolidlabs.io), which sponsors and maintains
Conductor, owns all of them.

## What the licence does not grant

The Apache License 2.0 covers the code. Its section 6 grants no right to the
marks: the licence "does not grant permission to use the trade names,
trademarks, service marks, or product names of the Licensor, except as
required for reasonable and customary use in describing the origin of the
Work and reproducing the content of the NOTICE file". The image files are in
the repository so that the unmodified software builds and shows them; that
is the whole of what their presence allows.

## Allowed without asking

- Redistributing unmodified builds of this repository with the marks as they
  are: packages, images, a mirror.
- Plain references in words that do not imply endorsement: "built on
  Conductor", "a fork of Conductor", "works with Conductor".
- The name, to identify this software in talks, articles, package lists and
  compatibility notes.
- Screenshots of the unmodified software.

## Not allowed without written permission

- A modified build, or a product derived from this repository, named or
  branded Conductor, or under a name confusingly close to it.
- The junction mark or the logos on anything that is not this project: a
  product, a site, a store listing, a domain name, a company name.
- Anything that implies RockSolid Labs made, endorses or sponsors something
  it did not.
- The RockSolid Labs logos anywhere except as the unmodified software shows
  them.

## A fork

Rename it and drop the marks. Where the images are:

- `web/public/brand/`: the SVGs.
- `web/public/sponsor/` and `desktop/static/sponsor/`: the sponsor PNGs.
- `desktop/build/`: the committed rasters the desktop packages carry
  (`desktop/electron-builder.yml`): `icon.png`, `icon-512.png` and
  `tray.png`, rendered from the app icon by
  `scripts/brand_assets.py --desktop-icons`, and the Windows installer's
  `installerSidebar.bmp` and `installerHeader.bmp`, rendered from
  `scripts/installer-art.html` by `scripts/installer-art.mjs`.

Where the name and the credit are written in:

- `web/app/utils/about.ts`: the credit, the sponsor's name and site;
- `web/app/components/AboutCard.vue` and `web/app/pages/join/[token].vue`:
  the About card and the join page's credit;
- `web/app/layouts/default.vue`: the sidebar's name;
- `desktop/src/splash.ts` and `desktop/static/splash.html`: the splash;
- `internal/cli/root.go`: `Credit`, the line `version` and `--help` end with;
- `internal/api/switchyard_page.go`: the header's marks, the footer's badge;
- `desktop/electron-builder.yml`: `productName`, `copyright`, the invite
  protocol's name;
- `web/nuxt.config.ts`: the title;
- `README.md`: the footer;
- `docs/design/brand.md`.

That is where to start, not the whole of it: the name is in page titles,
messages and documentation too, and `git grep -w Conductor` finds every
site. What a renamed build then shows is the fork's to check.

## The spelling

"Conductor". "RockSolid Labs", and "RockSolid Labs, Inc." for the legal
entity. The site is rocksolidlabs.io; the GitHub organization is
`rocksolidlabs`.

## Questions

conductor@rocksolidlabs.io.
