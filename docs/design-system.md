# Rivune design system

## Direction

Rivune is a content-first, premium cinematic interface. The canvas is neutral near-black, artwork supplies most of the color, and a warm ember/coral accent identifies the highest-priority action and active focus. The monochrome Rivune mark remains black and white. Functional layers are quiet, translucent, and edged by restrained neutral hairlines. The result should feel authored and editorial without imitating a specific platform.

Avoid rainbow gradients, generic Material defaults, decorative glass stacks, nested cards, heavy shadows, and accent color on every control. Hierarchy comes from artwork, type, spacing, and contrast before decoration.

## Color roles

| Role | Android value | Use |
| --- | --- | --- |
| Canvas | `#050505` | Full-screen content background |
| Canvas soft | `#080808` | Subtle depth and lower gradient stop |
| Surface | `#0D0D0D` | Solid content surfaces |
| Raised surface | `#141414` | Fields and elevated functional regions |
| Interactive surface | `#1B1B1B` | Press feedback and stronger control states |
| Functional layer | `#F20C0C0C` | Navigation, top bars, and control groups over content |
| Selected surface | `#202020` | Neutral selection state |
| Hairline | `#242424` | Surface separation |
| Strong outline | `#393939` | Controls and selected boundaries |
| Primary text | `#F4F1EC` | Headlines and primary labels |
| Secondary text | `#C8C4BE` | Body copy and supporting labels |
| Muted text | `#8C929A` | Metadata and disabled content only |
| Ember accent | `#FF8F70` | Primary action and active focus |
| Accent strong | `#FFAD94` | Highlight within primary controls; sparingly |
| Accent pressed | `#E77A5F` | Pressed primary action |
| Accent ink | `#25100A` | Text/icons on the accent |
| Danger | `#FF8D92` | Destructive and error states |
| Success | `#79D5AE` | Confirmed success |
| Warning | `#E8B870` | Caution that is not an error |
| Scrim | `#CC000000` | Readability behind overlays |

Artwork must not be tinted to match the accent. When imagery is missing, use a neutral charcoal placeholder with a quiet neutral monogram; do not fabricate art.

## Type scale

Use the native system sans family so language coverage, Dynamic Type, and platform rendering stay reliable. Display and headline styles are bold, compact, and slightly tight to create an editorial hierarchy without a generic serif voice.

| Concept | Size / line | Weight | Typical use |
| --- | --- | --- | --- |
| Display large | 52 / 56 | Bold | TV or expansive hero statements |
| Display medium | 44 / 48 | Bold | Large editorial statements |
| Headline large | 36 / 40 | Bold | Phone screen title |
| Headline medium | 28 / 34 | Bold | Feature or empty-state heading |
| Headline small | 24 / 30 | Semibold | Dense section emphasis |
| Title large | 21 / 27 | Bold | Section heading, brand name |
| Title medium | 16 / 22 | Semibold | Card/control title |
| Title small | 14 / 20 | Semibold | Compact control title |
| Body large | 16 / 25 | Regular | Prominent body copy |
| Body medium | 14 / 21 | Regular | Default body and metadata |
| Body small | 12 / 18 | Regular | Secondary metadata |
| Label large | 14 / 20 | Semibold | Buttons |
| Label medium | 12 / 17 | Bold, tracked | Eyebrows and compact labels |
| Label small | 11 / 16 | Semibold | Tertiary labels |

Never encode hierarchy only with size. Maintain contrast and weight, allow wrapping where meaning would otherwise be truncated, and test at large font scales.

## Spacing

The scale follows a 4dp base: **4, 8, 12, 16, 20, 24, 32, 40, 48, 64**. Use 16dp as the default phone inset and component gap, 24dp for stronger separation, and 32–48dp for major sections or larger surfaces. Do not introduce one-off spacing values.

Phone touch targets remain at least 48dp. TV and directional-focus targets remain at least 56dp. Safe drawing insets are part of the layout, not extra visual whitespace.

## Radii and elevation

