# Embedded sniproxy source

This directory contains the Go source and tests from mosajjal/sniproxy v2.4.2,
revision `1429d6dbe7bed4767597470223f93a407b0d5dab` (BSD-2-Clause).
The original license and Go dependency manifests are retained alongside it.

The reviewed domain boundary/startup, TCP half-close, and relay byte counter
patches in `scripts/` are applied to this copy. Their regression tests are
retained in `pkg/`.
The upstream CLI lives in `enginecli` and exposes `Run(args []string)` so
HopDNS can invoke its own binary as a dedicated engine child process.
Self-signed certificates are generated only when an enabled encrypted DNS
listener or override requires them. Ordinary TLS proxy traffic remains opaque.

This local module keeps the source revision and dependency versions fixed.
When updating upstream, reapply and test all three patches, inspect
platform portability, and refresh the retained dependency license notices.
