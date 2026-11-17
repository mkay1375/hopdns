package control

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// Config is shared with the existing browser panel and Docker deployment.
type Config struct {
	PublicIPv4  string   `json:"public_ipv4"`
	UpstreamDNS string   `json:"upstream_dns"`
	Mode        string   `json:"mode"`
	Domains     []string `json:"domains"`
}

func DefaultConfig() Config {
	return Config{UpstreamDNS: "tcp://1.1.1.1:53", Mode: "selected", Domains: []string{"xbox.com", "xboxlive.com", "playstation.net"}}
}

var labelPattern = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$`)

func Validate(c Config, allowEmptyIP bool) (Config, error) {
	if c.PublicIPv4 != "" || !allowEmptyIP {
		ip, err := netip.ParseAddr(c.PublicIPv4)
		if err != nil || !ip.Is4() || ip.IsUnspecified() || ip.IsMulticast() || ip.String() == "255.255.255.255" {
			return c, errors.New("reachable IPv4 address is required")
		}
	}
	u, err := url.Parse(c.UpstreamDNS)
	if err != nil || len(c.UpstreamDNS) > 256 {
		return c, errors.New("invalid upstream DNS URI")
	}
	addr, err := netip.ParseAddr(u.Hostname())
	port, portErr := strconv.Atoi(u.Port())
	if err != nil || portErr != nil || port < 1 || port > 65535 || (u.Scheme != "tcp" && u.Scheme != "udp") || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" || u.Opaque != "" || addr.Unmap().IsLoopback() || addr.Unmap().IsUnspecified() || addr.Unmap().IsMulticast() || addr.Unmap().String() == "255.255.255.255" || addr.Unmap().String() == c.PublicIPv4 {
		return c, errors.New("upstream must be tcp://IP:port or udp://IP:port and must not point back to HopDNS")
	}
	if c.Mode != "selected" && c.Mode != "all" {
		return c, errors.New("mode must be selected or all")
	}
	if c.Domains == nil || len(c.Domains) > 1000 {
		return c, errors.New("provide an array of at most 1,000 domain rules")
	}
	domains := make([]string, 0, len(c.Domains))
	seen := map[string]bool{}
	for _, domain := range c.Domains {
		domain = strings.ToLower(strings.TrimRight(domain, "."))
		if len(domain) > 253 || !strings.Contains(domain, ".") {
			return c, errors.New("domain rules must be plain hostnames")
		}
		if _, err := netip.ParseAddr(domain); err == nil {
			return c, errors.New("domain rules must be hostnames, not IP addresses")
		}
		for _, label := range strings.Split(domain, ".") {
			if !labelPattern.MatchString(label) {
				return c, errors.New("domain rules must be plain hostnames without wildcards")
			}
		}
		if !seen[domain] {
			domains = append(domains, domain)
			seen[domain] = true
		}
	}
	c.Domains = domains
	return c, nil
}

func atomicWrite(path string, body []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".hopdns-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if err = f.Chmod(0600); err == nil {
		_, err = f.Write(body)
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(f.Name(), path)
}

func engineConfig(c Config, o Options, runtimeDir, metrics string) []byte {
	general := map[string]any{
		"public_ipv4": c.PublicIPv4, "public_ipv6": "", "preferred_version": "ipv4only", "upstream_dns": c.UpstreamDNS,
		"bind_dns_over_udp": net.JoinHostPort(o.TrafficBind, strconv.Itoa(o.DNSPort)), "bind_dns_over_tcp": net.JoinHostPort(o.TrafficBind, strconv.Itoa(o.DNSPort)),
		"bind_http": net.JoinHostPort(o.TrafficBind, strconv.Itoa(o.HTTPPort)), "bind_https": net.JoinHostPort(o.TrafficBind, strconv.Itoa(o.HTTPSPort)),
		"prometheus": metrics, "bind_prometheus": metrics, "bind_pprof": "", "goroutine_leak_interval": "0s", "allow_conn_to_local": false, "log_level": "warn",
	}
	acl := map[string]any{"domain": map[string]any{"enabled": c.Mode == "selected", "priority": 20, "path": filepath.Join(runtimeDir, "domains.csv"), "refresh_interval": "24h"}, "cidr": map[string]bool{"enabled": false}, "geoip": map[string]bool{"enabled": false}, "override": map[string]bool{"enabled": false}}
	body, _ := json.Marshal(map[string]any{"general": general, "acl": acl})
	return body
}

func domainCSV(domains []string) []byte {
	var s strings.Builder
	for _, d := range domains {
		fmt.Fprintf(&s, "%s.,fqdn\n.%s.,suffix\n", d, d)
	}
	return []byte(s.String())
}
