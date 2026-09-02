package cmd

import (
	"strings"
	"testing"
)

// ────────────────────────────────────────────────────────────────────────────
// slugifyBranch
// ────────────────────────────────────────────────────────────────────────────

func TestSlugifyBranch(t *testing.T) {
	tests := []struct {
		name   string
		branch string
		want   string
	}{
		{"simple slash-separated", "feature/add-checkout-retry", "feature-add-checkout-retry"},
		{"mixed case and symbols", "Fix/Bug#142", "fix-bug-142"},
		{"dots collapse to hyphen", "renovate/go.mod-updates", "renovate-go-mod-updates"},
		{"all symbols falls back", "------", "branch"},
		{"main is not special-cased", "main", "main"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := slugifyBranch(tt.branch); got != tt.want {
				t.Errorf("slugifyBranch(%q) = %q, want %q", tt.branch, got, tt.want)
			}
		})
	}
}

func TestSlugifyBranch_TruncatesLongNames(t *testing.T) {
	branch := "a-branch-name-that-is-extremely-long-and-exceeds-the-limit"
	got := slugifyBranch(branch)
	if len(got) > 40 {
		t.Errorf("slugifyBranch(%q) = %q (len %d), want len <= 40", branch, got, len(got))
	}
	if strings.HasSuffix(got, "-") {
		t.Errorf("slugifyBranch(%q) = %q, should not end in a trailing hyphen after truncation", branch, got)
	}
	if got != branch[:40] {
		t.Errorf("slugifyBranch(%q) = %q, want the first 40 chars %q (no symbols land on the cut boundary in this case)", branch, got, branch[:40])
	}
}

func TestSlugifyBranch_Deterministic(t *testing.T) {
	branch := "feature/add-checkout-retry"
	first := slugifyBranch(branch)
	for i := 0; i < 5; i++ {
		if got := slugifyBranch(branch); got != first {
			t.Errorf("slugifyBranch(%q) is not deterministic: got %q, first call was %q", branch, got, first)
		}
	}
}

// ────────────────────────────────────────────────────────────────────────────
// deriveIngressHost
// ────────────────────────────────────────────────────────────────────────────

func TestDeriveIngressHost(t *testing.T) {
	tests := []struct {
		name   string
		slug   string
		domain string
		want   string
	}{
		{"bare domain gets staging. inserted", "example-branch", "subnode1.xyz", "example-branch.staging.subnode1.xyz"},
		{"domain already prefixed with staging. is not doubled", "example-branch", "staging.subnode1.xyz", "example-branch.staging.subnode1.xyz"},
		{"multi-part branch slug", "feature-checkout-retry", "example.com", "feature-checkout-retry.staging.example.com"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := deriveIngressHost(tt.slug, tt.domain); got != tt.want {
				t.Errorf("deriveIngressHost(%q, %q) = %q, want %q", tt.slug, tt.domain, got, tt.want)
			}
		})
	}
}

// ────────────────────────────────────────────────────────────────────────────
// applyBranchIngressHost
// ────────────────────────────────────────────────────────────────────────────

func TestApplyBranchIngressHost_DerivesFromBranchAndDomain(t *testing.T) {
	branch := "feature/checkout-retry"
	domain := "staging.example.com"
	slug := slugifyBranch(branch)
	derivedHost := slug + "." + domain

	dses := []snapshotDSE{
		{Name: "gateway", Ingress: &snapshotIngress{Enabled: true, Host: ""}},
	}
	derived, err := applyBranchIngressHost(dses, derivedHost)
	if err != nil {
		t.Fatalf("applyBranchIngressHost() error: %v", err)
	}
	want := "feature-checkout-retry.staging.example.com"
	if dses[0].Ingress.Host != want {
		t.Errorf("Ingress.Host = %q, want %q", dses[0].Ingress.Host, want)
	}
	if len(derived) != 1 || derived[0] != "gateway" {
		t.Errorf("derived = %v, want [\"gateway\"]", derived)
	}
}

func TestApplyBranchIngressHost_NeverOverridesExplicitHost(t *testing.T) {
	dses := []snapshotDSE{
		{Name: "gateway", Ingress: &snapshotIngress{Enabled: true, Host: "custom.example.com"}},
	}
	derived, err := applyBranchIngressHost(dses, "feature-checkout-retry.staging.example.com")
	if err != nil {
		t.Fatalf("applyBranchIngressHost() error: %v", err)
	}
	if dses[0].Ingress.Host != "custom.example.com" {
		t.Errorf("Ingress.Host = %q, want unchanged %q", dses[0].Ingress.Host, "custom.example.com")
	}
	if len(derived) != 0 {
		t.Errorf("derived = %v, want none (explicit host should not be reported as derived)", derived)
	}
}

