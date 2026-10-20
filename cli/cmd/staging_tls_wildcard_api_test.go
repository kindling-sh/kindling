package cmd

import "testing"

// ════════════════════════════════════════════════════════════════
// cloudflareZoneName
// ════════════════════════════════════════════════════════════════

func TestCloudflareZoneName(t *testing.T) {
	tests := []struct {
		name   string
		domain string
		want   string
	}{
		{"bare apex domain", "subnode1.xyz", "subnode1.xyz"},
		{"already prefixed with staging. -- de-duplicated", "staging.subnode1.xyz", "subnode1.xyz"},
		{"multi-label apex", "example.co.uk", "example.co.uk"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := cloudflareZoneName(tt.domain); got != tt.want {
				t.Errorf("cloudflareZoneName(%q) = %q, want %q", tt.domain, got, tt.want)
			}
		})
	}
}

// ════════════════════════════════════════════════════════════════
// nameserversAreCloudflare
// ════════════════════════════════════════════════════════════════

func TestNameserversAreCloudflare(t *testing.T) {
	tests := []struct {
		name  string
		hosts []string
		want  bool
	}{
		{"cloudflare nameservers", []string{"ana.ns.cloudflare.com", "bob.ns.cloudflare.com"}, true},
		{"registrar default nameservers", []string{"ns1.registrar-dns.com", "ns2.registrar-dns.com"}, false},
		{"no nameservers", nil, false},
		{"mixed -- at least one cloudflare host is enough", []string{"ns1.someotherprovider.net", "lily.ns.cloudflare.com"}, true},
		{"host merely containing but not ending in cloudflare.com is not a match", []string{"cloudflare.com.evil-lookalike.net"}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := nameserversAreCloudflare(tt.hosts); got != tt.want {
				t.Errorf("nameserversAreCloudflare(%v) = %v, want %v", tt.hosts, got, tt.want)
			}
		})
	}
}
