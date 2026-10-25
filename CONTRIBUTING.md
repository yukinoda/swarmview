# Contributing

Bug reports, small focused fixes, documentation improvements, and additional
agent integrations are welcome. For a larger feature, open an issue describing
the actual workflow before building it.

## Local setup

Use the Go version in [`go.mod`](go.mod), Git, `lsof`, and tmux. Install
Chrome/Chromium to run browser checks; `CHROME_BIN` can point to a custom binary.
Python 3.8+ is needed only for the optional memory comparison script.

```sh
go build -trimpath -ldflags='-s -w' -o swarmview .
./swarmview
```

Open http://127.0.0.1:4777. UI edits in `web/index.html`, its JavaScript modules, and
`web/app.css` need only a browser refresh when the `web/` directory is next to
the binary. Go and embedded-logo changes require a rebuild and server restart.

For the optional macOS app, run `bash desktop/macos/build.sh` and
`python3 desktop/macos/check.py dist/Swarmview.app`. This requires Apple's Swift
compiler/SDK. The backend stays standard-library-only; native packaging and
WebKit checks live under [`desktop/macos`](desktop/macos/README.md).
`bash desktop/macos/dmg.sh` builds and validates the branded DMG after the app
build, using a temporary Python 3.10+ environment with pinned dmgbuild.

## Code layout

- `main.go`: command-line entry point and server startup.
- `server.go`: HTTP routes, snapshot caching, and embedded web files.
- `process.go`: process discovery, agent detection, and activity sampling.
- `claude.go`: Claude session titles and worker metadata.
- `pulse*.go`: opt-in local metadata IPC, bounded live relationship/feedback delivery,
  peer authentication, and socket lifecycle; no conversation/event history.
- `tmux.go`, `sys_*.go`: terminal navigation and platform-specific integration.
- `system.go`: machine-wide memory measurements.
- `web/`: build-free browser interface; `index.html` loads `app.css` and the
  native JavaScript entry module `app.js`:
  - `app.js`: startup, polling, snapshot freshness, and wiring feature updates.
  - `agents.js`: snapshot normalization, parentage, statuses, and metric formatting.
  - `dashboard.js`: repository/worker tree, pinning, folding, and metric summaries.
  - `rows.js`: shared task/detail rows, result cards, and terminal activation.
  - `search.js`: local search index, dialog, and keyboard navigation.
  - `notifications.js`: quiet observations, notification episodes, and tab badges.
  - `communication.js`: opt-in live stream, bounded one-shot/static feedback, and
    geometry cleanup; reads dashboard edges without owning its tree or snapshot.
  - `icons.js`, `dom.js`, `storage.js`: provider glyphs, DOM helpers, and saved preferences.
- `web/app_test.js`, `ui_test.go`: browser assertions and their Go test harness.
- `*_test.go`: tests beside their subjects; `test_helpers_test.go` contains shared fixtures.
- `desktop/macos/`: native WebView host, installer, and packaging checks.
- `integrations/opencode/`: manually configured TUI-only metadata observer; uses
  the existing provider connection and forwards identifiers to local IPC.
- `assets/`: shared branding and documentation images.

The data flow is `web/app.js` → `GET /agents` → `server.snapshot` → process and
worker discovery → JSON → the dashboard, search, and notification modules. Terminal activation is a separate
`POST /focus` request. The macOS app hosts the same interface in WebKit. Feature
modules do not import `app.js`; it coordinates them without a shared global store.
The optional TUI observer → private socket → `GET /communication` stream supplies
live relationships; `app.js` retains snapshot/render ownership. Unchanged ordered
worker key/parent/PID frames refresh feedback/freshness without rebuilding the tree.

Keep this map current in the same change when adding, moving, or changing the
responsibility of a directory or entry point. Update affected embedding, test,
and packaging paths too; individual files do not need an exhaustive listing.

## Checks

```sh
gofmt -w *.go
go test -race ./...
go vet ./...
python3 memory_check.py --self-test
git diff --check
```

The tmux integration check uses its own isolated server. Browser checks run the
actual page in a fresh Chrome profile with synthetic data. Both skip locally
if the executable is absent; CI supplies the tools on macOS and Linux.

For Ghostty changes on macOS with Automation permission already granted:

```sh
SWARMVIEW_GHOSTTY_TEST=1 go test -race -run TestGhosttyLive -v .
```

