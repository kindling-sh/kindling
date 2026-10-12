package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"
)

// ── staging tls --wildcard ──────────────────────────────────────────────
//
// See wildcard-dns.md (local-only spec doc) for the full design and the
// pre-implementation validation log this follows. Summary: HTTP-01 (the
// default path in staging.go) cannot validate a wildcard hostname at all,
// so a shared staging cluster -- where every branch gets its own
// "<slug>.staging.<domain>" Ingress host -- needs a DNS-01 ClusterIssuer,
// a single Certificate requesting both the apex and wildcard SANs, and a
// Traefik TLSStore that makes that certificate the cluster-wide default.
// One Certificate, one Secret, one TLSStore, applied once per cluster --
// no per-branch TLS wiring, no cross-namespace Secret distribution.

const (
	// stagingTLSTraefikNamespace matches ensureIngressController's hardcoded
	// "--namespace traefik --create-namespace" for every Traefik install
	// kindling performs -- not a separate lookup or flag.
	stagingTLSTraefikNamespace = "traefik"

	// stagingTLSCertManagerNamespace is cert-manager's own "cluster resource
	// namespace" (cert-manager by default). A ClusterIssuer is cluster-scoped,
	// but the Secret it references via apiTokenSecretRef must live here --
	// not the Traefik namespace the Certificate/TLSStore live in. Confirmed
	// as a hard requirement during this feature's pre-implementation
	// validation, not a convention kindling invented.
	stagingTLSCertManagerNamespace = "cert-manager"
)

// wildcardDomainNames composes the apex and wildcard DNS names a shared
// staging cluster's certificate needs to cover, using exactly the same
// "staging.<domain>" convention deriveIngressHost enforces for
// branch-derived Ingress hosts (see snapshot.go). These two must agree by
// construction: passing a bare domain here and to --staging-domain
// produces a cert for *.staging.<domain> that actually covers every
// branch's <slug>.staging.<domain> host. A past divergence between these
// two conventions produced a silently unreachable, uncertified host
// (caught during this feature's own pre-implementation validation) --
// hardcoding the "staging." segment in both places, rather than taking it
// as two independently-configurable values, is what prevents that drift
// from recurring.
func wildcardDomainNames(domain string) (apex, wildcard string) {
	domain = strings.TrimPrefix(domain, "staging.")
	apex = "staging." + domain
	wildcard = "*." + apex
	return apex, wildcard
}

// dnsSlug turns a DNS name into a Kubernetes-object-name-safe slug, e.g.
// "staging.subnode1.xyz" -> "staging-subnode1-xyz".
func dnsSlug(s string) string {
	return strings.ReplaceAll(s, ".", "-")
}

// wildcardCertificateName derives the Certificate/Secret base name for a
// given staging domain, e.g. "staging-subnode1-xyz-wildcard".
func wildcardCertificateName(domain string) string {
	apex, _ := wildcardDomainNames(domain)
	return dnsSlug(apex) + "-wildcard"
}

// buildDNS01ClusterIssuerYAML renders a Cloudflare DNS-01 ClusterIssuer.
// Reuses the same issuer/email/ACME-server inputs as the existing HTTP-01
// template -- only the solvers block differs.
func buildDNS01ClusterIssuerYAML(issuer, acmeServer, email, dnsTokenSecret string) string {
	return fmt.Sprintf(`apiVersion: cert-manager.io/v1
kind: ClusterIssuer
metadata:
  name: %s
spec:
  acme:
    server: %s
    email: %s
    privateKeySecretRef:
      name: %s-account-key
    solvers:
    - dns01:
        cloudflare:
          apiTokenSecretRef:
            name: %s
            key: api-token
`, issuer, acmeServer, email, issuer, dnsTokenSecret)
}

// buildWildcardCertificateYAML renders a single Certificate requesting the
// apex and wildcard SANs together, in Traefik's own namespace (see
// buildTLSStoreYAML for why it lives there instead of a per-branch one).
func buildWildcardCertificateYAML(certName, secretName, issuer, apex, wildcard string) string {
	return fmt.Sprintf(`apiVersion: cert-manager.io/v1
kind: Certificate
metadata:
  name: %s
  namespace: %s
spec:
  secretName: %s
  issuerRef:
    name: %s
    kind: ClusterIssuer
  dnsNames:
    - "%s"
    - "%s"
`, certName, stagingTLSTraefikNamespace, secretName, issuer, apex, wildcard)
}

// buildTLSStoreYAML renders Traefik's default TLSStore, pointing at the
// wildcard cert's Secret. This is the piece that actually makes every
// branch's Ingress get HTTPS with zero per-Ingress TLS config of its own:
// Traefik serves this certificate for any TLS handshake that doesn't match
// a more specific one, regardless of which namespace the backing
// Ingress/Service lives in -- a plain Ingress's tls[].secretName can only
// reference a Secret in its own namespace, which doesn't work once every
// branch gets its own namespace.
func buildTLSStoreYAML(secretName string) string {
	return fmt.Sprintf(`apiVersion: traefik.io/v1alpha1
kind: TLSStore
metadata:
  name: default
  namespace: %s
spec:
  defaultCertificate:
    secretName: %s
`, stagingTLSTraefikNamespace, secretName)
}

