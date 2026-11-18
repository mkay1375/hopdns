package main

import "testing"

func TestPanelBindAddress(t *testing.T) {
	for _, tc := range []struct {
		address string
		want    string
	}{
		{"localhost:0", "127.0.0.1:0"},
		{"127.0.0.1:48741", "127.0.0.1:48741"},
		{"[::1]:48741", "[::1]:48741"},
		{"127.0.0.2:48741", ""},
		{"[0:0:0:0:0:0:0:1]:48741", ""},
		{"[::ffff:127.0.0.1]:48741", ""},
		{"0.0.0.0:48741", ""},
	} {
		t.Run(tc.address, func(t *testing.T) {
			got, err := panelBindAddress(tc.address)
			if got != tc.want || (err != nil) != (tc.want == "") {
				t.Fatalf("panelBindAddress(%q) = %q, %v; want %q", tc.address, got, err, tc.want)
			}
		})
	}
}
