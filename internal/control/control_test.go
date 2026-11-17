package control

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func testOptions(t *testing.T) Options {
	t.Helper()
	return Options{DataDir: t.TempDir(), TrafficBind: "127.0.0.1", DNSPort: 1053, HTTPPort: 1080, HTTPSPort: 1443, Executable: filepath.Join(t.TempDir(), "missing-engine"), Log: io.Discard}
}
func validConfig() Config { c := DefaultConfig(); c.PublicIPv4 = "192.0.2.10"; return c }

func TestConfigValidation(t *testing.T) {
	tests := []struct {
		name string
		edit func(*Config)
	}{
		{"invalid IPv4", func(c *Config) { c.PublicIPv4 = "0.0.0.0" }},
		{"IPv6 advertised", func(c *Config) { c.PublicIPv4 = "::1" }},
		{"loopback upstream", func(c *Config) { c.UpstreamDNS = "udp://127.0.0.1:53" }},
		{"unspecified mapped upstream", func(c *Config) { c.UpstreamDNS = "udp://[::ffff:0.0.0.0]:53" }},
		{"multicast mapped upstream", func(c *Config) { c.UpstreamDNS = "tcp://[::ffff:224.0.0.1]:53" }},
		{"mapped self", func(c *Config) { c.UpstreamDNS = "tcp://[::ffff:192.0.2.10]:53" }},
		{"zero DNS port", func(c *Config) { c.UpstreamDNS = "udp://1.1.1.1:0" }},
		{"DNS hostname", func(c *Config) { c.UpstreamDNS = "udp://dns.example:53" }},
		{"upstream injection", func(c *Config) { c.UpstreamDNS = "udp://1.1.1.1:53\nlog_level:debug" }},
		{"upstream credentials", func(c *Config) { c.UpstreamDNS = "udp://user@1.1.1.1:53" }},
		{"bad mode", func(c *Config) { c.Mode = "vpn" }},
		{"null domains", func(c *Config) { c.Domains = nil }},
		{"wildcard", func(c *Config) { c.Domains = []string{"*.example.com"} }},
		{"IP domain", func(c *Config) { c.Domains = []string{"1.2.3.4"} }},
		{"large domain array", func(c *Config) { c.Domains = make([]string, 1001) }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			c := validConfig()
			test.edit(&c)
			if _, err := Validate(c, false); err == nil {
				t.Fatal("accepted invalid configuration")
			}
		})
	}
	c := validConfig()
	c.Domains = []string{"EXAMPLE.COM.", "example.com"}
	normalized, err := Validate(c, false)
	if err != nil || len(normalized.Domains) != 1 || normalized.Domains[0] != "example.com" {
		t.Fatalf("normalize: %#v %v", normalized, err)
	}
	if string(domainCSV(normalized.Domains)) != "example.com.,fqdn\n.example.com.,suffix\n" {
		t.Fatal("domain rules lost label boundary")
	}
	for _, uri := range []string{"tcp://1.1.1.1:53", "udp://192.168.1.1:53", "tcp://[2606:4700:4700::1111]:53"} {
		c.UpstreamDNS = uri
		if _, err := Validate(c, false); err != nil {
			t.Fatalf("rejected resolver %s: %v", uri, err)
		}
	}
}

