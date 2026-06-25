// lore brand kit generator — concept A (strata + orbit), emerald.
// Run: node gen-brand.mjs   → writes all SVG marks + design tokens into this dir.
// Single source of truth: tweak PALETTE / geometry here and regenerate everything.
// PNG/og raster is handled separately by `task brand` (rsvg-convert) — see README.
import { writeFileSync } from "node:fs"
import { dirname } from "node:path"
import { fileURLToPath } from "node:url"

const OUT = dirname(fileURLToPath(import.meta.url))

// ── Palette ────────────────────────────────────────────────────────────────
const C = {
	emerald: {
		50: "#ECFDF5", 100: "#D1FAE5", 200: "#A7F3D0", 300: "#6EE7B7", 400: "#34D399",
		500: "#10B981", 600: "#059669", 700: "#047857", 800: "#065F46", 900: "#064E3B", 950: "#022C22",
	},
	lime: { 300: "#BEF264", 400: "#A3E635", 500: "#84CC16", 600: "#65A30D" },
	amber: { 500: "#F59E0B" },
	ink: "#0B0B12",
	white: "#FFFFFF",
}

// ── Shared geometry (512 master) ─────────────────────────────────────────────
const grad = (id, a, b, x2 = 512, y2 = 512) =>
	`<linearGradient id="${id}" x1="0" y1="0" x2="${x2}" y2="${y2}" gradientUnits="userSpaceOnUse"><stop stop-color="${a}"/><stop offset="1" stop-color="${b}"/></linearGradient>`

// strata records + recall orbit + satellite, in 0..512 space
const glyph = (fg, accent, { orbit = true } = {}) =>
	`${orbit ? `<ellipse cx="256" cy="256" rx="156" ry="96" transform="rotate(-24 256 256)" fill="none" stroke="${fg}" stroke-opacity="0.30" stroke-width="6"/>\n  ` : ""}<circle cx="383" cy="182" r="17" fill="${accent}"/>
  <g fill="${fg}">
    <rect x="168" y="196" width="176" height="40" rx="20"/>
    <rect x="148" y="252" width="216" height="40" rx="20"/>
    <rect x="168" y="308" width="176" height="40" rx="20"/>
  </g>`

const svgOpen = (w, h, label) =>
	`<svg width="${w}" height="${h}" viewBox="0 0 ${w} ${h}" fill="none" xmlns="http://www.w3.org/2000/svg" role="img" aria-label="${label}">`

// Full tile icon as a standalone file
const iconFile = (id, a, b, fg, accent, opts) =>
	`${svgOpen(512, 512, "lore")}
  <defs>${grad(id, a, b)}</defs>
  <rect width="512" height="512" rx="112" fill="url(#${id})"/>
  ${glyph(fg, accent, opts)}
</svg>
`

// ── Marks ────────────────────────────────────────────────────────────────────
const W = (f, s) => writeFileSync(`${OUT}/${f}`, s)

// color (primary), dark-tile, light-tile
W("lore-icon.svg",       iconFile("g-color", C.emerald[500], C.emerald[700], C.white,       C.lime[400]))
W("icon.svg",            iconFile("g-color2", C.emerald[500], C.emerald[700], C.white,      C.lime[400])) // generic alias (filemark parity)
W("lore-icon-dark.svg",  iconFile("g-dark",  C.emerald[800], C.emerald[950], C.emerald[50], C.lime[400]))
W("lore-icon-light.svg", iconFile("g-light", C.emerald[50],  C.emerald[100], C.emerald[700], C.lime[600]))

// favicon — drop the orbit so it stays legible at 16px
W("favicon.svg",         iconFile("g-fav",   C.emerald[500], C.emerald[700], C.white,       C.lime[400], { orbit: false }))

// glyph only (no tile) for light backgrounds — emerald strata + amber spark
W("lore-glyph.svg",      `${svgOpen(512, 512, "lore glyph")}\n  ${glyph(C.emerald[700], C.amber[500])}\n</svg>\n`)

// monochrome — single color via currentColor (terminals, print, one-color print)
W("lore-mono.svg",       `${svgOpen(512, 512, "lore")}\n  ${glyph("currentColor", "currentColor")}\n</svg>\n`)

// ── Wordmark + lockups ───────────────────────────────────────────────────────
const FONT = "'Space Grotesk', 'Geist', ui-sans-serif, system-ui, -apple-system, 'Segoe UI', Roboto, sans-serif"
const wordmark = (fill) =>
	`<text x="0" y="104" font-family="${FONT}" font-size="132" font-weight="600" letter-spacing="-6" fill="${fill}">lore</text>`

// standalone wordmark (ink, on light)
W("lore-wordmark.svg",
	`${svgOpen(300, 140, "lore")}\n  ${wordmark(C.ink)}\n</svg>\n`)

// horizontal lockups: scaled color icon + wordmark
const lockup = (id, a, b, fg, accent, textFill) =>
	`${svgOpen(620, 200, "lore")}
  <defs>${grad(id, a, b)}</defs>
  <g transform="translate(20,36) scale(0.25)">
    <rect width="512" height="512" rx="112" fill="url(#${id})"/>
    ${glyph(fg, accent)}
  </g>
  <text x="176" y="132" font-family="${FONT}" font-size="116" font-weight="600" letter-spacing="-5" fill="${textFill}">lore</text>
</svg>
`
W("lore-lockup.svg",       lockup("l-color", C.emerald[500], C.emerald[700], C.white,       C.lime[400], C.ink))        // on light
W("lore-lockup-dark.svg",  lockup("l-dark",  C.emerald[500], C.emerald[700], C.white,       C.lime[400], C.emerald[50])) // on dark

