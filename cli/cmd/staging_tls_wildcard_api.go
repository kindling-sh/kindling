package cmd

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os/exec"
	"strings"
	"time"
)

// ── Dashboard wizard: wildcard DNS + TLS (C3) ───────────────────────────
//
// Implements wildcard-dns.md's "Dashboard: the guided wizard itself"
// section -- a four-screen guided flow (domain/DNS-provider check, API
// token validation, DNS record confirmation, certificate issuance) that
// drives the exact same DNS-01/Cloudflare/wildcard-Certificate/TLSStore
// mechanism `kindling staging tls --wildcard` (staging_wildcard.go, B7)
// already implements for the CLI. Reuses that file's pure template
// builders (buildDNS01ClusterIssuerYAML/buildWildcardCertificateYAML/
// buildTLSStoreYAML/wildcardDomainNames/wildcardCertificateName/
// waitForCertificateReady -- see individual handlers for the wiring)
// directly; cert-manager install + SSE progress streaming mirror
// handleStagingTLSInstall's existing (HTTP-01) shape exactly, so there's
// one established pattern for "install TLS with progress" in this file,
// not two.

// ── GET /api/staging/tls/wildcard/check-ns — nameserver check ──────────
//
// Step 1 of the wizard: before asking for anything else, confirm the
// domain's nameservers are actually Cloudflare's -- the one DNS-01
// provider this feature supports (wildcard-dns.md's own Non-goals). A
// domain still on its registrar's default DNS (no wildcard support) is
// the one prerequisite this wizard cannot fix for the user, so it's
// surfaced first, before any token/record work that would be wasted
// otherwise.
func handleStagingTLSWildcardCheckNS(w http.ResponseWriter, r *http.Request) {
	domain := strings.TrimSpace(r.URL.Query().Get("domain"))
	if domain == "" {
		jsonError(w, "domain is required", 400)
		return
	}
	domain = cloudflareZoneName(domain)

	nsRecords, err := net.LookupNS(domain)
	if err != nil {
		jsonResponse(w, map[string]interface{}{
			"ok":    false,
			"error": fmt.Sprintf("could not look up nameservers for %s: %s", domain, err.Error()),
		})
		return
	}

	var hosts []string
	for _, ns := range nsRecords {
		hosts = append(hosts, strings.TrimSuffix(ns.Host, "."))
	}
	jsonResponse(w, map[string]interface{}{
		"ok":          true,
		"nameservers": hosts,
		"cloudflare":  nameserversAreCloudflare(hosts),
	})
}

// cloudflareZoneName derives the bare registrable domain (the name a
// Cloudflare zone is actually registered under) from whatever the wizard's
// "Domain" input happens to be -- de-duplicating an accidental "staging."
// prefix, same convention wildcardDomainNames already applies.
func cloudflareZoneName(domain string) string {
	return strings.TrimPrefix(domain, "staging.")
}

// nameserversAreCloudflare reports whether any of the given nameserver
// hostnames belong to Cloudflare -- the one signal this feature can use
// to confirm a domain's DNS actually supports wildcard records, since
// Cloudflare is the only DNS-01 provider in scope (wildcard-dns.md's own
// Non-goals).
func nameserversAreCloudflare(hosts []string) bool {
	for _, h := range hosts {
		if strings.HasSuffix(h, "cloudflare.com") {
			return true
		}
	}
	return false
}