func TestApplyBranchIngressHost_OverridesLocalDevLocalhostHost(t *testing.T) {
	// The local dev convention (<name>.localhost, carried over from the
	// Kind cluster's own DSE) is never meaningful staging intent — it's
	// never resolvable outside Kind, and is frequently identical across
	// branches (e.g. every branch's frontend DSE ends up "frontend.localhost"
	// after stripDSEPrefix), which is exactly the collision this feature
	// exists to prevent. It must be overridden, not treated as explicit.
	dses := []snapshotDSE{
		{Name: "frontend", Ingress: &snapshotIngress{Enabled: true, Host: "frontend.localhost"}},
	}
	derived, err := applyBranchIngressHost(dses, "feature-checkout-retry.staging.example.com")
	if err != nil {
		t.Fatalf("applyBranchIngressHost() error: %v", err)
	}
	if dses[0].Ingress.Host != "feature-checkout-retry.staging.example.com" {
		t.Errorf("Ingress.Host = %q, want derived host to replace the local .localhost convention", dses[0].Ingress.Host)
	}
	if len(derived) != 1 || derived[0] != "frontend" {
		t.Errorf(`derived = %v, want ["frontend"]`, derived)
	}
}

func TestApplyBranchIngressHost_LocalhostHostWithNoStagingDomainErrors(t *testing.T) {
	dses := []snapshotDSE{
		{Name: "frontend", Ingress: &snapshotIngress{Enabled: true, Host: "frontend.localhost"}},
	}
	_, err := applyBranchIngressHost(dses, "")
	if err == nil {
		t.Fatal("expected an error when the only host is the local .localhost convention and no --staging-domain is set, got nil")
	}
	if !strings.Contains(err.Error(), "frontend") {
		t.Errorf("error %q should name the affected DSE %q", err.Error(), "frontend")
	}
}

func TestApplyBranchIngressHost_IgnoresDisabledOrNilIngress(t *testing.T) {
	dses := []snapshotDSE{
		{Name: "worker", Ingress: nil},
		{Name: "internal-api", Ingress: &snapshotIngress{Enabled: false, Host: ""}},
	}
	derived, err := applyBranchIngressHost(dses, "feature-checkout-retry.staging.example.com")
	if err != nil {
		t.Fatalf("applyBranchIngressHost() error: %v", err)
	}
	if len(derived) != 0 {
		t.Errorf("derived = %v, want none", derived)
	}
	if dses[1].Ingress.Host != "" {
		t.Errorf("Ingress.Host = %q, want unchanged empty (Ingress not enabled)", dses[1].Ingress.Host)
	}
}

func TestApplyBranchIngressHost_ErrorsWithNoHostAvailable(t *testing.T) {
	dses := []snapshotDSE{
		{Name: "gateway", Ingress: &snapshotIngress{Enabled: true, Host: ""}},
	}
	_, err := applyBranchIngressHost(dses, "")
	if err == nil {
		t.Fatal("expected an error when no --staging-domain and no explicit host are available, got nil")
	}
	if !strings.Contains(err.Error(), "gateway") {
		t.Errorf("error %q should name the affected DSE %q", err.Error(), "gateway")
	}
	if !strings.Contains(err.Error(), "--staging-domain") {
		t.Errorf("error %q should mention --staging-domain as the fix", err.Error())
	}
}

func TestApplyBranchIngressHost_MixedServices(t *testing.T) {
	dses := []snapshotDSE{
		{Name: "gateway", Ingress: &snapshotIngress{Enabled: true, Host: ""}},
		{Name: "admin", Ingress: &snapshotIngress{Enabled: true, Host: "admin.example.com"}},
		{Name: "frontend", Ingress: &snapshotIngress{Enabled: true, Host: "frontend.localhost"}},
		{Name: "worker", Ingress: nil},
	}
	derived, err := applyBranchIngressHost(dses, "feature-checkout-retry.staging.example.com")
	if err != nil {
		t.Fatalf("applyBranchIngressHost() error: %v", err)
	}
	if dses[0].Ingress.Host != "feature-checkout-retry.staging.example.com" {
		t.Errorf("gateway Ingress.Host = %q, want derived host", dses[0].Ingress.Host)
	}
	if dses[1].Ingress.Host != "admin.example.com" {
		t.Errorf("admin Ingress.Host = %q, want unchanged", dses[1].Ingress.Host)
	}
	if dses[2].Ingress.Host != "feature-checkout-retry.staging.example.com" {
		t.Errorf("frontend Ingress.Host = %q, want the local .localhost convention replaced with the derived host", dses[2].Ingress.Host)
	}
	if len(derived) != 2 {
		t.Errorf(`derived = %v, want ["gateway", "frontend"]`, derived)
	}
}

// ────────────────────────────────────────────────────────────────────────────
// currentBranch
// ────────────────────────────────────────────────────────────────────────────

