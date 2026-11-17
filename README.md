# HopDNS

Smart DNS and an HTTP/HTTPS relay, powered by
[mosajjal/sniproxy](https://github.com/mosajjal/sniproxy), with a lightweight web
panel. Run one Docker container on a laptop or Linux server, choose the domains
to relay, and point your device's DNS at that machine.

![HopDNS panel in the isolated protocol lab](docs/desktop.png)

The screenshots use synthetic traffic in an isolated local lab, not a connected
Xbox or PlayStation. [Dark view](docs/dark.png) · [Mobile view](docs/mobile.png).

## Run from source

Build HopDNS from source with Docker Desktop (macOS/Windows/Linux) or Docker
Engine with Compose on Linux:

```sh
git clone https://github.com/mkay1375/hopdns.git
cd hopdns
docker compose -f compose.yaml -f compose.build.yaml up --build -d
```

For an existing source folder, start at `cd hopdns`. Open
**http://localhost:48741**. Enter the address your client can reach: the laptop's
LAN IPv4 for devices on your network, or the server's public IPv4 for remote
clients. Choose selected domains or **All domains**, then save. The engine stays
stopped until an advertised IPv4 address is configured.

Ports are bound to **127.0.0.1** initially. This lets you inspect the panel and
test locally without exposing a public resolver or proxy. For console/LAN or
server access, follow the deployment section below before changing the binding.

| Service | Default host binding |
| --- | --- |
| Web panel | `127.0.0.1:48741` |
| DNS | `127.0.0.1:53` UDP and TCP |
| HTTP relay | `127.0.0.1:80` TCP |
| HTTPS/SNI relay | `127.0.0.1:443` TCP |

Use both `-f` arguments for subsequent local Compose commands:

```sh
docker compose -f compose.yaml -f compose.build.yaml ps
docker compose -f compose.yaml -f compose.build.yaml logs --tail 50
docker compose -f compose.yaml -f compose.build.yaml down
```

Configuration lives in the private `hopdns-data` named volume and survives
restarts/recreation. `down -v` deletes it. Saving changed routing settings
restarts the engine and closes existing proxy connections. Saving unchanged
settings does not restart it. A container restart automatically resumes saved
settings. Stopping the container stops DNS and proxy services; there is no
network-wide VPN or fallback-routing guarantee.

## What routing means

For a selected hostname, DNS returns the advertised HopDNS IPv4. Your client
connects to HopDNS, which reads the HTTP Host header or TLS SNI and relays the
connection to the real origin. Other hostnames resolve normally. Selected rules
include the exact domain and its subdomains, with label boundaries preserved.

**All domains** disables the domain filter and returns HopDNS for all DNS A
lookups. It covers compatible **HTTP on port 80 and HTTPS on port 443**, not
arbitrary TCP/UDP traffic, IP-literal connections, QUIC/HTTP3, or all console
gameplay. DNS-only configuration cannot create a full VPN. Full traffic routing
requires a gateway/VPN solution in addition to this project.

TLS passes through without certificate replacement or decryption. Encrypted
ClientHello (ECH), connections without usable SNI, client-controlled encrypted
DNS, hardcoded addresses, and services on other ports may bypass or fail with
this approach. This version advertises IPv4 only; proxied AAAA queries receive
no IPv6 answer. It does not configure your host, router, or client's IPv6 paths.
The upstream HTTP relay filters request/response headers; prefer HTTPS and test
application compatibility. Sample Xbox/PlayStation domains are starting points,
not a complete or validated game-service list. No console compatibility or lower
latency is guaranteed without testing the target services and network.

Upstream DNS accepts `udp://IP:port` or `tcp://IP:port`. Use an actual resolver
address different from HopDNS; do not point it back at this container. Hostnames,
URLs, credentials, arbitrary YAML, and upstream environment overrides are not
accepted through the panel. No SOCKS5-chain configuration is exposed in this
version.

## Laptop, LAN, and server deployment

Copy `.env.example` to `.env` beside Compose. On a laptop serving your console,
set `HOPDNS_TRAFFIC_BIND` to the laptop's LAN IPv4 and advertise that same LAN IPv4
in the panel. Allow only your trusted LAN clients through the host firewall.
Keep the laptop awake and reachable. The console's custom DNS field takes that
IPv4; it still needs the standard ports 53, 80, and 443 on HopDNS.

On Docker Desktop for Mac, publishing those ports on a LAN address may require
**Settings → Advanced → Allow privileged port forwarding to external machines**.
Enable it, apply the change, and rerun the Compose startup command. Docker treats
ports below 1024 as privileged, so a “Cannot bind privileged port” error can
appear even when the port is free. The web panel still binds to localhost.
[Docker Desktop port permissions](https://docs.docker.com/desktop/setup/install/mac-permission-requirements/#binding-privileged-ports)

On a server, **first restrict inbound UDP/TCP 53 and TCP 80/443 to your client
public IPs** using your cloud/provider firewall. Then set
`HOPDNS_TRAFFIC_BIND=0.0.0.0` (or the specific host interface address), advertise
the server's public IPv4 in the panel, and recreate the container. Forward the
same ports on any upstream NAT. Docker-published ports can bypass UFW's usual
INPUT rules; use an upstream firewall or correctly configured Docker forwarding
rules (`DOCKER-USER` with the iptables backend). Do not expose an unrestricted
public recursive resolver/proxy. IP changes on your home connection require
updating its allowed source addresses.

The panel remains localhost-only and has **no login**. Access a remote panel
through SSH:

```sh
ssh -N -L 48741:127.0.0.1:48741 user@your-server
```

Then open http://localhost:48741. Do not expose the panel by changing its Compose
binding or putting it behind an unauthenticated reverse proxy. Same-origin and
Host checks protect browser boundaries, but are not user authentication. Anyone
with local access can read or change routing. Upstream metrics on loopback port
9090 are never published.

Optional `.env` variables:

| Variable | Purpose |
| --- | --- |
| `HOPDNS_PUBLIC_IPV4` | Initial advertised address, used only when there is no saved config |
| `HOPDNS_TRAFFIC_BIND` | Address to publish DNS/HTTP/HTTPS; defaults to localhost |
| `HOPDNS_PANEL_PORT` | Host panel port, default `48741` |
| `HOPDNS_DNS_PORT` | Host DNS port, default `53` |
| `HOPDNS_HTTP_PORT` | Host HTTP port, default `80` |
| `HOPDNS_HTTPS_PORT` | Host HTTPS port, default `443` |
| `HOPDNS_IMAGE` | Published image tag/digest, when available; build override uses `hopdns:local` |

Nonstandard traffic ports are useful for local development and conflicts, but
changing them does not tell a console to use those ports. Resolve port conflicts
before deploying for clients. Linux `systemd-resolved` may already own port 53;
adjust the listener/interface deliberately rather than disabling host DNS blindly.
The container needs no privileged mode, host networking, Docker socket, or
`NET_ADMIN`. It runs as UID/GID 10001 with all capabilities dropped; a namespaced
sysctl permits its low ports. Named volumes work without host permission changes.
For custom bind mounts, make the data directory writable by UID 10001.

## Monitoring and themes

The panel offers **System**, **Light**, and **Dark** themes. System follows OS
changes immediately; only the theme preference is stored in browser localStorage.
Routing configuration is stored server-side.

Python is outside the forwarding path. The engine is built from a pinned
upstream source revision with reviewed corrections for domain-boundary matching,
loading routing rules before startup, and propagating TCP half-closes. They add
no per-request monitoring hooks.
One worker samples every five seconds and caches a shared snapshot for all
viewers. Browser polling pauses in hidden tabs; history is bounded to 120 samples
(about ten minutes). No traffic payloads, per-request database writes, or
per-domain request logs are added.

| Metric | What it measures |
| --- | --- |
| Active connections | Established incoming TCP sockets on ports 80/443, sampled from Linux `/proc`; includes connections still negotiating |
| DNS requests / selected queries | Upstream engine counters; reset when the engine restarts |
| HTTPS connections / HTTP requests | Engine connection attempts and request counters, not unique users |
| Relayed requests / sessions | Combined upstream successful HTTPS connections and HTTP requests |
| Domains | Configured domain rules, not unique domains observed in traffic; all mode can cover more |
| Receive / transmit totals and rates | Container `eth0` traffic, including both relay legs, DNS, and panel overhead; not payload-only bandwidth |

Unavailable samples appear as unavailable, rather than invented zeroes. Bandwidth
totals are kernel interface counters and survive engine restarts until the
container/network interface is recreated; rates/history reset on engine restart.
The lightweight collection still consumes some CPU and memory: zero overhead is
not possible. Measure on the target machine before capacity planning. The
256 MiB/128-process Compose limits are conservative starting points, not a
large-deployment sizing guarantee. High connection counts may need larger
limits/file descriptor budgets and load testing.

## Verification and development

Python 3.11+ is sufficient for the application; the container uses Python 3.14.
No third-party Python or JavaScript dependencies are needed at runtime.

```sh
python3 -m unittest discover -s tests -v
docker compose config --quiet
docker compose -f compose.yaml -f compose.build.yaml config --quiet
docker compose -f compose.yaml -f compose.build.yaml build
python3 tests/integration_smoke.py --image hopdns:local
```

The integration harness needs Docker and `openssl`. It creates an isolated
internal Docker network with synthetic DNS and HTTP/TLS peers. It checks selected
and all-domain routing, DNS over UDP/TCP, suffix label boundaries, certificate
verified TLS pass-through, HTTP forwarding under concurrent dashboard polling,
private destination refusal, API boundaries, metric updates, and restart
persistence. It does not contact public game services or prove real-console
compatibility. `--keep` retains the lab for inspection and prints its container identifiers and cleanup
commands. A browser test container can share the application network namespace
using `--network container:<printed-app-name>` and visit `http://127.0.0.1:8787`;
the offline lab publishes no host ports.

With a local Compose instance running, run the browser smoke with synthetic
API responses and capture UI screenshots:

```sh
mkdir -p /tmp/hopdns-screenshots
docker run --rm --network container:$(docker compose -f compose.yaml -f compose.build.yaml ps -q hopdns) \
  --entrypoint node \
  -v "$PWD/tests/ui_smoke.cjs:/test.cjs:ro" \
  -v /tmp/hopdns-screenshots:/artifacts \
  mcr.microsoft.com/playwright/mcp:v0.0.83 /test.cjs
```

For a UI-only preview without Docker (the proxy binary is absent until installed):

```sh
HOPDNS_BIND=127.0.0.1 HOPDNS_PORT=48741 HOPDNS_RUNTIME=/tmp/hopdns-preview python3 -m app.server
```

Do not save routing settings in this preview unless you have installed the pinned
engine. The panel can still display initial setup and unavailable metrics.

## Release preparation

No repository, release, or image is published by running the local commands.
`compose.yaml` is prepared for `ghcr.io/mkay1375/hopdns:latest`; that reference
becomes usable only after publication. Keep the build override until then.
If publishing under another account, update that default and the source label.

CI runs unit, Compose, protocol, and browser checks on pushes and pull requests.
**Publishing requires a manually dispatched workflow with `publish=true`** on
main or a `v*` tag. Pushes, tags, and pull requests alone do not publish. After
verification, the workflow builds AMD64 and ARM64 images and signs in to GHCR
using the repository's automatic `GITHUB_TOKEN`. No registry secrets are needed
in source. Make the package public separately when intending a public release.

After an image has been published, a standalone folder needs only `compose.yaml`:

```sh
docker compose pull
docker compose up -d
```

Use `HOPDNS_IMAGE` in `.env` to pin a real released tag or digest. An update
(`pull`, then `up -d`) can recreate the container and close active connections.
The engine's pinned source revision, small correctness patches, and license notices
are recorded in [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md). Docker builds run
the patches' focused Go regression suites before compiling the executable.

## License

[MIT](LICENSE) for HopDNS. Bundled third-party software retains its own licenses
and notices; see [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md).