func TestAtomicPersistenceAndInvalidApply(t *testing.T) {
	o := testOptions(t)
	c, err := New(o)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	cfg := validConfig()
	if changed, err := c.Apply(cfg); err != nil || !changed {
		t.Fatalf("apply: %v %v", changed, err)
	}
	data, err := os.ReadFile(filepath.Join(o.DataDir, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	var saved Config
	if err = json.Unmarshal(data, &saved); err != nil || saved.PublicIPv4 != cfg.PublicIPv4 {
		t.Fatal("saved config differs")
	}
	if changed, err := c.Apply(cfg); err != nil || changed {
		t.Fatal("unchanged settings caused restart")
	}
	cfg.Mode = "invalid"
	if _, err = c.Apply(cfg); err == nil {
		t.Fatal("invalid settings saved")
	}
	after, _ := os.ReadFile(filepath.Join(o.DataDir, "config.json"))
	if !bytes.Equal(data, after) {
		t.Fatal("invalid change modified saved configuration")
	}
	copy := c.Config()
	copy.Domains[0] = "malicious.example"
	if c.Config().Domains[0] == copy.Domains[0] {
		t.Fatal("configuration snapshot aliases live data")
	}
	reloaded, err := New(o)
	if err != nil {
		t.Fatal(err)
	}
	defer reloaded.Close()
	if reloaded.Config().PublicIPv4 != saved.PublicIPv4 {
		t.Fatal("saved configuration did not reload")
	}
}

func TestHTTPBoundariesAndAssets(t *testing.T) {
	o := testOptions(t)
	c, err := New(o)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	handler := c.Handler()
	request := func(method, path, body string, headers map[string]string) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Host = "127.0.0.1:48741"
		for k, v := range headers {
			if k == "Host" {
				r.Host = v
			} else {
				r.Header.Set(k, v)
			}
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	for _, headers := range []map[string]string{{"Host": "evil.example"}, {"Host": "localhost@evil.example"}, {"Origin": "https://evil.example"}, {"Origin": "http://127.0.0.1:48741/extra"}, {"Sec-Fetch-Site": "cross-site"}} {
		if w := request("GET", "/api/config", "", headers); w.Code != 403 {
			t.Fatalf("untrusted request accepted: %v code=%d", headers, w.Code)
		}
	}
	if w := request("GET", "/", "", nil); w.Code != 200 || !strings.Contains(w.Body.String(), "HopDNS") || w.Header().Get("Content-Security-Policy") == "" {
		t.Fatal("embedded panel missing or unprotected")
	}
	for _, asset := range []string{"/app.js", "/theme.js", "/style.css"} {
		if request("GET", asset, "", nil).Code != 200 {
			t.Fatalf("asset unavailable: %s", asset)
		}
	}
	if request("GET", "/../../go.mod", "", nil).Code != 404 {
		t.Fatal("path traversal exposed a file")
	}
	if request("GET", "/healthz", "", nil).Code != 200 {
		t.Fatal("setup panel not healthy")
	}
	body, _ := json.Marshal(validConfig())
	headers := map[string]string{"X-HopDNS-Request": "1", "Content-Type": "application/json", "Origin": "http://127.0.0.1:48741"}
	if request("PUT", "/api/config", string(body), nil).Code != 403 {
		t.Fatal("write without custom header accepted")
	}
	for _, bad := range []string{"null", "{}", `{"public_ipv4":null,"upstream_dns":"tcp://1.1.1.1:53","mode":"all","domains":[]}`, string(body) + "{}", `{"public_ipv4":23,"upstream_dns":"tcp://1.1.1.1:53","mode":"all","domains":[]}`} {
		if w := request("PUT", "/api/config", bad, headers); w.Code != 400 {
			t.Fatalf("bad JSON code=%d body=%s", w.Code, w.Body.String())
		}
	}
	if request("PUT", "/api/config", strings.Repeat("x", 65537), headers).Code != 413 {
		t.Fatal("oversized body accepted")
	}
	if w := request("PUT", "/api/config", string(body), headers); w.Code != 200 {
		t.Fatalf("valid save: %d %s", w.Code, w.Body.String())
	}
}

func TestMetricsAndHistory(t *testing.T) {
	metrics := parseMetrics("sniproxy_1_dns_requests_received 12\nsniproxy_1_http_requests_proxied 2\nsniproxy_1_https_requests_proxied 4\nsniproxy_1_https_requests_received NaN\n")
	if metrics["dns_requests"] != int64(12) || metrics["proxied_connections"] != int64(6) || metrics["https_connections"] != nil {
		t.Fatalf("bad metrics: %#v", metrics)
	}
	o := testOptions(t)
	c := &Controller{options: o, config: DefaultConfig(), state: "setup_required", sample: map[string]any{}, history: []map[string]any{}}
	for i := 0; i < 125; i++ {
		c.collect()
	}
	if len(c.Status()["history"].([]map[string]any)) != 120 {
		t.Fatal("history grew beyond bound")
	}
}

func TestReadinessRequiresForwardingListeners(t *testing.T) {
	o := testOptions(t)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if listenersReady(ctx, o) {
		t.Fatal("reported readiness with no forwarding listeners")
	}
}

func TestRunningSamplerDoesNotProbeHTTPS(t *testing.T) {
	listeners := make([]net.Listener, 3)
	ports := make([]int, 3)
	for i := range listeners {
		var err error
		listeners[i], err = net.Listen("tcp4", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		defer listeners[i].Close()
		ports[i] = listeners[i].Addr().(*net.TCPAddr).Port
	}
	metrics := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "sniproxy_1_dns_requests_received 1\n")
	}))
	defer metrics.Close()
	o := testOptions(t)
	o.DNSPort = ports[0]
	o.HTTPPort = ports[1]
	o.HTTPSPort = ports[2]
	process, err := os.FindProcess(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	c := &Controller{options: o, config: DefaultConfig(), state: "running", process: &child{cmd: &exec.Cmd{Process: process}, done: make(chan struct{})}, metrics: strings.TrimPrefix(metrics.URL, "http://"), started: time.Now(), sample: map[string]any{}, history: []map[string]any{}}
	c.collect()
	if c.Status()["state"] != "running" {
		t.Fatal("ordinary metrics sample changed readiness")
	}
	_ = listeners[2].(*net.TCPListener).SetDeadline(time.Now().Add(50 * time.Millisecond))
	if conn, err := listeners[2].Accept(); err == nil {
		conn.Close()
		t.Fatal("ordinary metrics sample connected to HTTPS and inflated its counter")
	}
}
