package control

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

const UpstreamCommit = "1429d6dbe7bed4767597470223f93a407b0d5dab"
const sampleInterval = 5 * time.Second

type Options struct {
	DataDir                      string
	PublicIPv4                   string
	TrafficBind                  string
	DNSPort, HTTPPort, HTTPSPort int
	Executable                   string
	Log                          io.Writer
}

func (o Options) Validate() error {
	ip := net.ParseIP(o.TrafficBind)
	if ip == nil || ip.To4() == nil || ip.IsMulticast() {
		return errors.New("traffic-bind must be an IPv4 interface address")
	}
	seen := map[int]bool{}
	for _, port := range []int{o.DNSPort, o.HTTPPort, o.HTTPSPort} {
		if port < 1 || port > 65535 || seen[port] {
			return errors.New("DNS, HTTP and HTTPS ports must be distinct values between 1 and 65535")
		}
		seen[port] = true
	}
	if o.DataDir == "" {
		return errors.New("data directory is required")
	}
	return nil
}

type child struct {
	cmd  *exec.Cmd
	done chan struct{}
	err  error
}

func (p *child) running() bool {
	select {
	case <-p.done:
		return false
	default:
		return true
	}
}

type Controller struct {
	mu                   sync.Mutex
	options              Options
	config               Config
	process              *child
	runtimeDir, metrics  string
	state, lastError     string
	started, lastAttempt time.Time
	generation           uint64
	sample               map[string]any
	history              []map[string]any
	lastNetwork          *networkSample
	closeOnce            sync.Once
	stop                 chan struct{}
	stopped              chan struct{}
}

