# Rivune design system

Rivune is content-first and cinematic: near-black neutral surfaces, artwork as the main color source, and a restrained ember accent for the primary action and active focus. Avoid decorative gradients, generic Material defaults, nested cards, heavy shadows, and accent color on every control.

## Tokens

| Role | Value | Use |
| --- | --- | --- |
| Canvas | `#050505` | Full-screen background |
| Surface / raised | `#0D0D0D` / `#141414` | Content and controls |
| Hairline / outline | `#242424` / `#393939` | Separation and boundaries |
| Primary / secondary text | `#F4F1EC` / `#C8C4BE` | Content hierarchy |
| Muted text | `#8C929A` | Secondary metadata only |
| Ember / pressed | `#FF8F70` / `#E77A5F` | Primary action and focus |
| Danger / success / warning | `#FF8D92` / `#79D5AE` / `#E8B870` | Semantic status |

Use the native system sans family. Display and headline roles are bold and compact; body text remains readable and wraps rather than losing meaning. Do not encode hierarchy through size or color alone.

- Spacing: **4, 8, 12, 16, 20, 24, 32, 40, 48, 64**.
- Radii: **8, 14, 20, 28**, plus a pill.
- Elevation: **0** content, **2** functional surfaces, **6** overlays.
- Motion: **140ms** fast, **240ms** standard, **420ms** slow.
- Targets: at least **48dp** touch and **56dp** TV/directional focus.

Prefer spacing, tone, and a 1dp hairline over shadows. Artwork is never tinted to match the accent. Missing imagery uses a neutral placeholder, not fabricated art.

## Interaction and accessibility

- Preserve native roles, headings, selected/disabled/error state, and screen-reader labels.
- Use a visible 2dp ember focus ring; TV focus may scale only to 1.015.
- Respect RTL, large text, safe areas, and reduced motion.
- Never rely on color alone; keep muted text readable.
- Stop decorative animation when system animation is disabled.
- Use wrapping or scrolling instead of shrinking text or targets.

Phone is the compact reference surface. Tablets add width and spacing instead of scaling everything. TV requires explicit directional focus. Wide layouts use bounded content widths.

## Cross-platform contract

Platforms share semantic roles—canvas, surface, text, accent, spacing, radius, motion, focus—not component names. Android maps them through `MaterialTheme` and Rivune tokens; web uses equivalent CSS custom properties and `:focus-visible`. Interaction details remain native to each platform.
