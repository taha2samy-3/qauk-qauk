# Quack Quack brand assets

These files are the source of truth. Copy them where they are needed (`web/public/`, `docs/public/`); don't edit the
copies.

## Logo

| File | Use |
|---|---|
| `logo-mark.svg` | App icon / favicon. A duck whose quack is a signal. |
| `logo.svg` | Horizontal wordmark. It switches text color with `prefers-color-scheme`, so it works in `<img>` tags on both themes. |
| `logo-light.svg` / `logo-dark.svg` | Wordmark with fixed colors, for light or dark backgrounds. Use them where media queries don't apply, e.g. GitHub `<picture>`. |
| `logo-mark-mono-dark.svg` / `logo-mark-mono-light.svg` | One-color marks for print, stamps and embossing. |
| `favicon-32.png`, `apple-touch-icon.png` (180 px), `icon-512.png` | Raster icons (PWA, iOS home screen). |
| `social-card.svg` / `social-card.png` (1280×640) | GitHub social preview and the docs `og:image`. |

**Colors**

| Token | Value |
|---|---|
| Primary gradient | `#4F46E5` (indigo-600) → `#0891B2` (cyan-600) |
| Beak / accent | `#FBBF24` (amber-400) |
| Outline / ink | `#312E81` / `#1E1B4B` |
| Text | `#0F172A` on light, `#F8FAFC` on dark |

**Clear space:** at least 1/4 of the mark's width on every side. Don't recolor the gradient, stretch the mark, or put
the color mark on busy photos (use a mono mark instead).

## Duck illustrations (`ducks/`, 240×240)

All poses share the logo's duck geometry, so they always match the mark.

| File | Pose | Where it's used |
|---|---|---|
| `duck-hello.svg` | Sending a signal | Hero / welcome / login |
| `duck-sleeping.svg` | Asleep, "zZ" | Offline device, nothing streaming yet |
| `duck-detective.svg` | Magnifying glass | Search with no results |
| `duck-builder.svg` | Hard hat + gear | Empty dashboard / editor / setup |
| `duck-analyst.svg` | Live chart | Dashboards list, analytics |
| `duck-guardian.svg` | Shield with keyhole | Permissions / access denied |
| `duck-plugged.svg` | Plugging in a device | No devices yet / connect a device |
| `duck-celebrate.svg` | Party hat + confetti | Success, first dashboard saved |
| `duck-lost.svg` | Question marks | 404 / page not found |

The SVGs embed no fonts except the "z" and "?" glyphs, which use a system font stack.