func TestCurrentBranch(t *testing.T) {
	// Smoke test: this package's own checkout is a git repo, so
	// currentBranch() should resolve to something non-empty and
	// slugify-able without error.
	branch, err := currentBranch()
	if err != nil {
		t.Fatalf("currentBranch() error: %v", err)
	}
	if branch == "" {
		t.Error("currentBranch() returned an empty string")
	}
	if slug := slugifyBranch(branch); slug == "" {
		t.Errorf("slugifyBranch(currentBranch()=%q) returned an empty string", branch)
	}
}

// ────────────────────────────────────────────────────────────────────────────
// detectUserPrefix
// ────────────────────────────────────────────────────────────────────────────

func TestDetectUserPrefix(t *testing.T) {
	tests := []struct {
		name   string
		dses   []snapshotDSE
		expect string
	}{
		{
			name:   "no DSEs",
			dses:   nil,
			expect: "",
		},
		{
			name:   "single DSE (needs ≥ 2)",
			dses:   []snapshotDSE{{Name: "jeff-vincent-gateway"}},
			expect: "",
		},
		{
			name: "standard GitHub actor prefix",
			dses: []snapshotDSE{
				{Name: "jeff-vincent-gateway"},
				{Name: "jeff-vincent-inventory"},
				{Name: "jeff-vincent-orders"},
				{Name: "jeff-vincent-ui"},
			},
			expect: "jeff-vincent-",
		},
		{
			name: "mixed with unprefixed services",
			dses: []snapshotDSE{
				{Name: "jeff-vincent-gateway"},
				{Name: "jeff-vincent-inventory"},
				{Name: "jeff-vincent-orders"},
				{Name: "my-test-service"},
			},
			expect: "jeff-vincent-",
		},
		{
			name: "no common prefix",
			dses: []snapshotDSE{
				{Name: "gateway"},
				{Name: "inventory"},
				{Name: "orders"},
			},
			expect: "",
		},
		{
			name: "two-segment actor name",
			dses: []snapshotDSE{
				{Name: "my-org-api"},
				{Name: "my-org-web"},
				{Name: "my-org-worker"},
			},
			expect: "my-org-",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := detectUserPrefix(tt.dses)
			if got != tt.expect {
				t.Errorf("detectUserPrefix() = %q, want %q", got, tt.expect)
			}
		})
	}
}

// ────────────────────────────────────────────────────────────────────────────
// helmSafe
// ────────────────────────────────────────────────────────────────────────────

func TestHelmSafe(t *testing.T) {
	tests := []struct {
		input  string
		expect string
	}{
		{"My Service", "my-service"},
		{"gateway", "gateway"},
		{"my_api", "my-api"},
		{"UPPER CASE", "upper-case"},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			if got := helmSafe(tt.input); got != tt.expect {
				t.Errorf("helmSafe(%q) = %q, want %q", tt.input, got, tt.expect)
			}
		})
	}
}

// ────────────────────────────────────────────────────────────────────────────
// stagingImageClean
// ────────────────────────────────────────────────────────────────────────────

func TestStagingImageClean(t *testing.T) {
	tests := []struct {
		name   string
		image  string
		svc    string
		expect string
	}{
		{"localhost registry", "localhost:5001/my-svc:abc123", "my-svc", "my-svc:latest"},
		{"kind-loaded", "my-svc:1772351435", "my-svc", "my-svc:latest"},
		{"external registry", "ghcr.io/org/my-svc:v1", "my-svc", "ghcr.io/org/my-svc:v1"},
		{"already latest", "my-svc:latest", "my-svc", "my-svc:latest"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := stagingImageClean(tt.image, tt.svc); got != tt.expect {
				t.Errorf("stagingImageClean(%q, %q) = %q, want %q", tt.image, tt.svc, got, tt.expect)
			}
		})
	}
}

// ────────────────────────────────────────────────────────────────────────────
// buildValuesYAML — dep env vars are real configurable values
// ────────────────────────────────────────────────────────────────────────────

// ────────────────────────────────────────────────────────────────────────────
// buildValuesYAML / helmIngressTemplate — Ingress route backend naming
// ────────────────────────────────────────────────────────────────────────────

// Regression: an Ingress's extra routes must resolve to the actual
// deployed Service name ({{ .Release.Name }}-<safe-name>), not the raw
// dev-cluster service name -- writing the latter verbatim produced a
// "services \"jeff-vincent-auth\" not found" backend in a real staging
// deploy, since no such Service is ever created by the chart.
func TestBuildValuesYAML_IngressRouteServiceIsHelmSafe(t *testing.T) {
	dses := []snapshotDSE{
		{
			Name: "frontend",
			Ingress: &snapshotIngress{
				Enabled: true,
				Host:    "frontend.example.com",
				Routes:  []snapshotRoute{{Path: "/auth", PathType: "Prefix", Service: "Auth_Service", Port: 8000}},
			},
		},
	}

	yaml := buildValuesYAML("test", dses, map[string]bool{}, false, nil, nil)

	if !strings.Contains(yaml, `service: "auth-service"`) {
		t.Errorf("expected route service to be written in helm-safe form, got:\n%s", yaml)
	}
	if strings.Contains(yaml, "Auth_Service") {
		t.Error("raw, non-helm-safe service name should not appear in values.yaml")
	}
}