This opens temporary Ghostty views and an isolated tmux server, verifies existing
session focus and title restoration without duplicates, then cleans them up.
Do not enable it in an unattended environment that cannot approve Automation.

## Scope and style

- Keep the server standard-library-only and the UI build-free. Reuse existing
  functions before adding dependencies or layers.
- Fix the shared cause of a bug, including sibling callers. Leave a focused,
  runnable regression check for behavior changes.
- Keep process sampling and browser work cheap. Do not poll entire conversation
  histories or process environments just to improve a label.
- Treat task names and paths as untrusted display text. Preserve host/origin
  checks and avoid shell interpolation of unchecked request data.
- Preserve keyboard access, readable light/dark themes, and reduced motion.
- Update [compatibility claims](COMPATIBILITY.md) when platform or terminal
  behavior changes. Distinguish locally verified behavior from CI coverage.

The repository uses conventional commit subjects such as `fix: ...`,
`feat: ...`, and `docs: ...`. A pull request should explain the user-visible
change, how it was verified, and the platforms/terminals affected.

Changes to `main` go through pull requests with passing macOS and Linux CI.
Owner approval from **@yukinoda** is required. The repository's sole admin,
@yukinoda, can bypass the approval requirement for a pull request (including
their own); required CI still applies. Public access does not grant merge access.

## Dependency maintenance

[Dependabot](.github/dependabot.yml) checks GitHub Actions weekly with a three-day
cooldown. Minor/patch updates are grouped; major updates get separate review.
Regular version-update PRs are capped at two and run the existing CI checks.

Go currently uses only the standard library, so there is no `gomod` update job.
Add it if external modules are introduced; review Go toolchain updates manually.
Dependabot alerts and security updates are separate repository settings—the
configuration file alone does not enable them.

## Adding an agent

Executable names live in `kinds` in `process.go`; runtime wrappers and daemon
filters are nearby. Add a realistic process-table regression example. Detection
alone does not establish native session status or subagent support: document
those separately. The UI uses a monogram when no agent icon is available.

## Releases (maintainers)

### Privacy before committing or publishing

- Use synthetic task names, session identifiers, and paths in fixtures and
  screenshots. Keep real home/workspace layouts, private project names, process
  dumps, logs, credentials, and local databases out of commits.
- Inspect `git diff --cached` and author/committer metadata. Use a GitHub
  noreply address when a personal email should stay private.
- Run a local secret scanner over both the staged changes and complete history
  before publication, for example `gitleaks git --pre-commit --staged --redact`
  and `gitleaks git --log-opts="--all" --redact`. Scanners do not replace manual
  review of names, directory structures, and visible image content.
- If history was sanitized, coordinate cleanup of other clones and ask the
  hosting provider about obsolete commits/caches before making the repository
  public. A force-push alone does not guarantee server-side deletion.

### Building a release

Generated release notes use [`.github/release.yml`](.github/release.yml) to group
merged PRs by label, using the first matching category:

| PR label | Release-note heading |
| --- | --- |
| `enhancement` | 🚀 Features and improvements |
| `bug` | 🐛 Bug fixes |
| `documentation` | 📚 Documentation |
| `dependencies`, `github_actions` | 🔧 Dependencies and maintenance |
| No matching label | 📦 Other changes |

Apply the appropriate label before publishing a release. Conventional commit
subjects alone do not determine the category.

The release workflow verifies the repository on macOS and Linux, builds binary
archives for `darwin`/`linux` on `arm64`/`amd64`, and publishes SHA-256 checksums
and generated notes when a version tag is pushed. It does not sign or notarize
macOS binaries.

The release workflow also builds ARM/Intel macOS `.dmg` and `.app.zip` archives on a Mac.
They use ad-hoc signatures for the bundle and helper, without Developer ID
signing/notarization. CLI archives retain their existing build path.

1. Review the compatibility documentation and CI results for the release commit.
2. Tag that commit with `vMAJOR.MINOR.PATCH` (or a `-prerelease` suffix).
3. Push the tag to trigger `.github/workflows/release.yml`.
4. Inspect the published archives, checksums, and generated notes. Tags with a
   prerelease suffix are published as GitHub prereleases.

Release archives contain the binary, license, README, compatibility/contribution/
security documentation, and brand assets. Build provenance can be inspected with
`go version -m ./swarmview`.
