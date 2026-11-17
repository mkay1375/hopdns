package control

import (
	"bufio"
	"context"
	"io"
	"math"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	hostnet "github.com/shirou/gopsutil/v4/net"
)

type networkSample struct {
	at     time.Time
	rx, tx uint64
}

var metricsClient = &http.Client{Timeout: time.Second, Transport: &http.Transport{Proxy: nil, DisableKeepAlives: true}}

func parseMetrics(body string) map[string]any {
	out := map[string]any{}
	names := map[string]string{"dns_requests_received": "dns_requests", "dns_requests_proxied": "dns_proxied", "http_requests_received": "http_requests", "https_requests_received": "https_connections", "http_requests_proxied": "http_proxied", "https_requests_proxied": "https_proxied"}
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
		for suffix, target := range names {
			if strings.HasSuffix(key, "_"+suffix) || strings.HasSuffix(key, "_"+suffix+"_count") {
				v, err := strconv.ParseFloat(fields[1], 64)
				if err == nil && !math.IsNaN(v) && !math.IsInf(v, 0) && v >= 0 && v <= math.MaxInt64 {
					out[target] = int64(v)
				}
			}
		}
	}
	a, aok := out["http_proxied"].(int64)
	b, bok := out["https_proxied"].(int64)
	if aok && bok {
		out["proxied_connections"] = a + b
	}
	delete(out, "http_proxied")
	delete(out, "https_proxied")
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

func hostBytes(ctx context.Context) *networkSample {
	interfaces, err := net.Interfaces()
	if err != nil {
		return nil
	}
	excluded := map[string]bool{}
	for _, iface := range interfaces {
		if iface.Flags&net.FlagLoopback != 0 {
			excluded[iface.Name] = true
		}
	}
	stats, err := hostnet.IOCountersWithContext(ctx, true)
	if err != nil || len(stats) == 0 {
		return nil
	}
	result := &networkSample{at: time.Now()}
	for _, stat := range stats {
		if !excluded[stat.Name] {
			result.rx += stat.BytesRecv
			result.tx += stat.BytesSent
		}
	}
	return result
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
	sample := map[string]any{"sampled_at": time.Now().UTC().Format(time.RFC3339Nano), "rx_bytes_per_second": nil, "tx_bytes_per_second": nil}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	bytes := hostBytes(ctx)
	if bytes != nil {
		sample["rx_bytes"] = bytes.rx
		sample["tx_bytes"] = bytes.tx
	}
	metrics := map[string]any{}
	ready := false
	if pid != 0 {
		metrics = scrape(metricsAddress)
		for k, v := range metrics {
			sample[k] = v
		}
		sample["active_connections"] = inboundConnections(ctx, pid, c.options)
		ready = !needsReadiness || listenersReady(ctx, c.options)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if generation != c.generation {
		return
	}
	if bytes != nil && c.lastNetwork != nil {
		seconds := bytes.at.Sub(c.lastNetwork.at).Seconds()
		if seconds > 0 && bytes.rx >= c.lastNetwork.rx && bytes.tx >= c.lastNetwork.tx {
			sample["rx_bytes_per_second"] = float64(bytes.rx-c.lastNetwork.rx) / seconds
			sample["tx_bytes_per_second"] = float64(bytes.tx-c.lastNetwork.tx) / seconds
		}
	}
	c.lastNetwork = bytes
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
