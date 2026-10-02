# Companion design direction

The product is a quiet desktop companion for people working in the official Codex app. Settings live in our native control panel; the planned optional capsule exposes a few useful observations without taking keyboard focus. The appearance page follows this native design direction; capsule sketches below remain proposals.

## Composition

The signature is a compact, stable instrument strip: one primary measurement, a clear unit and source/availability cue, with more detail on deliberate expansion. Numbers should remain visually steady while work changes around them. Avoid turning the capsule into a miniature dashboard or filling empty states with simulated activity.

```text
Control panel (proposed)                 Capsule (proposed)
┌────────────┬───────────────────────┐  ┌──────────────────────────────┐
│ Overview   │ Codex connection      │  │ ● Working   42 tok/s    ⋯   │
│ Appearance │ Companion / limits    │  └──────────────────────────────┘
│ Capsule    │                       │  Unit/availability always visible;
│ Packages   │ A focused settings    │  details explain source and timing.
│ Settings   │ group with preview    │  The number here is illustrative.
└────────────┴───────────────────────┘
```

Page labels and final grouping are governed by the PRD, not this sketch. Implemented navigation is Overview, Appearance, Packages, Logs and Updates. Add later destinations with their working content and keep diagnostics secondary to the user's task.

## Reference system and native mapping

[Ke Spectrum 2](docs/resources/README.md) supplies a reference for calm layers, semantic colors, Chinese typography and restrained motion. It is not imported as a CSS or React dependency. WinUI 3 continues to use native controls, theme resources, Segoe UI Variable and Cascadia Mono; SwiftUI uses its native controls and system typography. Honor high-contrast settings before custom colors. New shared semantic tokens belong in the Go presentation source and are generated for each frontend.

The following exact values are design reference values from the pinned Ke source, not overrides currently applied to Codex or WinUI:

| Semantic role | Light | Dark |
| --- | --- | --- |
| Base surface | `#FFFFFF` | `#111111` |
| Secondary surface | `#F8F8F8` | `#1B1B1B` |
| Floating surface | `#FFFFFF` | `#2C2C2C` |
| Primary text | `#292929` | `#DBDBDB` |
| Secondary text | `#505050` | `#AFAFAF` |
| Accent | `#3B63FB` | `#4069FD` |
| Structural border | `#DADADA` | `#393939` |

Use spacing steps `4 / 8 / 12 / 16 / 24`, control radius `8` and grouped-surface radius `16` as starting references. Web pixels require native DIP/layout validation. Body text starts around 14, supporting text 12 and section titles 18–20; system text scaling takes precedence. Use tabular numerals for live metrics. Keep Chinese font fallback local; do not add remote font requests.

Ke's reference fonts are Source Sans 3, Source Code Pro and Noto Sans SC. Its full token engine, report grid, global CSS and font imports are outside this project. The resource has no root license file in the reviewed checkout, so only reference facts and original project-specific guidance are recorded here; copying code, fonts or artwork requires a separate license decision.

## Surface behavior

**Control panel:** one primary scroll container per page, a clear heading, concise grouped settings and actions next to the setting they affect. Keep wide content bounded and narrow layouts reachable. Prefer native controls and a few intentional spacing choices to a new branded control library.

**Capsule:** off by default. The first version uses a draggable fixed position, with hide, collapse and return to the control panel. Do not move focus on metric refresh. Following the selected Codex window and richer docking are later options after identity binding is verified; when implemented, the default layering should let another application cover both Codex and its capsule. Global always-on-top is an explicit preference. Window position, DPI, monitor removal, minimize/restore and keyboard access require real-device checks. The zcode-monitor size of 280×56 is a useful reference, not a fixed Codex specification.

**Official Codex appearance:** clearly separate “Companion appearance” from “Codex theme” and “Codex wallpaper.” Applying a companion theme alone must not silently restyle the official app. Theme packages touch only declared variables/selectors; injected components have their own root. Do not inject Ke's global stylesheet. Preserve composer readability, selections, approvals, focus and scroll position.

**Wallpaper:** local static images first, with readable foreground contrast and an immediate restore action. Blur/transparency are optional rendering choices, not a reason to reject the user's wallpaper goal or a requirement to copy Ke's opaque-only brand rule. Dynamic wallpaper and pets remain later, individually switchable features with background pause and reduced-motion behavior.

## States and accessibility

Use a small product vocabulary: connected, setup needed, unavailable and restore incomplete where appropriate. Technical adapter details belong in expanded diagnostics. Distinguish unknown from zero and stale from live. A speed label must say whether it is measured generation speed or an estimate; unavailable values display `—` with an explanation.

Use a single accent for selection and primary actions, status color paired with text or a symbol, visible keyboard focus, accessible labels and high-contrast-compatible controls. Validate keyboard-only operation, screen-reader names, 125%/150% DPI, text scaling and light/dark modes. Controls should retain comfortable native hit areas even when the capsule is compact.

Transitions may start at approximately 130 ms for state changes and 160–220 ms for panels. Respect reduced motion. Do not pulse or bounce continuously to advertise normal work. Performance targets and acceptance scenarios live in the [PRD](docs/desktop-enhancement/PRD.zh-CN.md).

## Review before implementation

For each new surface, review an actual light/dark mock or native prototype with real labels, empty/unavailable states, a long Chinese label and enlarged text. Verify that the main observation is legible, the scope is obvious and secondary controls do not compete. Then validate runtime screenshots and interaction against the same intent. Upstream screenshots and the [legacy Windows brief](docs/windows-ui-brief.md) remain evidence of the inherited interface, not proof of the new companion design.