func New(o Options) (*Controller, error) {
	if err := o.Validate(); err != nil {
		return nil, err
	}
	if o.Executable == "" {
		var err error
		o.Executable, err = os.Executable()
		if err != nil {
			return nil, err
		}
	}
	if o.Log == nil {
		o.Log = os.Stderr
	}
	c := &Controller{options: o, config: DefaultConfig(), state: "setup_required", sample: map[string]any{}, history: []map[string]any{}, stop: make(chan struct{}), stopped: make(chan struct{})}
	data, err := os.ReadFile(filepath.Join(o.DataDir, "config.json"))
	if err == nil {
		dec := json.NewDecoder(strings.NewReader(string(data)))
		dec.DisallowUnknownFields()
		if err = dec.Decode(&c.config); err != nil {
			return nil, fmt.Errorf("read saved settings: %w", err)
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	} else {
		c.config.PublicIPv4 = o.PublicIPv4
	}
	c.config, err = Validate(c.config, true)
	if err != nil {
		return nil, fmt.Errorf("saved settings: %w", err)
	}
	// Initialize before exposing the controller to API callers. Otherwise an
	// immediate Save could start a child before the worker starts another one.
	c.startEngine()
	go c.run()
	return c, nil
}

func (c *Controller) Config() Config {
	c.mu.Lock()
	defer c.mu.Unlock()
	copy := c.config
	copy.Domains = append([]string{}, copy.Domains...)
	return copy
}

func (c *Controller) Apply(value Config) (bool, error) {
	config, err := Validate(value, false)
	if err != nil {
		return false, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	select {
	case <-c.stop:
		return false, errors.New("service is stopping")
	default:
	}
	body, _ := json.MarshalIndent(config, "", "  ")
	// Persist even an unchanged env-initialized configuration when Save is pressed.
	if err = atomicWrite(filepath.Join(c.options.DataDir, "config.json"), append(body, '\n')); err != nil {
		return false, fmt.Errorf("save settings: %w", err)
	}
	if reflect.DeepEqual(config, c.config) {
		return false, nil
	}
	c.stopEngine()
	c.config = config
	c.startEngine()
	return true, nil
}

func (c *Controller) startEngine() {
	c.lastAttempt = time.Now()
	c.lastError = ""
	if c.config.PublicIPv4 == "" {
		c.state = "setup_required"
		return
	}
	c.state = "starting"
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		c.failed("Cannot allocate the private metrics listener.")
		return
	}
	c.metrics = listener.Addr().String()
	listener.Close()
	dir, err := os.MkdirTemp("", "hopdns-engine-")
	if err != nil {
		c.failed("Cannot create a private engine directory.")
		return
	}
	c.runtimeDir = dir
	if err = atomicWrite(filepath.Join(dir, "domains.csv"), domainCSV(c.config.Domains)); err == nil {
		err = atomicWrite(filepath.Join(dir, "engine.json"), engineConfig(c.config, c.options, dir, c.metrics))
	}
	if err != nil {
		c.failed("Cannot write engine configuration.")
		os.RemoveAll(dir)
		c.runtimeDir = ""
		return
	}
	cmd := exec.Command(c.options.Executable, "engine", "--config", filepath.Join(dir, "engine.json"))
	cmd.Stdout = c.options.Log
	cmd.Stderr = c.options.Log
	for _, entry := range os.Environ() {
		if !strings.HasPrefix(entry, "SNIPROXY_") && !strings.HasPrefix(entry, "TMPDIR=") && !strings.HasPrefix(entry, "NO_COLOR=") {
			cmd.Env = append(cmd.Env, entry)
		}
	}
	cmd.Env = append(cmd.Env, "NO_COLOR=true", "TMPDIR="+dir)
	if err = cmd.Start(); err != nil {
		c.failed("Proxy engine could not start. Check executable permissions and the log.")
		os.RemoveAll(dir)
		c.runtimeDir = ""
		return
	}
	p := &child{cmd: cmd, done: make(chan struct{})}
	c.process = p
	c.started = time.Now()
	go func() { p.err = cmd.Wait(); close(p.done) }()
}

func (c *Controller) failed(message string) { c.state = "error"; c.lastError = message }

func (c *Controller) stopEngine() {
	c.generation++
	if p := c.process; p != nil {
		if p.running() {
			if runtime.GOOS == "windows" {
				_ = p.cmd.Process.Kill()
			} else {
				_ = p.cmd.Process.Signal(os.Interrupt)
			}
			select {
			case <-p.done:
			case <-time.After(5 * time.Second):
				_ = p.cmd.Process.Kill()
				<-p.done
			}
		}
	}
	c.process = nil
	c.started = time.Time{}
	c.sample = map[string]any{}
	c.history = []map[string]any{}
	c.lastNetwork = nil
	if c.runtimeDir != "" {
		_ = os.RemoveAll(c.runtimeDir)
		c.runtimeDir = ""
	}
}

func (c *Controller) run() {
	defer close(c.stopped)
	ticker := time.NewTicker(sampleInterval)
	defer ticker.Stop()
	for {
		c.collect()
		select {
		case <-c.stop:
			c.mu.Lock()
			c.stopEngine()
			c.state = "stopped"
			c.mu.Unlock()
			return
		case <-ticker.C:
		}
		c.mu.Lock()
		if c.process != nil && !c.process.running() {
			c.stopEngine()
			c.failed("Proxy engine exited. Check the terminal for a port conflict, permissions, or configuration error. Retrying in 15 seconds.")
		}
		if c.process == nil && c.config.PublicIPv4 != "" && time.Since(c.lastAttempt) >= 15*time.Second {
			c.startEngine()
		}
		c.mu.Unlock()
	}
}

func (c *Controller) Close() { c.closeOnce.Do(func() { close(c.stop) }); <-c.stopped }

func (c *Controller) Status() map[string]any {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := map[string]any{"active_connections": nil, "dns_requests": nil, "dns_proxied": nil, "http_requests": nil, "https_connections": nil, "proxied_connections": nil, "rx_bytes": nil, "tx_bytes": nil, "rx_bytes_per_second": nil, "tx_bytes_per_second": nil}
	for k, v := range c.sample {
		out[k] = v
	}
	out["state"] = c.state
	out["engine_running"] = c.process != nil && c.process.running()
	out["error"] = nil
	if c.lastError != "" {
		out["error"] = c.lastError
	}
	out["uptime_seconds"] = 0
	if !c.started.IsZero() {
		out["uptime_seconds"] = int(time.Since(c.started).Seconds())
	}
	out["domain_count"] = len(c.config.Domains)
	out["mode"] = c.config.Mode
	out["history"] = append([]map[string]any{}, c.history...)
	out["sample_interval_seconds"] = int(sampleInterval.Seconds())
	out["bandwidth_scope"] = "Host network (includes other applications and all non-loopback interfaces)"
	out["upstream_commit"] = UpstreamCommit
	return out
}

// listenersReady verifies all forwarding transports, including an actual UDP DNS
// response. Metrics alone can be available before the forwarding listeners start.
func listenersReady(ctx context.Context, o Options) bool {
	host := o.TrafficBind
	if host == "0.0.0.0" {
		host = "127.0.0.1"
	}
	d := net.Dialer{Timeout: 200 * time.Millisecond}
	for _, port := range []int{o.DNSPort, o.HTTPPort, o.HTTPSPort} {
		conn, err := d.DialContext(ctx, "tcp4", net.JoinHostPort(host, strconv.Itoa(port)))
		if err != nil {
			return false
		}
		conn.Close()
	}
	conn, err := d.DialContext(ctx, "udp4", net.JoinHostPort(host, strconv.Itoa(o.DNSPort)))
	if err != nil {
		return false
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(200 * time.Millisecond))
	// DNS STATUS opcode requires no upstream lookup and is answered NOTIMP.
	query := []byte{0x48, 0x44, 0x10, 0, 0, 1, 0, 0, 0, 0, 0, 0, 0, 0, 1, 0, 1}
	if _, err = conn.Write(query); err != nil {
		return false
	}
	response := make([]byte, 512)
	n, err := conn.Read(response)
	return err == nil && n >= 12 && response[0] == query[0] && response[1] == query[1] && response[2]&0x80 != 0
}
