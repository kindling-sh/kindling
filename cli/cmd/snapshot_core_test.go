package cmd

import (
	"strings"
	"testing"
)

// ════════════════════════════════════════════════════════════════
// stripDSEPrefix
// ════════════════════════════════════════════════════════════════

func TestStripDSEPrefix_Basic(t *testing.T) {
	dses := []snapshotDSE{
		{Name: "jeff-vincent-gateway", Ingress: &snapshotIngress{Host: "jeff-vincent-gateway.local"}},
		{Name: "jeff-vincent-orders", Env: []snapshotEnvVar{{Name: "API_URL", Value: "http://jeff-vincent-gateway:3000"}}},
		{Name: "jeff-vincent-ui"},
	}

	prefix := stripDSEPrefix(dses)

	if prefix != "jeff-vincent-" {
		t.Errorf("expected prefix %q, got %q", "jeff-vincent-", prefix)
	}
	if dses[0].Name != "gateway" {
		t.Errorf("expected name %q, got %q", "gateway", dses[0].Name)
	}
	if dses[1].Name != "orders" {
		t.Errorf("expected name %q, got %q", "orders", dses[1].Name)
	}
	if dses[2].Name != "ui" {
		t.Errorf("expected name %q, got %q", "ui", dses[2].Name)
	}
}

func TestStripDSEPrefix_StripsIngressHost(t *testing.T) {
	dses := []snapshotDSE{
		{Name: "jeff-vincent-gateway", Ingress: &snapshotIngress{Host: "jeff-vincent-gateway.local"}},
		{Name: "jeff-vincent-api"},
	}

	stripDSEPrefix(dses)

	if dses[0].Ingress.Host != "gateway.local" {
		t.Errorf("expected ingress host %q, got %q", "gateway.local", dses[0].Ingress.Host)
	}
}

func TestStripDSEPrefix_StripsEnvVarValues(t *testing.T) {
	dses := []snapshotDSE{
		{Name: "jeff-vincent-gateway"},
		{Name: "jeff-vincent-orders", Env: []snapshotEnvVar{
			{Name: "GATEWAY_URL", Value: "http://jeff-vincent-gateway:3000"},
			{Name: "LOG_LEVEL", Value: "debug"},
		}},
	}

	stripDSEPrefix(dses)

	if dses[1].Env[0].Value != "http://gateway:3000" {
		t.Errorf("expected env value %q, got %q", "http://gateway:3000", dses[1].Env[0].Value)
	}
	// Non-prefix values should be unchanged
	if dses[1].Env[1].Value != "debug" {
		t.Errorf("expected env value %q unchanged, got %q", "debug", dses[1].Env[1].Value)
	}
}

func TestStripDSEPrefix_NoPrefix(t *testing.T) {
	dses := []snapshotDSE{
		{Name: "gateway"},
		{Name: "orders"},
	}

	prefix := stripDSEPrefix(dses)
	if prefix != "" {
		t.Errorf("expected empty prefix, got %q", prefix)
	}
	if dses[0].Name != "gateway" {
		t.Error("names should be unchanged when no prefix detected")
	}
}

func TestStripDSEPrefix_SingleDSE(t *testing.T) {
	dses := []snapshotDSE{
		{Name: "jeff-vincent-gateway"},
	}

	prefix := stripDSEPrefix(dses)
	if prefix != "" {
		t.Errorf("expected empty prefix for single DSE, got %q", prefix)
	}
}

func TestStripDSEPrefix_NilIngress(t *testing.T) {
	dses := []snapshotDSE{
		{Name: "jeff-vincent-gateway", Ingress: nil},
		{Name: "jeff-vincent-orders"},
	}

	// Should not panic when Ingress is nil
	prefix := stripDSEPrefix(dses)
	if prefix != "jeff-vincent-" {
		t.Errorf("expected prefix %q, got %q", "jeff-vincent-", prefix)
	}
}

// ════════════════════════════════════════════════════════════════
// buildConnectionURL — releasePrefix parameter
// ════════════════════════════════════════════════════════════════

func TestBuildConnectionURL_UsesReleasePrefix(t *testing.T) {
	def := depRegistry["postgres"]
	url := buildConnectionURL("kindling-snapshot", "postgres", "postgres", def)

	if !strings.Contains(url, "kindling-snapshot-postgres") {
		t.Errorf("URL should use release prefix as host, got: %s", url)
	}
	// Should NOT contain literal <release>
	if strings.Contains(url, "<release>") {
		t.Error("URL should not contain literal <release>")
	}
}

func TestBuildConnectionURL_EmptyPrefix(t *testing.T) {
	def := depRegistry["redis"]
	url := buildConnectionURL("", "redis", "redis", def)

	// Empty prefix should fall back to <release>
	if !strings.Contains(url, "<release>-redis") {
		t.Errorf("URL with empty prefix should use <release>, got: %s", url)
	}
}

