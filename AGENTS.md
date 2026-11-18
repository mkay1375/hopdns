# Repository instructions

## Structure and commands

- `cmd/hopdns/`: native terminal application entry point and browser launcher.
- `internal/control/`: Go API, self-executable engine supervision and portable sampled monitoring.
- `internal/engine/` and `third_party/sniproxy/`: embedded pinned upstream engine and reviewed corrections.
- `web/embed.go`: embeds the browser panel in native builds.
- `.github/workflows/native.yaml`: six native OS/architecture test and artifact builds.
- `tests/native_smoke.py`: standalone executable API/DNS/lifecycle and local HTTP/TLS protocol lab.

For native changes, run `go test ./...`, then build with
`go build -trimpath -o hopdns ./cmd/hopdns`. Run the actual binary with
`python3 tests/native_smoke.py --binary ./hopdns`. The upstream corrections are
also tested with `go test ./pkg ./pkg/acl -run TestHopDNS` from
`third_party/sniproxy`. The native test needs only Python's standard library and
uses synthetic local origins. Its generated engine configuration alone permits
private origins. Production parent configuration must refuse private origins.

Native builds must remain self-contained. Keep the browser assets embedded and
launch the same executable as the engine child. Preserve the loopback panel,
native `0.0.0.0` traffic default, bounded monitoring, and graceful child cleanup.
Transfer statistics count bytes forwarded by the relay in both deployments.


- `app/server.py`: standard-library Python API, supervised upstream engine, sampled monitoring.
- `web/`: directly served HTML, CSS, and vanilla JavaScript; no build or CDN dependency.
- `tests/test_backend.py`: backend validation, API security, lifecycle and monitoring tests.
- `tests/integration_smoke.py` and `tests/lab_origin.py`: real image against offline synthetic DNS/HTTP/TLS peers.
- `tests/ui_smoke.cjs`: Playwright UI/theme smoke; mocked API values are synthetic.
- `compose.yaml`: standalone deployment using the published image. `compose.build.yaml`: local build override.
- `docs/`: screenshots and third-party notices. `.github/workflows/ci.yaml`: verification and GHCR publishing.

Run `python3 -m unittest discover -s tests -v` for backend changes. Build with
`docker compose -f compose.yaml -f compose.build.yaml build`, then run
`python3 tests/integration_smoke.py --image hopdns:local` for networking/lifecycle
changes. Use both Compose files for every local development command. See README
for browser smoke and screenshots. No release or publication is implied by local work.

## Security and performance

Keep proxy forwarding in the pinned-source sniproxy executable with only the
reviewed domain-boundary/startup, TCP half-close, and relay byte counting patches
in `scripts/`. The Docker build must apply them and run their Go regression tests before
compiling for each target.
The panel is a separate process and must not sit in the forwarding path. Sample
and cache metrics centrally; do not add per-request logging, database writes,
per-viewer scrapes, or unbounded history/domain tracking. Test actual DNS and TLS
when changing routing, not only generated configuration.

Preserve localhost publishing defaults, same-origin and allowed-Host checks,
private destination refusal, nonroot execution, dropped capabilities, read-only
root filesystem, and bounded API concurrency/body sizes. Do not mount the Docker
socket or use privileged mode. Never commit runtime data or credentials. Keep
"all domains" wording accurate: HTTP/HTTPS through DNS, not all IP/UDP traffic.

Preserve upstream/dependency licenses when updating the pinned executable.
Keep documentation, source revision metadata, and patch in sync. Publish only
after checks pass in the canonical repository. Pushes to `main` publish
automatically. Version tags require manual dispatch with
`publish=true`. Pull requests must never publish. Use `GITHUB_TOKEN`, never
committed registry credentials.

## Workflow and review

Post updates only for important findings. For large changes, split work into
subagent parts, commit each before dependent work, then review the whole result.
Include relevant UI screenshots in the final response when changing the panel.
Read adjacent `RTK.md` if it exists.

After PR review and any fixes have been committed, verified, and pushed, confirm
the remote PR head equals the reviewed commit and leave a comment with first line:

```text
Reviewed at <full 40-character SHA of the PR head once the review's fixes are pushed>
```

Follow it with one line naming the review, for example
`Codex review against origin/main; critical fixes verified`.
For GitHub write the exact comment to a temporary file and use
`gh pr comment <n> --body-file <file>`. For GitLab use a plain MR note via
`glab api -X POST "projects/:id/merge_requests/<n>/notes" -f body=<text>` with proper
quoting and actual newlines. Any later commit invalidates the marker.
