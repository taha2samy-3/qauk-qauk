# Brand & logos

The source files live in [`branding/`](https://github.com/taha2samy-3/qauk-qauk/tree/main/branding)
in the repository. Every file below can be downloaded from this site.

<WavingDuck variant="wave" :width="260" alt="The Quack Quack duck waving hello" />

## Logo

The mark is a duck whose *quack* is a wireless signal: a device talking.

<div class="brand-grid">
  <figure class="brand-tile light"><img src="/brand/logo-light.svg" alt="Wordmark for light backgrounds"/><figcaption>Wordmark, light · <a :href="$withBase('/brand/logo-light.svg')" download>SVG</a></figcaption></figure>
  <figure class="brand-tile dark"><img src="/brand/logo-dark.svg" alt="Wordmark for dark backgrounds"/><figcaption>Wordmark, dark · <a :href="$withBase('/brand/logo-dark.svg')" download>SVG</a></figcaption></figure>
</div>

<div class="brand-grid icons">
  <figure class="brand-tile light"><img src="/brand/logo-mark.svg" alt="App icon"/><figcaption>App icon · <a :href="$withBase('/brand/logo-mark.svg')" download>SVG</a> · <a :href="$withBase('/brand/icon-512.png')" download>PNG 512</a></figcaption></figure>
  <figure class="brand-tile light"><img src="/brand/logo-mark-mono-dark.svg" alt="Monochrome mark, dark"/><figcaption>Mono, dark · <a :href="$withBase('/brand/logo-mark-mono-dark.svg')" download>SVG</a></figcaption></figure>
  <figure class="brand-tile dark"><img src="/brand/logo-mark-mono-light.svg" alt="Monochrome mark, light"/><figcaption>Mono, light · <a :href="$withBase('/brand/logo-mark-mono-light.svg')" download>SVG</a></figcaption></figure>
</div>

| File | Use it for |
|---|---|
| `logo.svg` | Wordmark that switches its text color with the viewer's light/dark preference |
| `logo-light.svg` / `logo-dark.svg` | Wordmark on a known light or dark background (e.g. GitHub `<picture>`) |
| `logo-mark.svg`, `icon-512.png` | App icon, favicon, avatars |
| `logo-mark-mono-*.svg` | One-color print, stamps, embossing |

## Extended marks & specialized icons

Contextual extensions of the original mark for specialized subsystems and documentation pages:

<div class="brand-grid icons">
  <figure class="brand-tile light">
    <img src="/brand/logo-pipeline.svg" alt="Element Pipeline mark"/>
    <figcaption><strong>Pipeline</strong><br/>Stream processing<br/><a :href="$withBase('/brand/logo-pipeline.svg')" download>SVG</a></figcaption>
  </figure>
  <figure class="brand-tile light">
    <img src="/brand/logo-gateway.svg" alt="Gateway Hub mark"/>
    <figcaption><strong>Gateway</strong><br/>Multi-transport hub<br/><a :href="$withBase('/brand/logo-gateway.svg')" download>SVG</a></figcaption>
  </figure>
  <figure class="brand-tile light">
    <img src="/brand/logo-analytics.svg" alt="Telemetry & Analytics mark"/>
    <figcaption><strong>Analytics</strong><br/>Timeseries telemetry<br/><a :href="$withBase('/brand/logo-analytics.svg')" download>SVG</a></figcaption>
  </figure>
  <figure class="brand-tile dark">
    <img src="/brand/logo-cyber.svg" alt="Cyber Dark mark"/>
    <figcaption><strong>Cyber Dark</strong><br/>Neon developer edition<br/><a :href="$withBase('/brand/logo-cyber.svg')" download>SVG</a></figcaption>
  </figure>
  <figure class="brand-tile light">
    <img src="/brand/logo-shield.svg" alt="Security Shield mark"/>
    <figcaption><strong>Security</strong><br/>Keys & RBAC<br/><a :href="$withBase('/brand/logo-shield.svg')" download>SVG</a></figcaption>
  </figure>
  <figure class="brand-tile light">
    <img src="/brand/logo-badge.svg" alt="Circular Badge mark"/>
    <figcaption><strong>Badge</strong><br/>Circular avatar & token<br/><a :href="$withBase('/brand/logo-badge.svg')" download>SVG</a></figcaption>
  </figure>
  <figure class="brand-tile light">
    <img src="/brand/logo-alerts.svg" alt="Alerts &amp; Webhooks mark"/>
    <figcaption><strong>Alerts &amp; Channels</strong><br/>Multi-channel notifications<br/><a :href="$withBase('/brand/logo-alerts.svg')" download>SVG</a></figcaption>
  </figure>
</div>

<div class="brand-grid">
  <figure class="brand-tile light">
    <img src="/brand/logo-pipeline-wordmark.svg" alt="Element Pipeline wordmark"/>
    <figcaption>Pipeline engine wordmark · <a :href="$withBase('/brand/logo-pipeline-wordmark.svg')" download>SVG</a></figcaption>
  </figure>
  <figure class="brand-tile light">
    <img src="/brand/logo-alerts-wordmark.svg" alt="Alerts &amp; Webhooks wordmark"/>
    <figcaption>Alerts &amp; channels wordmark · <a :href="$withBase('/brand/logo-alerts-wordmark.svg')" download>SVG</a></figcaption>
  </figure>
</div>

## Social card

Used as the GitHub social preview and the `og:image` of this site.

<figure class="brand-tile wide"><img src="/brand/social-card.svg" alt="Quack Quack social card"/><figcaption><a :href="$withBase('/brand/social-card.svg')" download>SVG</a> · <a :href="$withBase('/social-card.png')" download>PNG 1280×640</a></figcaption></figure>

## The welcoming ducks

These animated 3D ducks greet visitors in the docs. They wave, blink and quack. Visitors who prefer reduced motion
automatically get a still version.

<div class="brand-grid">
  <figure class="brand-tile light"><WavingDuck variant="welcome" :width="300" alt="Duck waving next to a Welcome sign"/><figcaption>Welcome · <a :href="$withBase('/ducks-3d/duck-3d-welcome.svg')" download>SVG</a></figcaption></figure>
  <figure class="brand-tile light"><WavingDuck variant="hi" :width="280" alt="Duck saying Hi there"/><figcaption>Hi there · <a :href="$withBase('/ducks-3d/duck-3d-hi.svg')" download>SVG</a></figcaption></figure>
</div>

## Colors

| Token | Value | |
|---|---|---|
| Primary gradient | `#4F46E5` → `#0891B2` | <span class="swatch" style="background: linear-gradient(90deg,#4F46E5,#0891B2)"></span> |
| Beak / accent | `#FBBF24` | <span class="swatch" style="background:#FBBF24"></span> |
| Ink | `#1E1B4B` | <span class="swatch" style="background:#1E1B4B"></span> |
| Text on light | `#0F172A` | <span class="swatch" style="background:#0F172A"></span> |
| Text on dark | `#F8FAFC` | <span class="swatch" style="background:#F8FAFC; border:1px solid #CBD5E1"></span> |

**Do:** keep clear space around the mark of at least 1/4 of its width.

**Don't:** recolor the gradient, stretch the mark, or put the color mark on busy photos (use a mono mark instead).

<style>
.brand-grid { display: grid; grid-template-columns: repeat(auto-fit, minmax(240px, 1fr)); gap: 16px; margin: 16px 0; }
.brand-grid.icons { grid-template-columns: repeat(auto-fit, minmax(160px, 1fr)); }
.brand-tile { margin: 0; padding: 20px; border-radius: 12px; border: 1px solid var(--vp-c-divider); display: flex; flex-direction: column; align-items: center; gap: 12px; }
.brand-tile.light { background: #ffffff; }
.brand-tile.dark { background: #0f172a; }
.brand-tile.dark figcaption, .brand-tile.dark figcaption a { color: #e2e8f0; }
.brand-tile.light figcaption { color: #334155; }
.brand-tile img { max-height: 96px; width: auto; }
.brand-tile .waving-duck img { max-height: 240px; }
.brand-grid.icons .brand-tile img { height: 72px; }
.brand-tile.wide img { max-height: none; width: 100%; border-radius: 8px; }
.brand-tile figcaption { font-size: 13px; text-align: center; }
.swatch { display: inline-block; width: 64px; height: 20px; border-radius: 6px; vertical-align: middle; }
</style>