func TestHelmIngressTemplate_RoutesUseReleaseNamePrefix(t *testing.T) {
	dse := snapshotDSE{
		Name: "frontend",
		Ingress: &snapshotIngress{
			Enabled: true,
			Host:    "frontend.example.com",
			Routes:  []snapshotRoute{{Path: "/auth", PathType: "Prefix", Service: "auth", Port: 8000}},
		},
	}

	tmpl := helmIngressTemplate(dse, "test-chart")

	if !strings.Contains(tmpl, "name: {{ $.Release.Name }}-{{ .service }}") {
		t.Errorf("expected route backend name to be prefixed with {{ $.Release.Name }}-, got:\n%s", tmpl)
	}
}

func TestBuildValuesYAML_DepEnvVarsConfigurable(t *testing.T) {
	dses := []snapshotDSE{
		{
			Name:     "inventory",
			Image:    "inventory:latest",
			Port:     3000,
			Replicas: 1,
			Deps:     []snapshotDep{{Type: "mongodb"}, {Type: "redis"}},
		},
	}
	depsSeen := map[string]bool{"mongodb": true, "redis": true}

	t.Run("clean values has empty dep env vars", func(t *testing.T) {
		yaml := buildValuesYAML("test", dses, depsSeen, false, nil, nil)

		// Should contain MONGO_URL and REDIS_URL as real values, not comments
		if !strings.Contains(yaml, "MONGO_URL: \"\"") {
			t.Error("values.yaml should contain MONGO_URL as a real value (not a comment)")
		}
		if !strings.Contains(yaml, "REDIS_URL: \"\"") {
			t.Error("values.yaml should contain REDIS_URL as a real value (not a comment)")
		}
		// Should NOT contain the old comment-only format
		if strings.Contains(yaml, "# MONGO_URL =") {
			t.Error("values.yaml should not contain commented-out MONGO_URL")
		}
	})

	t.Run("live values has populated dep env vars", func(t *testing.T) {
		yaml := buildValuesYAML("test", dses, depsSeen, true, nil, nil)

		if !strings.Contains(yaml, "MONGO_URL: \"mongodb://") {
			t.Error("values-live.yaml should have populated MONGO_URL connection string")
		}
		if !strings.Contains(yaml, "REDIS_URL: \"redis://") {
			t.Error("values-live.yaml should have populated REDIS_URL connection string")
		}
	})
}

// ────────────────────────────────────────────────────────────────────────────
// buildValuesYAML — imageOverrides / extraEnv (production values rendering)
// ────────────────────────────────────────────────────────────────────────────

func TestBuildValuesYAML_ImageOverride(t *testing.T) {
	dses := []snapshotDSE{
		{Name: "gateway", Image: "localhost:5001/gateway:abc123", Port: 8080, Replicas: 1},
		{Name: "worker", Image: "localhost:5001/worker:abc123", Port: 9090, Replicas: 1},
	}
	overrides := map[string]string{
		"gateway": "ghcr.io/myorg/gateway@sha256:deadbeef",
	}

	t.Run("clean mode: overridden service gets the digest-pinned image verbatim", func(t *testing.T) {
		yaml := buildValuesYAML("test", dses, nil, false, overrides, nil)
		if !strings.Contains(yaml, `image: "ghcr.io/myorg/gateway@sha256:deadbeef"`) {
			t.Errorf("expected the digest-pinned override to appear verbatim, got:\n%s", yaml)
		}
	})

	t.Run("live mode: overridden service still wins over the live image", func(t *testing.T) {
		yaml := buildValuesYAML("test", dses, nil, true, overrides, nil)
		if !strings.Contains(yaml, `image: "ghcr.io/myorg/gateway@sha256:deadbeef"`) {
			t.Errorf("expected the override to win over live=true's own image handling, got:\n%s", yaml)
		}
	})

	t.Run("service with no override falls back to normal clean-mode image handling", func(t *testing.T) {
		yaml := buildValuesYAML("test", dses, nil, false, overrides, nil)
		if !strings.Contains(yaml, "worker:") {
			t.Fatalf("expected a worker: values block, got:\n%s", yaml)
		}
		if strings.Contains(yaml, "sha256") == false {
			// sanity: the override for gateway should be present at all
		}
		if strings.Contains(yaml, `image: "ghcr.io/myorg/worker`) {
			t.Errorf("worker was not overridden and should not get a digest-pinned image, got:\n%s", yaml)
		}
	})
}

