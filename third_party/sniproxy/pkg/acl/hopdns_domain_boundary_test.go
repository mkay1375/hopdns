package acl

import (
	"github.com/knadh/koanf"
	"github.com/rs/zerolog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/golang-collections/collections/tst"
)

// These tests cover the collections/tst terminal-byte mismatch and the shorter
// valid rule that a bad longest candidate can otherwise conceal.
func TestHopDNSDomainBoundaries(t *testing.T) {
	maximum := strings.Repeat("a", 63) + "." + strings.Repeat("b", 63) + "." + strings.Repeat("c", 63) + "." + strings.Repeat("d", 61)
	oversized := strings.Repeat("a", 65535)
	cases := []struct {
		name      string
		fqdn      string
		rules     map[string]uint8
		wantProxy bool
	}{
		{"maximum valid hostname", maximum, map[string]uint8{maximum + ".": matchFQDN}, true},
		{"maximum valid absolute hostname", maximum + ".", map[string]uint8{maximum + ".": matchFQDN}, true},
		{"oversized input", oversized, map[string]uint8{oversized + ".": matchFQDN}, false},
		{"254 byte relative hostname", maximum + "x", map[string]uint8{maximum + "x.": matchFQDN}, false},
		{"exact", "selected.hopdns.test", map[string]uint8{"selected.hopdns.test.": matchFQDN}, true},
		{"case and trailing dot", "SELECTED.HOPDNS.TEST.", map[string]uint8{"selected.hopdns.test.": matchFQDN}, true},
		{"subdomain", "cdn.selected.hopdns.test", map[string]uint8{".selected.hopdns.test.": matchSuffix}, true},
		{"suffix lookalike", "evil-selected.hopdns.test", map[string]uint8{".selected.hopdns.test.": matchSuffix}, false},
		{"suffix shorter name", "selected.hopdns.test", map[string]uint8{".selected.hopdns.test.": matchSuffix}, false},
		{"suffix sibling", "selected.hopdns.tesu", map[string]uint8{".selected.hopdns.test.": matchSuffix}, false},
		{"nested shorter suffix", "evilfoo.example.com", map[string]uint8{".example.com.": matchSuffix, ".foo.example.com.": matchSuffix}, true},
		{"nested false suffix", "evilfoo.example.com", map[string]uint8{".foo.example.com.": matchSuffix, ".bar.example.com.": matchSuffix}, false},
		{"prefix match", "api.example.com", map[string]uint8{"api.": matchPrefix}, true},
		{"prefix lookalike", "api-example.com", map[string]uint8{"api.": matchPrefix}, false},
		{"prefix too short", "api", map[string]uint8{"api.example.": matchPrefix}, false},
		{"nested shorter prefix", "api.foo-example.com", map[string]uint8{"api.": matchPrefix, "api.foo.": matchPrefix}, true},
		{"no matching rule", "other.hopdns.test", map[string]uint8{".selected.hopdns.test.": matchSuffix}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := &domain{routePrefixes: tst.New(), routeSuffixes: tst.New(), routeFQDNs: tc.rules}
			for key, kind := range tc.rules {
				switch kind {
				case matchPrefix:
					d.routePrefixes.Insert(key, key)
				case matchSuffix:
					d.routeSuffixes.Insert(reverse(key), key)
				}
			}
			if gotProxy := !d.inDomainList(tc.fqdn); gotProxy != tc.wantProxy {
				t.Fatalf("proxy(%q) = %v, want %v", tc.fqdn, gotProxy, tc.wantProxy)
			}
		})
	}
}

// Readiness must include the initial ACL, not merely scheduling a loader.
func TestHopDNSDomainStartup(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "domains.csv")
	log := zerolog.Nop()
	configuration := koanf.New(".")
	_ = configuration.Set("acl.domain.path", path)
	_ = configuration.Set("acl.domain.refresh_interval", "1h")
	_ = configuration.Set("acl.domain.priority", 20)
	t.Run("missing list prevents startup", func(t *testing.T) {
		d := &domain{}
		if err := d.ConfigAndStart(&log, configuration); err == nil {
			d.Stop()
			t.Fatal("missing domain file must prevent startup")
		}
	})
	if err := os.WriteFile(path, []byte("selected.hopdns.test.,fqdn\n.selected.hopdns.test.,suffix\n"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Run("selected rules available immediately", func(t *testing.T) {
		d := &domain{}
		if err := d.ConfigAndStart(&log, configuration); err != nil {
			t.Fatal(err)
		}
		defer d.Stop()
		if d.inDomainList("selected.hopdns.test") || d.inDomainList("cdn.selected.hopdns.test") {
			t.Fatal("selected domains must be ready before ConfigAndStart returns")
		}
		if !d.inDomainList("other.hopdns.test") {
			t.Fatal("unlisted domain must remain direct")
		}
	})
}