// ── OG covers (1200×630) ─────────────────────────────────────────────────────
const og = (file, bgA, bgB, textMain, textSub1, textSub2, accentLine) =>
	W(file, `${svgOpen(1200, 630, "lore — give your AI coding agent a memory")}
  <defs>
    ${grad("og-bg", bgA, bgB, 1200, 630)}
    ${grad("og-icon", C.emerald[500], C.emerald[700])}
  </defs>
  <rect width="1200" height="630" fill="url(#og-bg)"/>
  <g transform="translate(96,180) scale(0.52)">
    <rect width="512" height="512" rx="112" fill="url(#og-icon)"/>
    ${glyph(C.white, C.lime[400])}
  </g>
  <text x="412" y="292" font-family="${FONT}" font-size="132" font-weight="600" letter-spacing="-5" fill="${textMain}">lore</text>
  <rect x="416" y="324" width="86" height="8" rx="4" fill="${accentLine}"/>
  <text x="414" y="392" font-family="${FONT}" font-size="40" font-weight="500" fill="${textSub1}">Give your AI coding agent a memory.</text>
  <text x="414" y="446" font-family="${FONT}" font-size="29" font-weight="400" fill="${textSub2}">Local-first · open source · free.</text>
</svg>
`)
og("og-cover.svg",       C.emerald[800], C.emerald[950], C.white,       C.emerald[100], C.emerald[300], C.lime[400]) // dark
og("og-cover-light.svg", C.emerald[50],  C.emerald[100], C.emerald[900], C.emerald[800], C.emerald[600], C.emerald[600]) // light

console.log("✓ marks + lockups + og written")

// ── Design tokens ────────────────────────────────────────────────────────────
const palette = {
	$meta: { name: "lore", concept: "strata + orbit", brand: "emerald", generated_by: "gen-brand.mjs" },
	emerald: C.emerald,
	lime: C.lime,
	amber: C.amber,
	neutral: { ink: C.ink, white: C.white, 100: "#F4F4F5", 300: "#D4D4D8", 500: "#71717A", 700: "#3F3F46", 900: "#18181B" },
	accent: C.lime[400],
	semantic: {
		light: { bg: C.white, surface: C.emerald[50], text: C.emerald[950], muted: C.emerald[700], brand: C.emerald[600], brandStrong: C.emerald[700], accent: C.lime[600], border: C.emerald[100] },
		dark: { bg: C.emerald[950], surface: C.emerald[900], text: C.emerald[50], muted: C.emerald[300], brand: C.emerald[400], brandStrong: C.emerald[300], accent: C.lime[400], border: C.emerald[800] },
	},
}
W("palette.json", `${JSON.stringify(palette, null, 2)}\n`)

const tokens = {
	$meta: palette.$meta,
	color: palette,
	font: {
		display: "Space Grotesk",
		sans: "Inter",
		mono: "JetBrains Mono",
		stack: { display: FONT, sans: "'Inter', ui-sans-serif, system-ui, sans-serif", mono: "'JetBrains Mono', ui-monospace, SFMono-Regular, Menlo, monospace" },
		weight: { regular: 400, medium: 500, semibold: 600, bold: 700 },
		tracking: { tight: "-0.03em", normal: "0", wide: "0.02em" },
	},
	radius: { sm: "6px", md: "10px", lg: "16px", xl: "24px", tile: "112px@512", pill: "9999px" },
	icon: { master: "512", favicon: [16, 32, 48, 180, 512], og: [1200, 630] },
}
W("tokens.json", `${JSON.stringify(tokens, null, 2)}\n`)

const css = `:root {
  /* lore brand — emerald (concept: strata + orbit). Generated by gen-brand.mjs. */
  --lore-emerald-50: ${C.emerald[50]};   --lore-emerald-100: ${C.emerald[100]};
  --lore-emerald-200: ${C.emerald[200]}; --lore-emerald-300: ${C.emerald[300]};
  --lore-emerald-400: ${C.emerald[400]}; --lore-emerald-500: ${C.emerald[500]};
  --lore-emerald-600: ${C.emerald[600]}; --lore-emerald-700: ${C.emerald[700]};
  --lore-emerald-800: ${C.emerald[800]}; --lore-emerald-900: ${C.emerald[900]};
  --lore-emerald-950: ${C.emerald[950]};
  --lore-lime-400: ${C.lime[400]}; --lore-amber-500: ${C.amber[500]};
  --lore-ink: ${C.ink};

  /* fonts */
  --lore-font-display: ${FONT};
  --lore-font-sans: 'Inter', ui-sans-serif, system-ui, sans-serif;
  --lore-font-mono: 'JetBrains Mono', ui-monospace, SFMono-Regular, Menlo, monospace;
  --lore-radius: 16px;

  /* semantic — light (default) */
  --lore-bg: ${palette.semantic.light.bg};
  --lore-surface: ${palette.semantic.light.surface};
  --lore-text: ${palette.semantic.light.text};
  --lore-muted: ${palette.semantic.light.muted};
  --lore-brand: ${palette.semantic.light.brand};
  --lore-brand-strong: ${palette.semantic.light.brandStrong};
  --lore-accent: ${palette.semantic.light.accent};
  --lore-border: ${palette.semantic.light.border};
}

[data-theme="dark"], .dark {
  --lore-bg: ${palette.semantic.dark.bg};
  --lore-surface: ${palette.semantic.dark.surface};
  --lore-text: ${palette.semantic.dark.text};
  --lore-muted: ${palette.semantic.dark.muted};
  --lore-brand: ${palette.semantic.dark.brand};
  --lore-brand-strong: ${palette.semantic.dark.brandStrong};
  --lore-accent: ${palette.semantic.dark.accent};
  --lore-border: ${palette.semantic.dark.border};
}
`
W("tokens.css", css)
console.log("✓ palette.json, tokens.json, tokens.css written")
