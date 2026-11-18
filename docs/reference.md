# HopDNS reference

[Back to the README](../README.md)

## Native application

The native version runs on macOS, Windows, and Linux as a single executable,
with the DNS relay and browser panel bundled inside. End users need no Docker,
Python, Go installation, or separate web files. Keep the terminal open while
HopDNS runs. It opens the panel in your default browser and prints its URL.

Download the archive for your operating system and CPU from the **Assets** on
the [latest public release](https://github.com/mkay1375/hopdns/releases/latest). Each archive contains one
application executable and license notices. The release provides SHA-256
checksums alongside the archives.

On macOS or Linux, extract the archive and run from Terminal:

```sh
./hopdns
```

On Windows, extract the ZIP and run `hopdns.exe` or launch it from PowerShell:

```powershell
.\hopdns.exe
```

Open **http://localhost:48741** if the browser does not open. In current native
builds, DNS and proxy listeners bind to `0.0.0.0` by default. When HopDNS finds a
private LAN IPv4 address, it starts the relay and shows the DNS address to use
under **Connect a device** and in the terminal. Automatic detection runs at
startup. If your address changes, restart HopDNS and update the console's DNS.
If the computer uses a VPN or multiple networks, check that the displayed
address belongs to the network your console is on. You can enter the correct
address in **Reachable IPv4 address** and click **Save routing**.

Settings persist in the user configuration directory under `hopdns`. A manually
saved address takes precedence over automatic detection. `--data-dir PATH`
selects a different directory or imports an existing Docker `config.json`.

For v0.1.0, or to bind only to a specific interface and set the initial
advertised address:

```sh
./hopdns --traffic-bind 192.168.1.20 --public-ip 192.168.1.20
```

Use your own LAN address. Keep the DNS and proxy ports reachable only by trusted
clients through the host firewall (UDP/TCP 53 and TCP 80/443). For loopback-only
access, use `--traffic-bind 127.0.0.1`. On Windows, allow HopDNS on your private
network when Windows Firewall asks. The panel stays on loopback.

Ports 53, 80, and 443 can require elevated privileges on macOS and Linux. If the
terminal reports a permission error, launch from an elevated terminal, or grant
only the low-port binding capability on Linux. Free any ports held by another
DNS/proxy application before starting. For example, on macOS or Linux:

```sh
sudo ./hopdns --no-browser
```

Then open the printed panel URL in your normal browser. On a Linux server,
`--no-browser` supports headless operation. A remote panel can be reached using
an SSH local forward to `127.0.0.1:48741`.

For a local preview using unprivileged ports:

```sh
./hopdns --no-browser --panel 127.0.0.1:48742 --traffic-bind 127.0.0.1 --public-ip 127.0.0.1 --dns-port 1053 --http-port 1080 --https-port 1443
```

Custom ports support development and matching services. Consoles still expect
standard DNS and proxy ports. The HTTPS relay connects to the same destination
port as its listening port, so a relay listening on 1443 targets origin port
1443. Running a Docker instance at the same time requires distinct ports.

The native downloads are unsigned. macOS Gatekeeper or Windows SmartScreen may
require the usual approval for a downloaded executable. Launching from a terminal
keeps error messages visible. Press **Ctrl+C** to stop the panel and its engine
child process. The application executes its own file as that child, so both
processes use the same single binary.

Native bandwidth totals cover the host's non-loopback interfaces, including
other applications. VPN interfaces can count the same transfer again. Active
connections are sampled for the engine process and configured relay ports.
Collection happens every five seconds outside forwarding, and unavailable OS
statistics are shown as unavailable.

To build from source with Go 1.26 or later:

```sh
go build -trimpath -o hopdns ./cmd/hopdns
```

On Windows, use `-o hopdns.exe`. Native CI runs Go tests and real executable
DNS/HTTP/TLS checks on AMD64 and ARM64 runners for all three operating systems.
It also checks the browser panel, themes, and mobile layout on Linux. Run the
same executable smoke test locally with Python as a development-only dependency:

```sh
python3 tests/native_smoke.py --binary ./hopdns
```

## Run the published image

Download the Compose file, pull the image from GitHub Container Registry, and
start HopDNS:

```sh
mkdir hopdns
cd hopdns
curl -fsSLO https://raw.githubusercontent.com/mkay1375/hopdns/main/compose.yaml
docker compose pull
docker compose up -d
```

To pull the image directly:

```sh
docker pull ghcr.io/mkay1375/hopdns:latest
```

Open **http://localhost:48741** to configure routing. For console or server
access, see [Laptop, LAN, and server deployment](#laptop-lan-and-server-deployment).

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

## Image releases

CI runs unit, Compose, protocol, and browser checks for code changes on pushes
and pull requests. Documentation-only changes skip native and Docker workflows.
After those checks pass on a push to `main`, GitHub Actions publishes the AMD64
and ARM64 images to GHCR and updates `latest`. Maintainers can also dispatch the
workflow with `publish=true` on `main` to update `latest`, or on a `v*` tag to
publish a versioned image. The publish job signs in with the repository's
`GITHUB_TOKEN`.

Use `HOPDNS_IMAGE` in `.env` to pin a real released tag or digest. An update
(`pull`, then `up -d`) can recreate the container and close active connections.
The engine's pinned source revision, small correctness patches, and license notices
are recorded in [THIRD_PARTY_NOTICES.md](../THIRD_PARTY_NOTICES.md). Docker builds run
the patches' focused Go regression suites before compiling the executable.

## License

[MIT](../LICENSE) for HopDNS. Bundled third-party software retains its own licenses
and notices; see [THIRD_PARTY_NOTICES.md](../THIRD_PARTY_NOTICES.md).