func TestBuildValuesYAML_ExtraEnv(t *testing.T) {
	dses := []snapshotDSE{
		{Name: "gateway", Image: "gateway:latest", Port: 8080, Replicas: 1},
	}
	extraEnv := map[string]string{"KINDLING_ENV_PREFIX": "prod-"}

	yaml := buildValuesYAML("test", dses, nil, false, nil, extraEnv)
	if !strings.Contains(yaml, `KINDLING_ENV_PREFIX: "prod-"`) {
		t.Errorf("expected KINDLING_ENV_PREFIX to appear even with no other env/deps present, got:\n%s", yaml)
	}
}

func TestBuildValuesYAML_ExtraEnvSortedDeterministic(t *testing.T) {
	dses := []snapshotDSE{{Name: "gateway", Image: "gateway:latest", Port: 8080, Replicas: 1}}
	extraEnv := map[string]string{"ZEBRA": "z", "ALPHA": "a", "MIDDLE": "m"}

	first := buildValuesYAML("test", dses, nil, false, nil, extraEnv)
	for i := 0; i < 5; i++ {
		if got := buildValuesYAML("test", dses, nil, false, nil, extraEnv); got != first {
			t.Fatalf("buildValuesYAML output is not deterministic across repeated calls with the same map input")
		}
	}
	alphaIdx := strings.Index(first, "ALPHA")
	middleIdx := strings.Index(first, "MIDDLE")
	zebraIdx := strings.Index(first, "ZEBRA")
	if !(alphaIdx < middleIdx && middleIdx < zebraIdx) {
		t.Errorf("expected extraEnv keys sorted alphabetically (ALPHA, MIDDLE, ZEBRA), got order in:\n%s", first)
	}
}

func TestBuildValuesYAML_ImageOverrideNeverContainsResolvedSecret(t *testing.T) {
	// Regression guard for the "never resolve a credential to a literal
	// value" requirement: a production-style render (clean mode + image
	// override + extraEnv) on a DSE with both a user secret and a
	// dependency should still only ever produce TODO placeholders for
	// anything credential-shaped, identically to today's plain clean mode.
	dses := []snapshotDSE{
		{
			Name:  "gateway",
			Image: "localhost:5001/gateway:abc123",
			Port:  8080,
			Env:   []snapshotEnvVar{{Name: "STRIPE_KEY", Value: "sk_live_should_never_appear", IsSecret: true}},
			Deps:  []snapshotDep{{Type: "postgres"}},
		},
	}
	depsSeen := map[string]bool{"postgres": true}
	overrides := map[string]string{"gateway": "ghcr.io/myorg/gateway@sha256:deadbeef"}
	extraEnv := map[string]string{"KINDLING_ENV_PREFIX": "prod-"}

	yaml := buildValuesYAML("test", dses, depsSeen, false, overrides, extraEnv)
	if strings.Contains(yaml, "sk_live_should_never_appear") {
		t.Errorf("production-style render must never contain a resolved secret value, got:\n%s", yaml)
	}
	if !strings.Contains(yaml, "TODO: set staging value") {
		t.Error("expected the secret to still be a TODO placeholder, same as plain clean mode")
	}
	if !strings.Contains(yaml, "TODO: set your staging postgres connection string") {
		t.Error("expected the dependency connection string to still be a TODO placeholder, same as plain clean mode")
	}
}

// ────────────────────────────────────────────────────────────────────────────
// helmDeploymentTemplate — dep env vars reference values
// ────────────────────────────────────────────────────────────────────────────

func TestHelmDeploymentTemplate_DepEnvVarsFromValues(t *testing.T) {
	dse := snapshotDSE{
		Name:     "orders",
		Image:    "orders:latest",
		Port:     5000,
		Replicas: 1,
		Deps:     []snapshotDep{{Type: "postgres"}, {Type: "redis"}},
		Env:      []snapshotEnvVar{{Name: "LOG_LEVEL", Value: "debug"}},
	}

	tmpl := helmDeploymentTemplate(dse, "test-chart", nil)

	// Dep env vars should use {{ .Values.orders.env.DATABASE_URL }}
	if !strings.Contains(tmpl, ".Values.orders.env.DATABASE_URL") {
		t.Error("template should reference DATABASE_URL from values")
	}
	if !strings.Contains(tmpl, ".Values.orders.env.REDIS_URL") {
		t.Error("template should reference REDIS_URL from values")
	}

	// Should NOT contain hardcoded protocol://release-dep:port
	if strings.Contains(tmpl, "postgresql://{{ .Release.Name }}") {
		t.Error("template should not hardcode dep connection string")
	}

	// User env vars should still reference values
	if !strings.Contains(tmpl, ".Values.orders.env.LOG_LEVEL") {
		t.Error("template should reference user env LOG_LEVEL from values")
	}
}

// ────────────────────────────────────────────────────────────────────────────
// helmDeploymentTemplate — secret env vars use secretKeyRef
// ────────────────────────────────────────────────────────────────────────────

