package cmd

import (
	"strings"
	"testing"
	"time"
)

// ────────────────────────────────────────────────────────────────────────────
// wildcardDomainNames
// ────────────────────────────────────────────────────────────────────────────

func TestWildcardDomainNames(t *testing.T) {
	tests := []struct {
		name         string
		domain       string
		wantApex     string
		wantWildcard string
	}{
		{
			name:         "bare apex domain",
			domain:       "subnode1.xyz",
			wantApex:     "staging.subnode1.xyz",
			wantWildcard: "*.staging.subnode1.xyz",
		},
		{
			name:         "caller already included staging. prefix -- de-duplicated, not doubled",
			domain:       "staging.subnode1.xyz",
			wantApex:     "staging.subnode1.xyz",
			wantWildcard: "*.staging.subnode1.xyz",
		},
		{
			name:         "multi-label apex",
			domain:       "example.co.uk",
			wantApex:     "staging.example.co.uk",
			wantWildcard: "*.staging.example.co.uk",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			apex, wildcard := wildcardDomainNames(tt.domain)
			if apex != tt.wantApex {
				t.Errorf("apex = %q, want %q", apex, tt.wantApex)
			}
			if wildcard != tt.wantWildcard {
				t.Errorf("wildcard = %q, want %q", wildcard, tt.wantWildcard)
			}
		})
	}
}

// Must agree by construction with deriveIngressHost (snapshot.go): a
// branch-derived Ingress host must always fall under this feature's
// wildcard, for any domain input either function could receive.
func TestWildcardDomainNames_AgreesWithDeriveIngressHost(t *testing.T) {
	domains := []string{"subnode1.xyz", "staging.subnode1.xyz", "example.co.uk"}
	for _, domain := range domains {
		apex, wildcard := wildcardDomainNames(domain)
		host := deriveIngressHost("my-branch", domain)
		if !strings.HasSuffix(host, "."+apex) {
			t.Errorf("deriveIngressHost(%q) = %q does not fall under apex %q", domain, host, apex)
		}
		// Sanity: the wildcard glob textually covers that host's shape.
		wantPrefixless := strings.TrimPrefix(wildcard, "*.")
		if wantPrefixless != apex {
			t.Errorf("wildcard %q is not exactly *.%s", wildcard, apex)
		}
	}
}

func TestWildcardCertificateName(t *testing.T) {
	got := wildcardCertificateName("subnode1.xyz")
	want := "staging-subnode1-xyz-wildcard"
	if got != want {
		t.Errorf("wildcardCertificateName() = %q, want %q", got, want)
	}
}

// ────────────────────────────────────────────────────────────────────────────
// YAML templating
// ────────────────────────────────────────────────────────────────────────────

func TestBuildDNS01ClusterIssuerYAML(t *testing.T) {
	got := buildDNS01ClusterIssuerYAML("letsencrypt-prod-dns", "https://acme-v02.api.letsencrypt.org/directory", "you@example.com", "cloudflare-api-token")

	wantContains := []string{
		"kind: ClusterIssuer",
		"name: letsencrypt-prod-dns",
		"server: https://acme-v02.api.letsencrypt.org/directory",
		"email: you@example.com",
		"name: letsencrypt-prod-dns-account-key",
		"dns01:",
		"cloudflare:",
		"name: cloudflare-api-token",
		"key: api-token",
	}
	for _, s := range wantContains {
		if !strings.Contains(got, s) {
			t.Errorf("ClusterIssuer YAML missing %q:\n%s", s, got)
		}
	}
	if strings.Contains(got, "http01") {
		t.Errorf("DNS-01 ClusterIssuer YAML must not contain an http01 solver:\n%s", got)
	}
}

func TestBuildWildcardCertificateYAML(t *testing.T) {
	got := buildWildcardCertificateYAML(
		"staging-subnode1-xyz-wildcard",
		"staging-subnode1-xyz-wildcard-tls",
		"letsencrypt-prod-dns",
		"staging.subnode1.xyz",
		"*.staging.subnode1.xyz",
	)

	wantContains := []string{
		"kind: Certificate",
		"name: staging-subnode1-xyz-wildcard",
		"namespace: traefik",
		"secretName: staging-subnode1-xyz-wildcard-tls",
		"name: letsencrypt-prod-dns",
		"kind: ClusterIssuer",
		`"staging.subnode1.xyz"`,
		`"*.staging.subnode1.xyz"`,
	}
	for _, s := range wantContains {
		if !strings.Contains(got, s) {
			t.Errorf("Certificate YAML missing %q:\n%s", s, got)
		}
	}
}

