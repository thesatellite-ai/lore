# lore — brand

Visual identity for **lore**. Concept: **strata + orbit** — stacked memory records (the knowledge lore accumulates) with a recalling satellite node (recall, and a nod to *thesatellite-ai*). Brand color: **emerald**.

Everything here is generated from a single source — edit [`gen-brand.mjs`](./gen-brand.mjs) and run `task gen` (or `node gen-brand.mjs`); raster with `task raster`. Never hand-edit the output SVGs/PNGs.

## Files

| File | What | Use |
|---|---|---|
| `lore-icon.svg` / `icon.svg` | Primary app icon — emerald tile, white records, recall orbit, lime satellite | Default everywhere |
| `lore-icon-dark.svg` | Dark-tile variant (deep emerald) | On bright/colored surfaces |
| `lore-icon-light.svg` | Light-tile variant (mint, emerald glyph) | On dark surfaces / light UI chrome |
| `lore-glyph.svg` | Glyph only, no tile (emerald + amber spark) | On white, inline, watermarks |
| `lore-mono.svg` | Single color via `currentColor` | Terminals, print, one-color contexts |
| `favicon.svg` | Orbit removed for legibility at 16px | Browser tab |
| `lore-wordmark.svg` | `lore` wordmark (ink) | Standalone wordmark |
| `lore-lockup.svg` / `-dark.svg` | Icon + wordmark, horizontal | Headers, nav, READMEs |
| `og-cover.svg` / `-light.svg` | 1200×630 social card | OpenGraph / Twitter, docs hero |
| `png/` | Rasterized favicons, apple-touch, 512/1024 icons, OG covers | Production embedding |
| `favicon.ico` | Multi-res 16/32/48 | Legacy favicon |
| `palette.json` · `tokens.json` · `tokens.css` | Design tokens | App theming |

## Color

Emerald scale (50→950) + lime accent. Full values in `palette.json`; CSS variables in `tokens.css` (light is default, `[data-theme="dark"]` / `.dark` flips semantics).

| Token | Hex | Role |
|---|---|---|
| emerald-500 | `#10B981` | Tile gradient start |
| emerald-700 | `#047857` | Tile gradient end, brand-strong |
| emerald-600 | `#059669` | Brand (light mode) |
| emerald-400 | `#34D399` | Brand (dark mode) |
| lime-400 | `#A3E635` | Accent — the recall satellite |
| amber-500 | `#F59E0B` | Spark accent (glyph-on-white) |
| emerald-950 | `#022C22` | Dark surface / OG background |
| ink | `#0B0B12` | Neutral text on light |

## Type

All open-source (OFL), free to self-host or load via Google Fonts.

| Role | Font | Notes |
|---|---|---|
| Display / wordmark | **Space Grotesk** (600) | Tight tracking (`-0.03em`). The `lore` wordmark + OG headline |
| UI / body | **Inter** | App and docs text |
| Code / mono | **JetBrains Mono** | CLI output, code samples |

Stacks are in `tokens.json` → `font.stack`. The SVG wordmark/OG declare Space Grotesk with a system fallback, so previews render even without the webfont installed; embed the webfont for the true mark.

## Regenerate

```sh
task gen      # SVG marks + tokens from gen-brand.mjs
task raster   # PNGs + favicon.ico (needs rsvg-convert + magick)
task all      # both
```

## Don'ts

- Don't recolor outside the palette — pick a tile variant instead.
- Don't stretch or rotate the mark; the orbit angle is intentional.
- Don't add effects (shadows, bevels). The mark is flat.
- Don't put the full orbit icon below ~24px — use `favicon.svg` (orbit removed).
- Keep clearspace ≥ 25% of the icon's width on all sides.