Radii are **8dp small**, **14dp medium**, **20dp large**, and **28dp extra large**, plus a pill for buttons and compact status controls. Choose a radius by scale and purpose rather than rounding every container.

Elevation is restrained: 0dp for content surfaces, 2dp for a functional surface, and 6dp for overlays. Prefer tonal separation and a 1dp hairline over shadow. Do not stack elevated surfaces.

## Materials

- **Cinematic background:** full-screen neutral near-black depth. It is structural, not a decorative hero gradient.
- **Functional translucent surface:** high-opacity neutral charcoal layer with a hairline and minimal shadow. Use for navigation, control groups, and compact system feedback over imagery.
- **Content surface:** normally solid and flat. Cards should disappear when spacing or artwork can provide the grouping.
- **Selection:** a warm low-chroma surface plus semantic selected state; focus remains the brighter ember ring.
- **Fields:** soft canvas at rest, raised surface on focus, one clear outline, and visible supporting/error text.

## Motion and feedback

Motion is direct and quiet: **140ms fast**, **240ms standard**, **420ms slow**, and **1100ms ambient**. Pressed controls may scale to 0.985; TV focus may scale to 1.015. Use standard ease-out behavior, never bounce or elastic easing.

Motion must clarify state, not decorate idle screens. When system animation is disabled, decorative infinite transitions must not start; skeletons render at a stable opacity and artwork does not crossfade. Loading, selected, disabled, error, and pressed states must remain understandable without animation.

## Imagery

Artwork is the visual lead and is rendered uncropped only when its media contract requires it; otherwise use the established poster or landscape crop. Preserve source color. Overlays use scrims only for legibility. Missing or failed imagery falls back to a restrained placeholder derived from the title, with the same accessibility description as the intended artwork.

## Accessibility and focus

- Preserve TalkBack roles, selected state, state descriptions, headings, and merged semantics.
- Use a 2dp ember focus ring with sufficient separation from the control boundary.
- Do not remove default interaction semantics when replacing ripple visuals.
- Respect RTL through logical start/end placement and layout-direction-aware Compose APIs.
- Support font scaling without fixed text heights or meaning-destroying truncation.
- Never rely on color alone for selected, error, loading, or disabled state.
- Keep text contrast strong; muted text is still readable on the canvas and is not used on the accent.

## Adaptive behavior

Phone is the reference surface. Compact layouts prioritize one clear action and edge-to-edge content with safe insets. Tablets increase content width and breathing room rather than scaling every control. TV retains 56dp targets, explicit directional focus, visible focus rings, and modest focus scale. Wide layouts use bounded content widths so type and controls do not become difficult to scan.

Reduced height, large fonts, and RTL are first-class adaptive cases. Prefer wrapping and scrolling over shrinking text or targets.

## Compose to future Web concept map

| Concept | Compose | Future Web |
| --- | --- | --- |
| Color roles | internal named `Color` tokens / `MaterialTheme.colorScheme` | CSS custom properties such as `--color-canvas`, `--color-accent` |
| Type roles | `MaterialTheme.typography.*` | semantic classes backed by font-size, line-height, weight, tracking variables |
| Spacing | `RivuneSpacing` | `--space-1` through `--space-10` on the same 4px scale |
| Radii | `RivuneShapes` | `--radius-sm/md/lg/xl` and `--radius-pill` |
| Elevation | `RivuneElevation` plus surface tone | paired surface and shadow tokens; never shadow alone |
| Motion | `RivuneMotion` duration/scale tokens | duration, easing, and transform custom properties with `prefers-reduced-motion` |
| Cinematic background | `RivuneCinematicBackground` | page-shell background primitive |
| Functional layer | `RivuneFunctionalSurface` | reusable functional-surface component with hairline |
| Section hierarchy | `RivuneSectionHeading` | semantic heading row with optional trailing action |
| Focus | focus semantics plus 2dp ring | `:focus-visible` ring with the same role and minimum target |

The Web implementation should map concepts rather than Android component names. Shared visual roles and behavioral invariants are the contract; platform-native interaction details remain native.