// ── POST /api/staging/tls/wildcard/validate-token — Cloudflare token check ──
//
// Step 2 of the wizard: validates the API token against Cloudflare's own
// API with a cheap, read-only call (GET /zones?name=<apex>) before the
// user ever reaches the multi-minute DNS-01 wait -- a bad token is
// caught in seconds here, not as a generic ACME failure later.
func handleStagingTLSWildcardValidateToken(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, "method not allowed", 405)
		return
	}
	var body struct {
		Domain string `json:"domain"`
		Token  string `json:"token"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Domain == "" || body.Token == "" {
		jsonError(w, "domain and token are required", 400)
		return
	}

	apex := cloudflareZoneName(body.Domain)

	req, err := http.NewRequest(http.MethodGet, "https://api.cloudflare.com/client/v4/zones?name="+url.QueryEscape(apex), nil)
	if err != nil {
		jsonError(w, "failed to build Cloudflare API request: "+err.Error(), 500)
		return
	}
	req.Header.Set("Authorization", "Bearer "+body.Token)
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		jsonResponse(w, map[string]interface{}{"ok": false, "error": "could not reach Cloudflare API: " + err.Error()})
		return
	}
	defer resp.Body.Close()

	var result struct {
		Success bool `json:"success"`
		Errors  []struct {
			Message string `json:"message"`
		} `json:"errors"`
		Result []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"result"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		jsonResponse(w, map[string]interface{}{"ok": false, "error": "could not parse Cloudflare API response: " + err.Error()})
		return
	}
	if !result.Success {
		msg := "token validation failed"
		if len(result.Errors) > 0 {
			msg = result.Errors[0].Message
		}
		jsonResponse(w, map[string]interface{}{"ok": false, "error": msg})
		return
	}
	if len(result.Result) == 0 {
		jsonResponse(w, map[string]interface{}{
			"ok":    false,
			"error": fmt.Sprintf("token is valid but zone %q was not found or not accessible with this token -- confirm it's scoped to this zone with Zone:DNS:Edit permission", apex),
		})
		return
	}
	jsonResponse(w, map[string]interface{}{
		"ok":        true,
		"zone_id":   result.Result[0].ID,
		"zone_name": result.Result[0].Name,
	})
}

// ── GET /api/staging/tls/wildcard/check-record — live DNS confirmation ──
//
// Step 3 of the wizard: confirms the wildcard record has actually been
// added and propagated, by resolving a synthetic probe hostname under
// the wildcard (a bare "*" isn't directly queryable) and comparing it
// against the ingress controller's known address -- the frontend already
// has that address (fetchStagingIngressController, used elsewhere on
// this same page), so it's passed in rather than re-detected here.
func handleStagingTLSWildcardCheckRecord(w http.ResponseWriter, r *http.Request) {
	domain := strings.TrimSpace(r.URL.Query().Get("domain"))
	expected := strings.TrimSpace(r.URL.Query().Get("expected"))
	if domain == "" || expected == "" {
		jsonError(w, "domain and expected are required", 400)
		return
	}

	apex, wildcard := wildcardDomainNames(domain)
	probe := "kindling-wildcard-check." + apex

	expectedIPs, err := net.LookupHost(expected)
	if err != nil {
		jsonResponse(w, map[string]interface{}{
			"ok":    false,
			"probe": probe,
			"error": fmt.Sprintf("could not resolve expected address %q: %s", expected, err.Error()),
		})
		return
	}

	probeIPs, err := net.LookupHost(probe)
	if err != nil {
		jsonResponse(w, map[string]interface{}{
			"ok":       false,
			"probe":    probe,
			"wildcard": wildcard,
			"error":    fmt.Sprintf("no DNS record found yet for %s (%s) -- DNS propagation can take a few minutes", probe, err.Error()),
		})
		return
	}

	matched := false
	for _, p := range probeIPs {
		for _, e := range expectedIPs {
			if p == e {
				matched = true
			}
		}
	}
	jsonResponse(w, map[string]interface{}{
		"ok":       matched,
		"probe":    probe,
		"wildcard": wildcard,
		"resolved": probeIPs,
		"expected": expectedIPs,
	})
}

// Step 4 of the wizard. Mirrors handleStagingTLSInstall's cert-manager
// install + SSE progress shape exactly, then reuses staging_wildcard.go's
// pure YAML builders (shared with the CLI's --wildcard path) for the
// DNS-01 ClusterIssuer, Certificate, and TLSStore -- one implementation
// of what those resources look like, not a second copy.
func handleStagingTLSWildcardInstall(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, "method not allowed", 405)
		return
	}
	ctx := getStagingContext()
	if ctx == "" {
		jsonError(w, "no staging context configured", 400)
		return
	}

	var body struct {
		Domain         string `json:"domain"`
		Email          string `json:"email"`
		DNSProvider    string `json:"dns_provider"`
		DNSAPIToken    string `json:"dns_api_token"`
		UseACMEStaging bool   `json:"use_acme_staging"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		jsonError(w, "invalid request body", 400)
		return
	}
	if body.Domain == "" || body.Email == "" || body.DNSAPIToken == "" {
		jsonError(w, "domain, email, and dns_api_token are required", 400)
		return
	}
	if strings.ContainsAny(body.Domain, "\n\r\t ") || strings.ContainsAny(body.Email, "\n\r\t") {
		jsonError(w, "invalid characters in domain or email", 400)
		return
	}
	if body.DNSProvider == "" {
		body.DNSProvider = "cloudflare"
	}
	if body.DNSProvider != "cloudflare" {
		jsonError(w, fmt.Sprintf("dns_provider %q is not supported yet -- only \"cloudflare\" is implemented", body.DNSProvider), 400)
		return
	}

	issuer := "letsencrypt-prod-dns"
	if body.UseACMEStaging {
		issuer = "letsencrypt-staging-dns"
	}
	// Tied to the domain, not the issuer, so re-running the wizard for
	// the same domain (e.g. to rotate a token, or to later cut over from
	// --staging to production) idempotently reuses the same Secret
	// instead of accumulating stale ones; a prod/staging ACME switch
	// still gets its own distinct ClusterIssuer per B7's own validation
	// log recommendation, but both can reference this one Secret.
	secretName := wildcardCertificateName(body.Domain) + "-dns-token"

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	flusher, ok := w.(http.Flusher)
	if !ok {
		jsonError(w, "streaming not supported", 500)
		return
	}
	send := func(msgType, msg string) {
		fmt.Fprintf(w, "data: %s\n\n", mustJSON(map[string]string{"type": msgType, "message": msg}))
		flusher.Flush()
	}

	// ── cert-manager (shared with the HTTP-01 install path) ──────────
	_, err := runSilent("kubectl", "--context", ctx, "get", "namespace", "cert-manager")
	if err != nil {
		send("step", "Installing cert-manager v1.17.1")
		certManagerURL := "https://github.com/cert-manager/cert-manager/releases/download/v1.17.1/cert-manager.yaml"
		if _, err := runSilent("kubectl", "--context", ctx, "apply", "-f", certManagerURL); err != nil {
			send("error", "cert-manager installation failed: "+err.Error())
			return
		}
		send("step", "Waiting for cert-manager webhook")
		webhookReady := false
		for i := 0; i < 30; i++ {
			_, err := runSilent("kubectl", "--context", ctx, "-n", "cert-manager",
				"rollout", "status", "deployment/cert-manager-webhook", "--timeout=5s")
			if err == nil {
				webhookReady = true
				break
			}
			time.Sleep(2 * time.Second)
		}
		if !webhookReady {
			send("error", "cert-manager webhook did not become ready — check pod status in cert-manager namespace")
			return
		}
		time.Sleep(5 * time.Second)
		send("step", "cert-manager installed")
	} else {
		send("step", "cert-manager already installed")
	}

	// ── Ingress controller (Traefik) ──────────────────────────────────
	// The TLSStore applied at the end of this flow must land in the
	// "traefik" namespace -- which doesn't exist at all if TLS is being
	// configured before any staging deploy has ever run (the only other
	// place that currently calls ensureIngressController). Without this,
	// step 3's "no ingress controller detected" dead-ends into step 4
	// silently failing to apply the TLSStore against a namespace that was
	// never created.
	if err := ensureIngressController(ctx, func(msg string) { send("step", msg) }); err != nil {
		send("error", "ingress controller setup failed: "+err.Error())
		return
	}

	// ── DNS provider token Secret (cert-manager namespace -- see ──────
	// staging_wildcard.go's own comment on stagingTLSCertManagerNamespace
	// for why it can't live anywhere else: a ClusterIssuer's
	// apiTokenSecretRef resolves against cert-manager's own configured
	// "cluster resource namespace", not an arbitrary one).
	send("step", "Storing DNS provider API token as a Secret")
	secretYAML := fmt.Sprintf(`apiVersion: v1
kind: Secret
metadata:
  name: %s
  namespace: %s
type: Opaque
stringData:
  api-token: "%s"
`, secretName, stagingTLSCertManagerNamespace, yamlEscape(body.DNSAPIToken))
	if out, err := applyYAMLToContext(ctx, secretYAML); err != nil {
		send("error", "failed to store DNS token secret: "+out)
		return
	}

	acmeServer := "https://acme-v02.api.letsencrypt.org/directory"
	if body.UseACMEStaging {
		acmeServer = "https://acme-staging-v02.api.letsencrypt.org/directory"
		send("step", "Using Let's Encrypt staging server")
	}

	send("step", fmt.Sprintf("Creating DNS-01 ClusterIssuer %q", issuer))
	issuerYAML := buildDNS01ClusterIssuerYAML(issuer, acmeServer, body.Email, secretName)
	if out, err := applyYAMLToContext(ctx, issuerYAML); err != nil {
		send("error", "DNS-01 ClusterIssuer creation failed: "+out)
		return
	}

	apex, wildcard := wildcardDomainNames(body.Domain)
	certName := wildcardCertificateName(body.Domain)
	certSecretName := certName + "-tls"

	send("step", fmt.Sprintf("Requesting certificate for %s and %s", apex, wildcard))
	certYAML := buildWildcardCertificateYAML(certName, certSecretName, issuer, apex, wildcard)
	if out, err := applyYAMLToContext(ctx, certYAML); err != nil {
		send("error", "Certificate creation failed: "+out)
		return
	}

	send("step", "Waiting for certificate to be issued (DNS-01 validation can take several minutes)")
	if err := waitForCertificateReady(ctx, certName, stagingTLSTraefikNamespace, 10*time.Minute); err != nil {
		send("error", fmt.Sprintf("certificate did not become ready: %s -- check 'kubectl --context %s describe certificate %s -n %s' and confirm the DNS token has Zone:DNS:Edit permission", err.Error(), ctx, certName, stagingTLSTraefikNamespace))
		return
	}
	send("step", "Certificate issued and ready")

	send("step", "Setting the wildcard certificate as Traefik's default")
	tlsStoreYAML := buildTLSStoreYAML(certSecretName)
	if out, err := applyYAMLToContext(ctx, tlsStoreYAML); err != nil {
		send("error", "TLSStore creation failed: "+out)
		return
	}

	send("done", fmt.Sprintf("Wildcard TLS configured — every branch under %s now gets HTTPS automatically, no per-branch setup needed", wildcard))
}

