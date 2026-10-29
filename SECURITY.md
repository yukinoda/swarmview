# Security

## Reporting a vulnerability

Use [GitHub private vulnerability reporting](https://github.com/yukinoda/swarmview/security/advisories/new)
when available. If the private form is unavailable, open an issue requesting a
private contact channel without including exploit details or sensitive data.

Include the affected commit/release, platform, prerequisites, impact, and a
minimal reproduction. Redact session content, credentials, and private paths.
The project does not currently promise a fixed response time or backported
security fixes for older releases; use the latest revision.

## Local access model

Swarmview is a single-user local tool. It binds to `127.0.0.1` and checks the
request host; focus and communication requests also have origin checks, and
focus requests also have a content-type check. Responses carry a
Content-Security-Policy (`default-src 'self'`, no plugins, `<base>` or form
submissions, and no framing). It has no remote authentication layer and is not
designed to be exposed through a public listener or reverse proxy.

Other local OS users on the same machine are outside the threat model: they can
call the loopback API directly. The host and origin checks only defend against
browser pages, not local processes such as curl.

The dashboard can display local project paths, task titles, process activity,
and memory use. The focus endpoint can select tmux panes and, on supported
macOS setups, control Ghostty windows. Browser notifications can show task titles.

Swarmview reads process metadata, Claude subagent metadata, and bounded session
JSONL tails for generated title records. It does not read process environments
or full conversation transcripts. Ghostty identification
briefly changes a candidate client's terminal title; it does not type commands
or send input to the agent.

Only use trusted `web/index.html`, JavaScript modules, and `web/app.css` files
beside the executable: they intentionally override the embedded UI and run as
the local dashboard, under the same Content-Security-Policy. It blocks inline
`<script>` and `<style>` elements, `style` and `on*` attributes, and
non-same-origin URLs (`data:` images are allowed). Command-line tools resolved
through `PATH` must also be trusted.

The optional macOS app starts its own bundled server on loopback port 4778 and
loads that exact HTTP origin in WKWebView. It blocks other page navigation and
does not expose a native JavaScript bridge. Closing or force-quitting the app
closes a lifetime pipe so the owned server exits. Its fixed Finder tool search
path and separate WebKit preferences are documented in `desktop/macos/README.md`.

The macOS app opens the owner-only live communication socket
(`~/Library/Caches/swarmview/live.sock`, directory 0700, socket 0600) and a
private copy of its binary for the Claude plugin by default; launch with
`--no-communication` to avoid both. The CLI does so only with `-communication`.
The socket carries no conversation content. Nothing is installed automatically:
the OpenCode plugin and Claude Code hooks stay user-installed, and without
them nothing is sent. Only same-user processes can connect.

Optional installer cleanup is offered only from Applications after matching the
mounted installer's app version and both executable files. It requires an
explicit choice, rechecks the download's file identity, ejects first, and uses
macOS Trash rather than deleting files. No install helper runs persistently.
