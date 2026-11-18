# Third-party software

HopDNS's application and panel are MIT licensed. The container also distributes
the [mosajjal/sniproxy](https://github.com/mosajjal/sniproxy) executable,
based on **v2.4.2**, source revision
`1429d6dbe7bed4767597470223f93a407b0d5dab`, under the BSD 2-Clause license.
Its copyright and license are retained in [docs/licenses/sniproxy.txt](docs/licenses/sniproxy.txt)
and in `/usr/share/doc/hopdns/licenses/` in the image.

HopDNS builds **v2.4.2-hopdns.1** from that exact source revision with the small
reviewed patches in `scripts/`. `sniproxy-domain-boundary.patch` corrects a
dependency's false prefix/suffix candidates, preserves valid shorter rules, and
bounds hostname matching input. It also loads the selected domain rules
synchronously before listeners start, preventing early queries from bypassing
the filter during startup. Without it, a lookalike such as
`evil-selected.example` can incorrectly match `.selected.example`. The Docker
build runs the accompanying focused Go regression suite before compilation.
`sniproxy-half-close.patch` propagates TCP half-closes after copying completes,
so EOF-dependent clients finish and upload FIN does not discard the reverse
response. `sniproxy-relay-bytes.patch` adds cumulative directional counters for
forwarded HTTP bodies and HTTPS tunnel bytes.
The upstream Go module includes dependencies with their own licenses. Their
notices are retained in `docs/licenses/go-dependencies/`; the dependency manifest
is retained as `docs/licenses/sniproxy-go.mod` and `sniproxy-go.sum`.

The Go runtime's BSD license is copied from the build toolchain into
`/usr/share/doc/hopdns/licenses/go-runtime.txt`. Python, Debian, and their packaged
libraries retain their respective licenses.
The Python base image retains operating-system notices under
`/usr/share/doc/*/copyright` and Python's license under its installed Python
library directory. Image builds and publications do not relicense these components.

## Native application

The native executable embeds the same pinned and patched upstream source under
`third_party/sniproxy`, together with HopDNS's Go control API and browser assets.
It invokes its own executable as an engine child process. The source adapter skips
unused encrypted-DNS certificate generation during ordinary proxy startup.

Portable TCP connection sampling uses `github.com/shirou/gopsutil/v4` and its dependencies,
including platform support libraries such as `purego`, `go-ole`, `wmi`, and
`perfstat`. Their license notices are retained with the other Go dependency
notices. The exact native dependency versions are in the root `go.mod` and
`go.sum`. Native archives retain these notices and the Go toolchain's BSD license
alongside the executable. The notices are not runtime requirements.