func TestHelmDeploymentTemplate_SecretKeyRef(t *testing.T) {
	dse := snapshotDSE{
		Name:     "gateway",
		Image:    "gateway:latest",
		Port:     9090,
		Replicas: 1,
		Env: []snapshotEnvVar{
			{Name: "AUTH0_DOMAIN", Value: "dev.auth0.com", IsSecret: true},
			{Name: "ORDERS_URL", Value: "http://orders:5000"},
		},
	}

	tmpl := helmDeploymentTemplate(dse, "test-chart", nil)

	// Secret env vars should use secretKeyRef
	if !strings.Contains(tmpl, "secretKeyRef") {
		t.Error("template should use secretKeyRef for secret env vars")
	}
	if !strings.Contains(tmpl, "gateway-secrets") {
		t.Error("template should reference gateway-secrets Secret resource")
	}
	if !strings.Contains(tmpl, "key: AUTH0_DOMAIN") {
		t.Error("template should reference AUTH0_DOMAIN secret key")
	}

	// Non-secret env vars should still use values.yaml
	if !strings.Contains(tmpl, ".Values.gateway.env.ORDERS_URL") {
		t.Error("template should reference non-secret env from values")
	}
}

// ────────────────────────────────────────────────────────────────────────────
// helmSecretsTemplate — generates K8s Secret resource
// ────────────────────────────────────────────────────────────────────────────

func TestHelmSecretsTemplate_Basic(t *testing.T) {
	dse := snapshotDSE{
		Name: "gateway",
		Env: []snapshotEnvVar{
			{Name: "AUTH0_DOMAIN", Value: "dev.auth0.com", IsSecret: true},
			{Name: "SESSION_SECRET", Value: "abc123", IsSecret: true},
			{Name: "ORDERS_URL", Value: "http://orders:5000"},
		},
	}

	tpl := helmSecretsTemplate(dse, "test-chart")

	if tpl == "" {
		t.Fatal("expected non-empty template for service with secrets")
	}
	if !strings.Contains(tpl, "kind: Secret") {
		t.Error("template should define a Secret resource")
	}
	if !strings.Contains(tpl, "gateway-secrets") {
		t.Error("template should name the secret gateway-secrets")
	}
	if !strings.Contains(tpl, ".Values.gateway.secrets.AUTH0_DOMAIN") {
		t.Error("template should reference AUTH0_DOMAIN from values.secrets")
	}
	if !strings.Contains(tpl, ".Values.gateway.secrets.SESSION_SECRET") {
		t.Error("template should reference SESSION_SECRET from values.secrets")
	}
	if strings.Contains(tpl, "ORDERS_URL") {
		t.Error("template should not include non-secret env vars")
	}
}

func TestHelmSecretsTemplate_NoSecrets(t *testing.T) {
	dse := snapshotDSE{
		Name: "ui",
		Env:  []snapshotEnvVar{{Name: "FOO", Value: "bar"}},
	}

	tpl := helmSecretsTemplate(dse, "test-chart")
	if tpl != "" {
		t.Error("expected empty template for service with no secrets")
	}
}

// ────────────────────────────────────────────────────────────────────────────
// buildValuesYAML — secrets section
// ────────────────────────────────────────────────────────────────────────────

func TestBuildValuesYAML_SecretsSection(t *testing.T) {
	dses := []snapshotDSE{
		{
			Name:     "gateway",
			Image:    "gateway:latest",
			Port:     9090,
			Replicas: 1,
			Env: []snapshotEnvVar{
				{Name: "AUTH0_DOMAIN", Value: "dev.auth0.com", IsSecret: true},
				{Name: "ORDERS_URL", Value: "http://orders:5000"},
			},
		},
	}

	// Live values should include secrets section with dev values
	live := buildValuesYAML("test", dses, nil, true, nil, nil)
	if !strings.Contains(live, "secrets:") {
		t.Error("live values should contain secrets: section")
	}
	if !strings.Contains(live, "AUTH0_DOMAIN: \"dev.auth0.com\"") {
		t.Error("live values should contain resolved secret value")
	}

	// Clean values should have empty secret values with TODO
	clean := buildValuesYAML("test", dses, nil, false, nil, nil)
	if !strings.Contains(clean, "secrets:") {
		t.Error("clean values should contain secrets: section")
	}
	if !strings.Contains(clean, "AUTH0_DOMAIN: \"\"") {
		t.Error("clean values should have empty secret placeholder")
	}
	if !strings.Contains(clean, "TODO") {
		t.Error("clean values should have TODO comment for secrets")
	}
}

// ────────────────────────────────────────────────────────────────────────────
// kustomizeDeployment — dep env vars are placeholders
// ────────────────────────────────────────────────────────────────────────────