func TestBuildTLSStoreYAML(t *testing.T) {
	got := buildTLSStoreYAML("staging-subnode1-xyz-wildcard-tls")

	wantContains := []string{
		"apiVersion: traefik.io/v1alpha1",
		"kind: TLSStore",
		"name: default",
		"namespace: traefik",
		"secretName: staging-subnode1-xyz-wildcard-tls",
	}
	for _, s := range wantContains {
		if !strings.Contains(got, s) {
			t.Errorf("TLSStore YAML missing %q:\n%s", s, got)
		}
	}
}

// ────────────────────────────────────────────────────────────────────────────
// certificateReadyCondition
// ────────────────────────────────────────────────────────────────────────────

func TestCertificateReadyCondition(t *testing.T) {
	tests := []struct {
		name        string
		json        string
		wantReady   bool
		wantMessage string
	}{
		{
			name:        "ready true",
			json:        `{"status":{"conditions":[{"type":"Ready","status":"True","reason":"Ready","message":"Certificate is up to date and has not expired"}]}}`,
			wantReady:   true,
			wantMessage: "Ready: Certificate is up to date and has not expired",
		},
		{
			name:        "pending with reason/message",
			json:        `{"status":{"conditions":[{"type":"Ready","status":"False","reason":"DoesNotExist","message":"Issuing certificate as Secret does not exist"}]}}`,
			wantReady:   false,
			wantMessage: "DoesNotExist: Issuing certificate as Secret does not exist",
		},
		{
			name:        "no conditions yet",
			json:        `{"status":{}}`,
			wantReady:   false,
			wantMessage: "",
		},
		{
			name:        "malformed json",
			json:        `not json`,
			wantReady:   false,
			wantMessage: "",
		},
		{
			name:        "no Ready-typed condition present",
			json:        `{"status":{"conditions":[{"type":"Issuing","status":"True"}]}}`,
			wantReady:   false,
			wantMessage: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ready, message := certificateReadyCondition(tt.json)
			if ready != tt.wantReady {
				t.Errorf("ready = %v, want %v", ready, tt.wantReady)
			}
			if message != tt.wantMessage {
				t.Errorf("message = %q, want %q", message, tt.wantMessage)
			}
		})
	}
}

// ────────────────────────────────────────────────────────────────────────────
// runStagingTLSWildcard validation (the parts that don't shell out)
// ────────────────────────────────────────────────────────────────────────────

func TestRunStagingTLSWildcard_RejectsUnsupportedProvider(t *testing.T) {
	origProvider, origSecret := stagingTLSDNSProvider, stagingTLSDNSTokenSecret
	defer func() { stagingTLSDNSProvider, stagingTLSDNSTokenSecret = origProvider, origSecret }()

	stagingTLSDNSProvider = "route53"
	stagingTLSDNSTokenSecret = "whatever"

	err := runStagingTLSWildcard("some-context", "https://acme-v02.api.letsencrypt.org/directory")
	if err == nil {
		t.Fatal("expected an error for an unsupported --dns-provider, got nil")
	}
	if !strings.Contains(err.Error(), "route53") || !strings.Contains(err.Error(), "cloudflare") {
		t.Errorf("error should name both the rejected and supported provider, got: %v", err)
	}
}

func TestRunStagingTLSWildcard_RequiresDNSTokenSecret(t *testing.T) {
	origProvider, origSecret := stagingTLSDNSProvider, stagingTLSDNSTokenSecret
	defer func() { stagingTLSDNSProvider, stagingTLSDNSTokenSecret = origProvider, origSecret }()

	stagingTLSDNSProvider = "cloudflare"
	stagingTLSDNSTokenSecret = ""

	err := runStagingTLSWildcard("some-context", "https://acme-v02.api.letsencrypt.org/directory")
	if err == nil {
		t.Fatal("expected an error when --dns-token-secret is unset, got nil")
	}
	if !strings.Contains(err.Error(), "--dns-token-secret") {
		t.Errorf("error should name the missing flag, got: %v", err)
	}
}

// waitForCertificateReady's own timeout path, exercised against a context
// that doesn't exist so the underlying kubectl call fails immediately --
// this keeps the test fast while still covering the deadline/error-message
// plumbing without depending on a real cluster.
func TestWaitForCertificateReady_TimesOutWithoutACluster(t *testing.T) {
	origInterval := certificateReadyPollInterval
	certificateReadyPollInterval = 10 * time.Millisecond
	defer func() { certificateReadyPollInterval = origInterval }()

	err := waitForCertificateReady("definitely-not-a-real-context", "some-cert", "traefik", 50*time.Millisecond)
	if err == nil {
		t.Fatal("expected a timeout error, got nil")
	}
	if !strings.Contains(err.Error(), "timed out") {
		t.Errorf("expected a timeout error, got: %v", err)
	}
}
