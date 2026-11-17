package acl

import (
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