func TestBuildConnectionURL_AllDepTypes(t *testing.T) {
	expected := map[string]string{
		"postgres":      "postgres://",
		"redis":         "redis://",
		"mysql":         "mysql://",
		"mongodb":       "mongodb://",
		"rabbitmq":      "amqp://",
		"nats":          "nats://",
		"elasticsearch": "http://",
		"minio":         "http://",
		"kafka":         "test-kafka:",     // kafka uses host:port, no protocol
		"memcached":     "test-memcached:", // memcached uses host:port, no protocol
	}

	for depType, expectedProto := range expected {
		t.Run(depType, func(t *testing.T) {
			def, ok := depRegistry[depType]
			if !ok {
				t.Skipf("dep type %q not in registry", depType)
			}
			url := buildConnectionURL("test", depType, helmSafe(depType), def)
			if !strings.Contains(url, expectedProto) {
				t.Errorf("buildConnectionURL(%q) = %q, want to contain %q", depType, url, expectedProto)
			}
			// All should contain the release prefix
			if !strings.Contains(url, "test-") {
				t.Errorf("URL should contain release prefix 'test-', got: %s", url)
			}
		})
	}
}

// ════════════════════════════════════════════════════════════════
// helmValuesKey
// ════════════════════════════════════════════════════════════════

func TestHelmValuesKey(t *testing.T) {
	tests := []struct {
		input  string
		expect string
	}{
		{"orders", "orders"},
		{"my-service", "my_service"},
		{"My Service", "my_service"},
		{"my_api", "my_api"},
		{"gateway-api", "gateway_api"},
		{"UPPER-CASE", "upper_case"},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			if got := helmValuesKey(tt.input); got != tt.expect {
				t.Errorf("helmValuesKey(%q) = %q, want %q", tt.input, got, tt.expect)
			}
		})
	}
}

// ════════════════════════════════════════════════════════════════
// rewriteServiceURL
// ════════════════════════════════════════════════════════════════

func TestRewriteServiceURL(t *testing.T) {
	services := map[string]bool{
		"gateway":   true,
		"inventory": true,
	}

	tests := []struct {
		name     string
		value    string
		expected string
	}{
		{
			name:     "rewrites service reference with port",
			value:    "http://gateway:3000/api",
			expected: `"http://{{ .Release.Name }}-gateway:3000/api"`,
		},
		{
			name:     "rewrites service reference with path",
			value:    "http://inventory/items",
			expected: `"http://{{ .Release.Name }}-inventory/items"`,
		},
		{
			name:     "no match for unknown service",
			value:    "http://unknown-service:3000",
			expected: "",
		},
		{
			name:     "no match for non-URL string",
			value:    "just a plain string",
			expected: "",
		},
		{
			name:     "no match for external URL",
			value:    "https://api.stripe.com/v1",
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := rewriteServiceURL(tt.value, services)
			if got != tt.expected {
				t.Errorf("rewriteServiceURL(%q) = %q, want %q", tt.value, got, tt.expected)
			}
		})
	}
}

func TestRewriteServiceURL_EmptyServices(t *testing.T) {
	got := rewriteServiceURL("http://gateway:3000", map[string]bool{})
	if got != "" {
		t.Errorf("expected empty string for empty services, got %q", got)
	}
}

// ════════════════════════════════════════════════════════════════
// Compute tags in buildValuesYAML
// ════════════════════════════════════════════════════════════════

func TestBuildValuesYAML_ComputeTag(t *testing.T) {
	dses := []snapshotDSE{
		{
			Name:     "inference",
			Image:    "inference:latest",
			Port:     8080,
			Replicas: 1,
			Compute:  "gpu",
		},
		{
			Name:     "api",
			Image:    "api:latest",
			Port:     3000,
			Replicas: 2,
			Compute:  "", // no compute tag
		},
	}

	yaml := buildValuesYAML("test", dses, map[string]bool{}, false)

	// Service with compute tag should have it set
	if !strings.Contains(yaml, `compute: "gpu"`) {
		t.Error("values.yaml should contain compute: \"gpu\" for inference service")
	}

	// Service without compute tag should have empty string with hint comment
	if !strings.Contains(yaml, `compute: ""`) {
		t.Error("values.yaml should contain empty compute field for api service")
	}
}

func TestBuildValuesYAML_ComputeLive(t *testing.T) {
	dses := []snapshotDSE{
		{
			Name:     "ml-worker",
			Image:    "ml-worker:latest",
			Port:     8080,
			Replicas: 1,
			Compute:  "high-memory",
		},
	}

	yaml := buildValuesYAML("test", dses, map[string]bool{}, true)

	if !strings.Contains(yaml, `compute: "high-memory"`) {
		t.Error("live values should contain compute tag")
	}
}

// ════════════════════════════════════════════════════════════════
// Compute tags in helmDeploymentTemplate
// ════════════════════════════════════════════════════════════════

