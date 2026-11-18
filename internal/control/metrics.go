package control

import (
	"bufio"
	"context"
	"io"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	hostnet "github.com/shirou/gopsutil/v4/net"
)

type relaySample struct {
	at     time.Time
	rx, tx int64
}

var metricsClient = &http.Client{Timeout: time.Second, Transport: &http.Transport{Proxy: nil, DisableKeepAlives: true}}

func parseMetrics(body string) map[string]any {
	out := map[string]any{}
	names := map[string]string{
		"dns_requests_received": "dns_requests", "dns_requests_proxied": "dns_proxied",
		"http_requests_received": "http_requests", "https_requests_received": "https_connections",
		"http_requests_proxied": "http_proxied", "https_requests_proxied": "https_proxied",
		"relay_bytes_to_client": "rx_bytes", "relay_bytes_to_origin": "tx_bytes",
	}
	scanner := bufio.NewScanner(strings.NewReader(body))
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) != 2 {
			continue
		}
		key := strings.SplitN(fields[0], "{", 2)[0]
		key = strings.TrimSuffix(strings.TrimSuffix(key, "_count"), "_total")
		for suffix, target := range names {
			if key != suffix && !strings.HasSuffix(key, "_"+suffix) {
				continue
			}
			if value, err := strconv.ParseInt(fields[1], 10, 64); err == nil && value >= 0 {
				out[target] = value
			} else if value, err := strconv.ParseFloat(fields[1], 64); err == nil && value >= 0 && value < float64(math.MaxInt64) && math.Trunc(value) == value {
				out[target] = int64(value)
			}
			break
		}
	}
	a, aok := out["http_proxied"].(int64)
	b, bok := out["https_proxied"].(int64)
	if aok && bok {
		out["proxied_connections"] = a + b
	}
	return out
}

func scrape(address string) map[string]any {
	response, err := metricsClient.Get("http://" + address + "/metrics")
	if err != nil {
		return nil
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, 262145))
	if err != nil || len(body) > 262144 {
		return nil
	}
	return parseMetrics(string(body))
}

func (c *Controller) updateRelayRates(sample map[string]any, now time.Time) {
	previous := c.lastRelay
	c.lastRelay = nil
	rx, rxOK := sample["rx_bytes"].(int64)
	tx, txOK := sample["tx_bytes"].(int64)
	if !rxOK || !txOK {
		return
	}
	c.lastRelay = &relaySample{at: now, rx: rx, tx: tx}
	if previous != nil && now.After(previous.at) && rx >= previous.rx && tx >= previous.tx {
		seconds := now.Sub(previous.at).Seconds()
		sample["rx_bytes_per_second"] = float64(rx-previous.rx) / seconds
		sample["tx_bytes_per_second"] = float64(tx-previous.tx) / seconds
	}
}

func inboundConnections(ctx context.Context, pid int, o Options) any {
	connections, err := hostnet.ConnectionsPidWithContext(ctx, "tcp", int32(pid))
	if err != nil {
		return nil
	}
	count := 0
	for _, conn := range connections {
		if conn.Status == "ESTABLISHED" && (conn.Laddr.Port == uint32(o.HTTPPort) || conn.Laddr.Port == uint32(o.HTTPSPort)) {
			count++
		}
	}
	return count
}

func (c *Controller) collect() {
	c.mu.Lock()
	generation := c.generation
	pid := 0
	metricsAddress := c.metrics
	needsReadiness := c.state != "running"
	if c.process != nil && c.process.running() {
		pid = c.process.cmd.Process.Pid
	}
	c.mu.Unlock()
	sample := map[string]any{"rx_bytes_per_second": nil, "tx_bytes_per_second": nil}
	metrics := map[string]any{}
	ready := false
	sampledAt := time.Now()
	if pid != 0 {
		metrics = scrape(metricsAddress)
		sampledAt = time.Now()
		for k, v := range metrics {
			sample[k] = v
		}
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		sample["active_connections"] = inboundConnections(ctx, pid, c.options)
		cancel()
		ready = !needsReadiness
		if needsReadiness {
			// Optional OS telemetry may use its entire deadline. Startup readiness
			// gets a fresh bounded context so slow statistics cannot block it.
			probeContext, probeCancel := context.WithTimeout(context.Background(), time.Second)
			ready = listenersReady(probeContext, c.options)
			probeCancel()
		}
	}
	sample["sampled_at"] = sampledAt.UTC().Format(time.RFC3339Nano)
	c.mu.Lock()
	defer c.mu.Unlock()
	if generation != c.generation {
		return
	}
	c.updateRelayRates(sample, sampledAt)
	c.sample = sample
	point := map[string]any{"at": sample["sampled_at"], "rx_bytes_per_second": sample["rx_bytes_per_second"], "tx_bytes_per_second": sample["tx_bytes_per_second"], "active_connections": sample["active_connections"]}
	c.history = append(c.history, point)
	if len(c.history) > 120 {
		c.history = c.history[len(c.history)-120:]
	}
	if pid != 0 && c.process != nil && c.process.running() {
		if len(metrics) > 0 && ready {
			c.state = "running"
			c.lastError = ""
		} else if time.Since(c.started) > 15*time.Second {
			c.failed("Proxy listeners or metrics are unavailable. Check the terminal for port conflicts and permissions.")
		}
	}
}
