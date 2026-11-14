# Hashwatch design spec (v0.3)

Reference bar: Linear, Vercel, Stripe Dashboard, Apple Stocks, Raycast.

## Principles
- One hero per page; everything else is quieter. No grid of equal boxes.
- Numbers are the product: big, tabular, animated. Labels are small and muted.
- Accent (amber) only for the primary series, the active selection and one primary action per view.
- Semantic colour only for status (good / warn / bad).

## Tokens
| Token | Value | Use |
| --- | --- | --- |
| bg-0 | #070809 | app background |
| bg-1 | #0b0c0e | sidebar, tab bar |
| bg-2 | #101114 | panels |
| bg-3 | #15171b | inputs, hover, segmented track |
| bg-4 | #1c1f24 | segmented thumb, tooltips |
| line | rgba(255,255,255,.06) | hairlines |
| line-2 | rgba(255,255,255,.10) | control borders |
| hi | inset 0 1px 0 rgba(255,255,255,.04) | top highlight on raised surfaces |
| text-1 / 2 / 3 | #f3f4f6 / #9aa0a9 / #5f656e | primary / secondary / tertiary |
| accent | #f5a524 (soft: rgba(245,165,36,.14)) | primary series, selection |
| good / warn / bad | #3ccf8e / #f0b44c / #f06363 | status only |
| radius | 16 panel · 10 control · 8 chip · 999 pill | |
| space | 4 · 8 · 12 · 16 · 24 · 32 · 48 · 64 | 8-pt grid |
| shadow | 0 1px 0 rgba(255,255,255,.04) inset, 0 24px 48px -24px rgba(0,0,0,.7) | elevation |

## Type (Geist, tabular numerals via `tnum`)
| Style | Size / line / weight / tracking |
| --- | --- |
| Hero number | clamp(56px, 7vw, 104px) / 0.9 / 600 / -0.05em |
| Large title (iOS) | 34 / 1.1 / 700 / -0.03em |
| Page title | 22 / 1.2 / 600 / -0.02em |
| Metric | 28 / 1 / 600 / -0.03em |
| Section title | 15 / 1.3 / 600 / -0.01em |
| Body | 14 / 1.5 / 450 |
| Label (the only label style) | 12.5 / 1.3 / 500, text-2 |
| Mono (hosts, firmware) | Geist Mono 12.5 |

## Components
- **Sidebar** (≥1024): brand, miner switcher, 6 nav items with Lucide icons, live status footer.
  Collapses to a 72px icon rail on iPad portrait (700–1023).
- **Glass header**: sticky, `backdrop-filter: blur(20px) saturate(180%)`, hairline bottom.
- **iPhone**: large-title header that collapses into a compact glass bar on scroll; bottom tab bar
  (Overview, Performance, Hardware, Pools, Controls) with safe-area padding. Raw data is reached
  from Hardware.
- **Hero**: label → count-up number + unit → delta pill → one status line. Full-bleed area chart
  with amber gradient, crosshair + floating tooltip, reveal animation.
- **Metric strip**: one panel split by hairlines (temp, power, efficiency, fan, shares), each with a
  sparkline. Replaces six cards.
- **Inset list** (iOS grouped style) for key/value detail instead of dense tables.
- **Segmented control**: track bg-3, sliding thumb bg-4 with highlight, 36/44px tall.
- **Heatmap**: chip tiles tinted by temperature on a cool→hot ramp, numbers centred.
- **Light panel**: live device preview (glow follows colour, brightness and effect), 10 small colour
  chips, brightness slider, effect segmented control, "Apply" appears only when something changed.