func TestHelmDeploymentTemplate_ComputeTag(t *testing.T) {
	dse := snapshotDSE{
		Name:     "inference",
		Image:    "inference:latest",
		Port:     8080,
		Replicas: 1,
		Compute:  "gpu",
	}

	tmpl := helmDeploymentTemplate(dse, "test-chart", nil)

	// Should have conditional nodeSelector
	if !strings.Contains(tmpl, "nodeSelector") {
		t.Error("template should contain nodeSelector block")
	}
	if !strings.Contains(tmpl, "kindling.dev/compute") {
		t.Error("template should reference kindling.dev/compute label")
	}

	// Should have matching toleration
	if !strings.Contains(tmpl, "tolerations") {
		t.Error("template should contain tolerations block")
	}

	// Should reference values
	vk := helmValuesKey("inference")
	if !strings.Contains(tmpl, ".Values."+vk+".compute") {
		t.Error("template should reference .Values.inference.compute")
	}
}

func TestHelmDeploymentTemplate_NoComputeTag(t *testing.T) {
	dse := snapshotDSE{
		Name:     "api",
		Image:    "api:latest",
		Port:     3000,
		Replicas: 1,
		Compute:  "",
	}

	tmpl := helmDeploymentTemplate(dse, "test-chart", nil)

	// Template should still contain the conditional blocks (they're always generated)
	// but they'll be empty when compute is "" due to the {{- if .Values.api.compute }} guard
	if !strings.Contains(tmpl, "if .Values.api.compute") {
		t.Error("template should contain conditional compute check even for services without compute")
	}
}

// ════════════════════════════════════════════════════════════════
// connectionProtocol (extended)
// ════════════════════════════════════════════════════════════════

func TestConnectionProtocol_AllRegistered(t *testing.T) {
	// Every dep in depRegistry should return a non-empty protocol
	for depType := range depRegistry {
		t.Run(depType, func(t *testing.T) {
			proto := connectionProtocol(depType)
			if proto == "" {
				t.Errorf("connectionProtocol(%q) returned empty string", depType)
			}
		})
	}
}

// ════════════════════════════════════════════════════════════════
// helmDeploymentTemplate — env vars
// ════════════════════════════════════════════════════════════════

func TestHelmDeploymentTemplate_UserEnvVarsFromValues(t *testing.T) {
	dse := snapshotDSE{
		Name:     "api",
		Image:    "api:latest",
		Port:     3000,
		Replicas: 1,
		Env: []snapshotEnvVar{
			{Name: "LOG_LEVEL", Value: "debug"},
			{Name: "PORT", Value: "3000"},
		},
	}

	tmpl := helmDeploymentTemplate(dse, "test-chart", nil)

	vk := helmValuesKey("api")
	if !strings.Contains(tmpl, ".Values."+vk+".env.LOG_LEVEL") {
		t.Error("template should reference LOG_LEVEL from values")
	}
	if !strings.Contains(tmpl, ".Values."+vk+".env.PORT") {
		t.Error("template should reference PORT from values")
	}
}

func TestHelmDeploymentTemplate_ServiceURLRewriting(t *testing.T) {
	dse := snapshotDSE{
		Name:     "orders",
		Image:    "orders:latest",
		Port:     5000,
		Replicas: 1,
		Env: []snapshotEnvVar{
			{Name: "GATEWAY_URL", Value: "http://gateway:3000/api"},
		},
	}
	allDSEs := []snapshotDSE{
		{Name: "gateway"},
		dse,
	}

	tmpl := helmDeploymentTemplate(dse, "test-chart", allDSEs)

	// The gateway reference should be rewritten to use {{ .Release.Name }}-gateway
	if !strings.Contains(tmpl, "{{ .Release.Name }}-gateway") {
		t.Error("template should rewrite service URL to use Release.Name prefix")
	}
}

func TestHelmDeploymentTemplate_NoEnv(t *testing.T) {
	dse := snapshotDSE{
		Name:     "static",
		Image:    "static:latest",
		Port:     80,
		Replicas: 1,
	}

	tmpl := helmDeploymentTemplate(dse, "test-chart", nil)

	// Should still be valid YAML without env section
	if !strings.Contains(tmpl, "kind: Deployment") {
		t.Error("template should contain Deployment kind")
	}
	// Should have image and port from values
	vk := helmValuesKey("static")
	if !strings.Contains(tmpl, ".Values."+vk+".image") {
		t.Error("template should reference image from values")
	}
}

// ════════════════════════════════════════════════════════════════
// DeployOpts struct validation
// ════════════════════════════════════════════════════════════════

func TestDeployOpts_CredOverrides(t *testing.T) {
	opts := DeployOpts{
		Context:   "test-context",
		Namespace: "default",
		Format:    "helm",
		ChartName: "test",
		CredOverrides: map[string]map[string]credOverride{
			"orders": {"DATABASE_URL": {Value: "postgres://prod@db:5432/app", IsSecret: false}},
		},
	}

	// Verify the struct holds the data correctly
	if len(opts.CredOverrides) != 1 {
		t.Errorf("expected 1 cred override, got %d", len(opts.CredOverrides))
	}
	if opts.CredOverrides["orders"]["DATABASE_URL"].Value != "postgres://prod@db:5432/app" {
		t.Error("CredOverrides should hold the correct value")
	}
}
