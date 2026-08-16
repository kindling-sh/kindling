package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const sampleDSEYAML = `apiVersion: apps.example.com/v1alpha1
kind: DevStagingEnvironment
metadata:
  name: sample-app-dev
  labels:
    app.kubernetes.io/part-of: sample-app
    app.kubernetes.io/managed-by: kindling
spec:
  deployment:
    image: sample-app:dev
    replicas: 1
    port: 8080
    healthCheck:
      path: /healthz

  service:
    port: 8080
    type: ClusterIP

  # ── Ingress ────────────────────────────────────────────────────
  # Works out of the box with Traefik on Kind.
  ingress:
    enabled: true
    host: sample-app.localhost
    ingressClassName: traefik

  # ── Dependencies ────────────────────────────────────────────────
  dependencies:
    - type: postgres
      version: "16"
`

func writeTempYAML(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "dev-environment.yaml")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("cannot write temp yaml: %v", err)
	}
	return path
}

func TestWriteRoutesToYAML_AddsRoutesBlock(t *testing.T) {
	path := writeTempYAML(t, sampleDSEYAML)

	routes := []snapshotRoute{
		{Path: "/orders", PathType: "Prefix", Service: "orders", Port: 5000},
		{Path: "/inventory", PathType: "Exact", Service: "inventory", Port: 3000},
	}
	if err := writeRoutesToYAML(path, routes); err != nil {
		t.Fatalf("writeRoutesToYAML failed: %v", err)
	}

	out, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("cannot read result: %v", err)
	}
	result := string(out)

	// Routes block present with expected content.
	for _, want := range []string{
		"routes:",
		"- path: /orders",
		"pathType: Prefix",
		"service: orders",
		"port: 5000",
		"- path: /inventory",
		"pathType: Exact",
		"service: inventory",
		"port: 3000",
	} {
		if !strings.Contains(result, want) {
			t.Errorf("expected result to contain %q, got:\n%s", want, result)
		}
	}

	// Rest of the file (dependencies block, comments) must be preserved.
	if !strings.Contains(result, "dependencies:") || !strings.Contains(result, "type: postgres") {
		t.Errorf("dependencies block was not preserved:\n%s", result)
	}
	if !strings.Contains(result, "ingressClassName: traefik") {
		t.Errorf("existing ingress fields were not preserved:\n%s", result)
	}
	if !strings.Contains(result, "host: sample-app.localhost") {
		t.Errorf("existing host field was not preserved:\n%s", result)
	}
}

func TestWriteRoutesToYAML_ReplacesExistingRoutesBlock(t *testing.T) {
	withRoutes := sampleDSEYAML[:strings.Index(sampleDSEYAML, "ingressClassName: traefik")+len("ingressClassName: traefik")] +
		"\n    routes:\n    - path: /old\n      pathType: Prefix\n      service: old-svc\n      port: 1111\n" +
		sampleDSEYAML[strings.Index(sampleDSEYAML, "\n\n  # ── Dependencies"):]

	path := writeTempYAML(t, withRoutes)

	routes := []snapshotRoute{
		{Path: "/new", PathType: "Prefix", Service: "new-svc", Port: 2222},
	}
	if err := writeRoutesToYAML(path, routes); err != nil {
		t.Fatalf("writeRoutesToYAML failed: %v", err)
	}

	out, _ := os.ReadFile(path)
	result := string(out)

	if strings.Contains(result, "/old") || strings.Contains(result, "old-svc") {
		t.Errorf("old routes block was not removed:\n%s", result)
	}
	if !strings.Contains(result, "/new") || !strings.Contains(result, "new-svc") {
		t.Errorf("new routes block missing:\n%s", result)
	}
	if !strings.Contains(result, "dependencies:") {
		t.Errorf("dependencies block was not preserved:\n%s", result)
	}
}

func TestWriteRoutesToYAML_EmptyRoutesRemovesBlock(t *testing.T) {
	withRoutes := sampleDSEYAML[:strings.Index(sampleDSEYAML, "ingressClassName: traefik")+len("ingressClassName: traefik")] +
		"\n    routes:\n    - path: /old\n      pathType: Prefix\n      service: old-svc\n      port: 1111\n" +
		sampleDSEYAML[strings.Index(sampleDSEYAML, "\n\n  # ── Dependencies"):]

	path := writeTempYAML(t, withRoutes)

	if err := writeRoutesToYAML(path, nil); err != nil {
		t.Fatalf("writeRoutesToYAML failed: %v", err)
	}

	out, _ := os.ReadFile(path)
	result := string(out)
	if strings.Contains(result, "routes:") {
		t.Errorf("expected routes: block to be removed when routes is empty:\n%s", result)
	}
	if !strings.Contains(result, "dependencies:") {
		t.Errorf("dependencies block was not preserved:\n%s", result)
	}
}

func TestWriteRoutesToYAML_NoIngressBlockErrors(t *testing.T) {
	path := writeTempYAML(t, "apiVersion: apps.example.com/v1alpha1\nkind: DevStagingEnvironment\nspec:\n  deployment:\n    image: foo\n")

	err := writeRoutesToYAML(path, []snapshotRoute{{Path: "/x", Service: "x", Port: 1}})
	if err == nil {
		t.Fatal("expected an error when no ingress: block is present")
	}
}