// ── POST /api/staging/tls/wildcard/ensure-ingress — install Traefik now ─
//
// Lets the wizard's step 3 (confirm the DNS record) recover from exactly
// the dead end above without the user having to wait until step 4 or
// drop to kubectl: if no ingress controller has ever been installed on
// this staging cluster (e.g. TLS is being configured before any
// `snapshot --deploy` has ever run there), there's no address yet for
// step 3 to confirm a DNS record against at all. This runs the same
// ensureIngressController install path, streamed, so the frontend can
// then re-poll /api/staging/ingress-controller and get a real address.
func handleStagingTLSWildcardEnsureIngress(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, "method not allowed", 405)
		return
	}
	ctx := getStagingContext()
	if ctx == "" {
		jsonError(w, "no staging context configured", 400)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	flusher, ok := w.(http.Flusher)
	if !ok {
		jsonError(w, "streaming not supported", 500)
		return
	}
	send := func(msgType, msg string) {
		fmt.Fprintf(w, "data: %s\n\n", mustJSON(map[string]string{"type": msgType, "message": msg}))
		flusher.Flush()
	}

	if err := ensureIngressController(ctx, func(msg string) { send("step", msg) }); err != nil {
		send("error", "ingress controller setup failed: "+err.Error())
		return
	}
	send("done", "Ingress controller ready")
}

// applyYAMLToContext runs `kubectl --context <ctx> apply -f -` with yaml
// piped to stdin, returning combined output for error messages. Shared by
// every apply step above instead of each repeating the same three lines.
func applyYAMLToContext(ctx, yaml string) (string, error) {
	cmd := exec.Command("kubectl", "--context", ctx, "apply", "-f", "-")
	cmd.Stdin = strings.NewReader(yaml)
	out, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(out)), err
}