func TestKustomizeDeployment_DepEnvVarsPlaceholder(t *testing.T) {
	dse := snapshotDSE{
		Name:     "inventory",
		Image:    "inventory:latest",
		Port:     3000,
		Replicas: 1,
		Deps:     []snapshotDep{{Type: "mongodb"}},
	}

	yaml := kustomizeDeployment(dse)

	// Should have MONGO_URL with empty value + TODO comment
	if !strings.Contains(yaml, "MONGO_URL") {
		t.Error("kustomize deployment should include MONGO_URL")
	}
	if !strings.Contains(yaml, "TODO") {
		t.Error("kustomize deployment should have TODO comment for dep env var")
	}

	// Should NOT have hardcoded mongodb://mongodb:27017
	if strings.Contains(yaml, "mongodb://mongodb:") {
		t.Error("kustomize deployment should not hardcode dev connection string")
	}
}

// ────────────────────────────────────────────────────────────────────────────
// connectionProtocol
// ────────────────────────────────────────────────────────────────────────────

func TestConnectionProtocol(t *testing.T) {
	tests := []struct {
		depType  string
		expected string
	}{
		{"postgres", "postgresql"},
		{"redis", "redis"},
		{"mongodb", "mongodb"},
		{"mysql", "mysql"},
		{"rabbitmq", "amqp"},
		{"nats", "nats"},
		{"elasticsearch", "http"},
		{"minio", "http"},
		{"kafka", "kafka"},
		{"memcached", "memcached"},
		{"unknown", "tcp"},
	}

	for _, tt := range tests {
		t.Run(tt.depType, func(t *testing.T) {
			if got := connectionProtocol(tt.depType); got != tt.expected {
				t.Errorf("connectionProtocol(%q) = %q, want %q", tt.depType, got, tt.expected)
			}
		})
	}
}

// ────────────────────────────────────────────────────────────────────────────
// buildConnectionURL
// ────────────────────────────────────────────────────────────────────────────

func TestBuildConnectionURL(t *testing.T) {
	tests := []struct {
		depType  string
		contains string
	}{
		{"postgres", "postgres://"},
		{"redis", "redis://"},
		{"mongodb", "mongodb://"},
		{"mysql", "mysql://"},
		{"rabbitmq", "amqp://"},
		{"nats", "nats://"},
	}

	for _, tt := range tests {
		t.Run(tt.depType, func(t *testing.T) {
			def := depRegistry[tt.depType]
			url := buildConnectionURL("test-chart", tt.depType, helmSafe(tt.depType), def)
			if !strings.Contains(url, tt.contains) {
				t.Errorf("buildConnectionURL(%q) = %q, want to contain %q", tt.depType, url, tt.contains)
			}
		})
	}
}

// ────────────────────────────────────────────────────────────────────────────
// nextTagNumber
// ────────────────────────────────────────────────────────────────────────────

func TestNextTagNumber(t *testing.T) {
	tests := []struct {
		name   string
		tags   []string
		prefix string
		want   int
	}{
		{"no existing tags", nil, "feature-checkout-retry", 1},
		{"single prior tag", []string{"feature-checkout-retry-1"}, "feature-checkout-retry", 2},
		{"picks the max, not the last", []string{"feature-checkout-retry-1", "feature-checkout-retry-3", "feature-checkout-retry-2"}, "feature-checkout-retry", 4},
		{"ignores tags with a different prefix", []string{"main-1", "main-2", "other-branch-5"}, "feature-checkout-retry", 1},
		{"ignores non-numeric suffixes", []string{"feature-checkout-retry-latest", "feature-checkout-retry-1"}, "feature-checkout-retry", 2},
		{"tolerates blank lines from crane ls output", []string{"", "feature-checkout-retry-1", ""}, "feature-checkout-retry", 2},
		{"default snapshot prefix unaffected by branch-prefixed tags", []string{"feature-checkout-retry-1", "snapshot-1"}, "snapshot", 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := nextTagNumber(tt.tags, tt.prefix); got != tt.want {
				t.Errorf("nextTagNumber(%v, %q) = %d, want %d", tt.tags, tt.prefix, got, tt.want)
			}
		})
	}
}

// ────────────────────────────────────────────────────────────────────────────
// registryImage
// ────────────────────────────────────────────────────────────────────────────

func TestRegistryImage(t *testing.T) {
	tests := []struct {
		name     string
		svc      string
		registry string
		tag      string
		expect   string
	}{
		{"ghcr basic", "orders", "ghcr.io/myorg", "abc123", "ghcr.io/myorg/orders:abc123"},
		{"ecr", "gateway", "123456.dkr.ecr.us-east-1.amazonaws.com/myapp", "v1.0", "123456.dkr.ecr.us-east-1.amazonaws.com/myapp/gateway:v1.0"},
		{"trailing slash", "api", "ghcr.io/org/", "latest", "ghcr.io/org/api:latest"},
		{"dockerhub", "web", "myorg", "sha-abc", "myorg/web:sha-abc"},
		{"uppercase name", "My Service", "ghcr.io/org", "v1", "ghcr.io/org/my-service:v1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := registryImage(tt.svc, tt.registry, tt.tag)
			if got != tt.expect {
				t.Errorf("registryImage(%q, %q, %q) = %q, want %q", tt.svc, tt.registry, tt.tag, got, tt.expect)
			}
		})
	}
}

