# syntax=docker/dockerfile:1
# Pinned upstream v2.4.2 with reviewed routing/startup and TCP EOF corrections.
FROM --platform=$BUILDPLATFORM golang:1.26-alpine AS engine
RUN apk add --no-cache git
WORKDIR /src
RUN git init && git remote add origin https://github.com/mosajjal/sniproxy.git \
    && git fetch --depth 1 origin 1429d6dbe7bed4767597470223f93a407b0d5dab \
    && git checkout --detach FETCH_HEAD
COPY scripts/sniproxy-domain-boundary.patch /tmp/domain-boundary.patch
COPY scripts/sniproxy-half-close.patch /tmp/half-close.patch
COPY scripts/domain_boundary_test.go /src/pkg/acl/hopdns_domain_boundary_test.go
COPY scripts/half_close_test.go /src/pkg/hopdns_half_close_test.go
RUN git apply --check /tmp/domain-boundary.patch && git apply /tmp/domain-boundary.patch \
    && git apply --check /tmp/half-close.patch && git apply /tmp/half-close.patch
ENV CGO_ENABLED=0 GOEXPERIMENT=goroutineleakprofile,jsonv2
ARG TARGETOS
ARG TARGETARCH
# Tests execute on the build host, then Go cross-compiles for the target platform.
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    go test ./pkg ./pkg/acl -run TestHopDNS -count=1 \
    && GOOS=$TARGETOS GOARCH=$TARGETARCH go build -buildvcs=false \
       -ldflags "-s -w -X main.version=v2.4.2-hopdns.1 -X main.commit=1429d6dbe7bed4767597470223f93a407b0d5dab" \
       -o /sniproxy ./cmd/sniproxy
FROM python:3.14-slim
LABEL org.opencontainers.image.title="HopDNS" \
      org.opencontainers.image.description="Smart DNS and SNI relay with a sampled monitoring panel" \
      org.opencontainers.image.source="https://github.com/mkay1375/hopdns" \
      org.opencontainers.image.licenses="MIT AND BSD-2-Clause"
ENV PYTHONDONTWRITEBYTECODE=1 PYTHONUNBUFFERED=1 \
    HOPDNS_DATA=/data HOPDNS_RUNTIME=/run/hopdns \
    HOPDNS_ENGINE=/usr/local/bin/sniproxy HOPDNS_BIND=0.0.0.0 HOPDNS_PORT=8787
WORKDIR /app
COPY --from=engine /sniproxy /usr/local/bin/sniproxy
COPY app/ app/
COPY web/ web/
COPY LICENSE THIRD_PARTY_NOTICES.md /usr/share/doc/hopdns/
COPY docs/licenses/ /usr/share/doc/hopdns/licenses/
COPY --from=engine /usr/local/go/LICENSE /usr/share/doc/hopdns/licenses/go-runtime.txt
RUN mkdir -p /data /run/hopdns && chown 10001:10001 /data /run/hopdns
USER 10001:10001
EXPOSE 53/udp 53/tcp 80/tcp 443/tcp 8787/tcp
HEALTHCHECK --interval=15s --timeout=3s --start-period=20s --retries=3 \
  CMD ["python3", "-c", "import urllib.request; urllib.request.urlopen('http://127.0.0.1:8787/healthz',timeout=2).read()"]
CMD ["python3", "-m", "app.server"]
