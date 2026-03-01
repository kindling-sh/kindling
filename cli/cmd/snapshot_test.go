package cmd

import (
	"strings"
	"testing"
)

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
// productionImageClean
// ────────────────────────────────────────────────────────────────────────────

func TestProductionImageClean(t *testing.T) {
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
			if got := productionImageClean(tt.image, tt.svc); got != tt.expect {
				t.Errorf("productionImageClean(%q, %q) = %q, want %q", tt.image, tt.svc, got, tt.expect)
			}
		})
	}
}

// ────────────────────────────────────────────────────────────────────────────
// buildValuesYAML — dep env vars are real configurable values
// ────────────────────────────────────────────────────────────────────────────

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
		yaml := buildValuesYAML("test", dses, depsSeen, false)

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
		yaml := buildValuesYAML("test", dses, depsSeen, true)

		if !strings.Contains(yaml, "MONGO_URL: \"mongodb://") {
			t.Error("values-live.yaml should have populated MONGO_URL connection string")
		}
		if !strings.Contains(yaml, "REDIS_URL: \"redis://") {
			t.Error("values-live.yaml should have populated REDIS_URL connection string")
		}
	})
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

	tmpl := helmDeploymentTemplate(dse)

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
// buildExampleConnectionURL
// ────────────────────────────────────────────────────────────────────────────

func TestBuildExampleConnectionURL(t *testing.T) {
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
			url := buildExampleConnectionURL(tt.depType, helmSafe(tt.depType), def)
			if !strings.Contains(url, tt.contains) {
				t.Errorf("buildExampleConnectionURL(%q) = %q, want to contain %q", tt.depType, url, tt.contains)
			}
		})
	}
}