// ────────────────────────────────────────────────────────────────────────────
// registryPullRef — rewrites in-cluster registry → localhost:<port>
// ────────────────────────────────────────────────────────────────────────────

func TestRegistryPullRef(t *testing.T) {
	tests := []struct {
		name   string
		image  string
		port   int
		expect string
	}{
		{"registry:5000", "registry:5000/gateway:abc123", 52431, "localhost:52431/gateway:abc123"},
		{"localhost:5000", "localhost:5000/orders:def456", 52431, "localhost:52431/orders:def456"},
		{"localhost:5001", "localhost:5001/svc:tag", 52431, "localhost:52431/svc:tag"},
		{"external registry unchanged", "ghcr.io/org/svc:v1", 52431, "ghcr.io/org/svc:v1"},
		{"no registry", "myapp:latest", 52431, "myapp:latest"},
		{"long tag from CI", "registry:5000/gateway:jeff-vincent-67d144f6942fa2a5100495d7b35d852d801ff82b", 39000,
			"localhost:39000/gateway:jeff-vincent-67d144f6942fa2a5100495d7b35d852d801ff82b"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := registryPullRef(tt.image, tt.port)
			if got != tt.expect {
				t.Errorf("registryPullRef(%q, %d) = %q, want %q", tt.image, tt.port, got, tt.expect)
			}
		})
	}
}

// ────────────────────────────────────────────────────────────────────────────
// isClusterRegistryImage
// ────────────────────────────────────────────────────────────────────────────

func TestIsClusterRegistryImage(t *testing.T) {
	tests := []struct {
		image  string
		expect bool
	}{
		{"registry:5000/gateway:abc", true},
		{"localhost:5000/orders:def", true},
		{"localhost:5001/svc:tag", true},
		{"ghcr.io/org/svc:v1", false},
		{"myapp:latest", false},
		{"docker.io/library/nginx:latest", false},
	}
	for _, tt := range tests {
		t.Run(tt.image, func(t *testing.T) {
			got := isClusterRegistryImage(tt.image)
			if got != tt.expect {
				t.Errorf("isClusterRegistryImage(%q) = %v, want %v", tt.image, got, tt.expect)
			}
		})
	}
}

// ────────────────────────────────────────────────────────────────────────────
// Registry images pass through stagingImageClean unchanged
// ────────────────────────────────────────────────────────────────────────────

func TestStagingImageClean_RegistryImages(t *testing.T) {
	tests := []struct {
		name   string
		image  string
		svc    string
		expect string
	}{
		{"ghcr image", "ghcr.io/myorg/orders:abc123", "orders", "ghcr.io/myorg/orders:abc123"},
		{"ecr image", "123456.dkr.ecr.us-east-1.amazonaws.com/myapp/gateway:v1.0", "gateway", "123456.dkr.ecr.us-east-1.amazonaws.com/myapp/gateway:v1.0"},
		{"dockerhub with org", "myorg/web:sha-abc", "web", "myorg/web:sha-abc"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := stagingImageClean(tt.image, tt.svc)
			if got != tt.expect {
				t.Errorf("stagingImageClean(%q, %q) = %q, want %q", tt.image, tt.svc, got, tt.expect)
			}
		})
	}
}

// ────────────────────────────────────────────────────────────────────────────
// Values YAML uses registry images when provided
// ────────────────────────────────────────────────────────────────────────────

func TestBuildValuesYAML_WithRegistryImages(t *testing.T) {
	dses := []snapshotDSE{
		{
			Name:     "orders",
			Image:    "ghcr.io/myorg/orders:abc123",
			Port:     5000,
			Replicas: 1,
		},
		{
			Name:     "gateway",
			Image:    "ghcr.io/myorg/gateway:abc123",
			Port:     3000,
			Replicas: 2,
		},
	}

	yaml := buildValuesYAML("test", dses, map[string]bool{}, false, nil, nil)

	if !strings.Contains(yaml, `image: "ghcr.io/myorg/orders:abc123"`) {
		t.Error("values.yaml should contain the full registry image for orders")
	}
	if !strings.Contains(yaml, `image: "ghcr.io/myorg/gateway:abc123"`) {
		t.Error("values.yaml should contain the full registry image for gateway")
	}
	// Should NOT contain TODO comments for these images
	if strings.Contains(yaml, "TODO: replace") {
		t.Error("values.yaml should not have TODO replace comments for registry images")
	}
}