// runStagingTLSWildcard is the --wildcard branch of runStagingTLS: creates
// a DNS-01 ClusterIssuer, requests a combined apex+wildcard Certificate,
// waits for it to be issued, and sets it as Traefik's default via a
// TLSStore. cert-manager itself is assumed already installed by the
// caller (runStagingTLS installs it unconditionally before branching).
func runStagingTLSWildcard(ctx, acmeServer string) error {
	if stagingTLSDNSProvider != "cloudflare" {
		return fmt.Errorf("--dns-provider %q is not supported yet -- only \"cloudflare\" is implemented", stagingTLSDNSProvider)
	}
	if stagingTLSDNSTokenSecret == "" {
		return fmt.Errorf("--dns-token-secret is required with --wildcard (name of an existing Secret in the %q namespace containing the Cloudflare API token under key \"api-token\")", stagingTLSCertManagerNamespace)
	}

	apex, wildcard := wildcardDomainNames(stagingTLSDomain)
	certName := wildcardCertificateName(stagingTLSDomain)
	secretName := certName + "-tls"

	step("🌐", "Creating DNS-01 ClusterIssuer for wildcard cert")
	issuerYAML := buildDNS01ClusterIssuerYAML(stagingTLSIssuer, acmeServer, stagingTLSEmail, stagingTLSDNSTokenSecret)
	if err := runStdin(issuerYAML, "kubectl", "--context", ctx, "apply", "-f", "-"); err != nil {
		return fmt.Errorf("DNS-01 ClusterIssuer creation failed: %w", err)
	}
	success(fmt.Sprintf("ClusterIssuer %q created (dns01/cloudflare)", stagingTLSIssuer))

	step("🔏", fmt.Sprintf("Requesting certificate for %s and %s", apex, wildcard))
	certYAML := buildWildcardCertificateYAML(certName, secretName, stagingTLSIssuer, apex, wildcard)
	if err := runStdin(certYAML, "kubectl", "--context", ctx, "apply", "-f", "-"); err != nil {
		return fmt.Errorf("Certificate creation failed: %w", err)
	}

	step("⏳", "Waiting for certificate to be issued (DNS-01 validation can take several minutes)")
	if err := waitForCertificateReady(ctx, certName, stagingTLSTraefikNamespace, 10*time.Minute); err != nil {
		return fmt.Errorf("certificate did not become ready: %w -- check 'kubectl --context %s describe certificate %s -n %s' and confirm the DNS token has Zone:DNS:Edit permission", err, ctx, certName, stagingTLSTraefikNamespace)
	}
	success("Wildcard certificate issued and ready")

	step("🔗", "Setting the wildcard certificate as Traefik's default")
	tlsStoreYAML := buildTLSStoreYAML(secretName)
	if err := runStdin(tlsStoreYAML, "kubectl", "--context", ctx, "apply", "-f", "-"); err != nil {
		return fmt.Errorf("TLSStore creation failed: %w", err)
	}
	success("Traefik now serves this certificate for any host under the wildcard")

	fmt.Println()
	fmt.Fprintf(os.Stderr, "  %s🔒 Wildcard TLS is configured!%s\n", colorGreen+colorBold, colorReset)
	fmt.Println()
	fmt.Fprintf(os.Stderr, "  Every branch's Ingress host under %s now gets HTTPS automatically --\n", wildcard)
	fmt.Fprintf(os.Stderr, "  no per-branch TLS configuration needed. Deploy with:\n")
	fmt.Println()
	fmt.Fprintf(os.Stderr, "    kindling snapshot -r <registry> --deploy --context %s --staging-domain %s\n", ctx, strings.TrimPrefix(apex, "staging."))
	fmt.Println()

	return nil
}

// certificateReadyPollInterval is a var (not a const) so tests can shrink
// it instead of waiting out a real multi-second sleep per poll.
var certificateReadyPollInterval = 5 * time.Second

// waitForCertificateReady polls a cert-manager Certificate's Ready
// condition until it's True, or returns an error after timeout. 10 minutes
// is the default used by the caller, not 5 -- cert-manager's own DNS
// propagation re-check loop took 6-8 minutes end to end during this
// feature's pre-implementation validation, and 5 minutes timed out before
// eventually succeeding on real runs.
func waitForCertificateReady(ctx, name, namespace string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	var lastMessage string
	for time.Now().Before(deadline) {
		out, err := runCapture("kubectl", "--context", ctx, "-n", namespace,
			"get", "certificate", name, "-o", "json")
		if err == nil {
			ready, message := certificateReadyCondition(out)
			if ready {
				return nil
			}
			if message != "" {
				lastMessage = message
			}
		}
		time.Sleep(certificateReadyPollInterval)
	}
	if lastMessage != "" {
		return fmt.Errorf("timed out after %s -- last reported condition: %s", timeout, lastMessage)
	}
	return fmt.Errorf("timed out after %s waiting for a Ready condition", timeout)
}

// certificateReadyCondition parses `kubectl get certificate -o json`
// output and reports whether its Ready condition is currently True, plus
// that condition's reason/message (regardless of status) so callers can
// surface *why* it isn't ready yet instead of a generic timeout.
func certificateReadyCondition(jsonOut string) (ready bool, message string) {
	var cert struct {
		Status struct {
			Conditions []struct {
				Type    string `json:"type"`
				Status  string `json:"status"`
				Reason  string `json:"reason"`
				Message string `json:"message"`
			} `json:"conditions"`
		} `json:"status"`
	}
	if err := json.Unmarshal([]byte(jsonOut), &cert); err != nil {
		return false, ""
	}
	for _, c := range cert.Status.Conditions {
		if c.Type != "Ready" {
			continue
		}
		msg := c.Reason
		if c.Message != "" {
			if msg != "" {
				msg += ": "
			}
			msg += c.Message
		}
		return c.Status == "True", msg
	}
	return false, ""
}
