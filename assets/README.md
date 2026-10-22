# Brand assets

`logo.svg` is the source logo: three agent nodes connected to one observation
point. It is used in the dashboard, favicon, and project README. The original
vector artwork is included under the repository's [MIT license](../LICENSE).

- Product name: **Swarmview**.
- Repository, CLI command, URLs, and technical identifiers: **swarmview**, lowercase.
- Short description: **A local dashboard for AI coding agents.**
- Tagline: **Your agents. One view.**
- Colors: teal `#0d9488`, ink `#0f172a`, white `#ffffff`.
- Use the supplied white tile on either light or dark backgrounds. Keep its
  proportions, avoid extra effects, and leave clear space around it.
- The dashboard uses system fonts and a separate semantic status palette;
  logo colors do not indicate agent status.

The README dashboard screenshot uses illustrative demo data, not real sessions.
`dashboard-guide.svg` is the annotated README illustration, also using synthetic
tasks and metrics. Keep its labels aligned with the dashboard when changing UI
terminology; `screenshot.png` shows the fuller multi-repository layout.

`installer-light.svg` is the selected installer artwork, exported as vectors
with outlined typography from the maintainer's private Figma source. The
self-contained SVG includes the background, copy, brand mark, and arrow, but no
native Finder icons or filenames. Contributors do not need access to the design
source to build. Maintainers update it privately and re-export SVG with outlined
text rather than redrawing the artwork in code.
`desktop/macos/DMGBackground.swift` rasterizes it at build time as a Retina sRGB
PNG; no fonts, Figma URLs, or extra rendering runtime are needed by the app.
The light canvas is intentional: Finder kept native filenames black on the
tested dark image background. Its fallback fill does not control filename color.

The installer volume icon uses the system's default removable disk with
`logo.svg` composited over its download arrow by `desktop/macos/Icon.swift`.
The white-backed logo stays square on the top-down disk face, without perspective
distortion. The app icon uses the same logo with transparent macOS canvas padding
(an 824-point tile centered in a 1024-point canvas). Finder, Dock, and the app
switcher all use the generated bundle icon.

Agent glyphs in `index.html` come from [Simple Icons](https://simpleicons.org/)
(CC0), including the OpenCode mark. These identify third-party tools; they are
separate from Swarmview's own logo and do not imply endorsement.
