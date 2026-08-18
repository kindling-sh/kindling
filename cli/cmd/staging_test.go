package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ────────────────────────────────────────────────────────────────────────────
// patchDSEWithTLS
// ────────────────────────────────────────────────────────────────────────────

func TestPatchDSEWithTLS(t *testing.T) {
	tests := []struct {
		name         string
		input        string
		domain       string
		issuer       string
		ingressClass string
		wantContains []string
		wantAbsent   []string
	}{
		{
			name: "basic ingress with host",
			input: `apiVersion: kindling.dev/v1alpha1
kind: DevStagingEnvironment
metadata:
  name: gateway
spec:
  image: gateway:latest
  ingress:
    enabled: true
    host: gateway.local
    path: /`,
			domain:       "app.example.com",
			issuer:       "letsencrypt-prod",
			ingressClass: "traefik",
			wantContains: []string{
				"host: app.example.com",
				"ingressClassName: traefik",
				"cert-manager.io/cluster-issuer: letsencrypt-prod",
				"secretName: app-example-com-tls",
				"- app.example.com",
			},
			wantAbsent: []string{
				"host: gateway.local",
			},
		},
		{
			name: "ingress without host adds one",
			input: `spec:
  ingress:
    enabled: true
    path: /`,
			domain:       "myapp.dev",
			issuer:       "letsencrypt-prod",
			ingressClass: "traefik",
			wantContains: []string{
				"host: myapp.dev",
			},
		},
		{
			name: "replaces existing ingressClassName",
			input: `spec:
  ingress:
    enabled: true
    host: old.example.com
    ingressClassName: nginx
    path: /`,
			domain:       "new.example.com",
			issuer:       "letsencrypt-prod",
			ingressClass: "traefik",
			wantContains: []string{
				"ingressClassName: traefik",
			},
			wantAbsent: []string{
				"ingressClassName: nginx",
			},
		},
		{
			name: "domain with dots converted for secret name",
			input: `spec:
  ingress:
    enabled: true
    host: old.example.com
    path: /`,
			domain:       "sub.domain.example.com",
			issuer:       "letsencrypt-staging",
			ingressClass: "traefik",
			wantContains: []string{
				"secretName: sub-domain-example-com-tls",
			},
		},
		{
			name: "replaces existing TLS block",
			input: `spec:
  ingress:
    enabled: true
    host: old.example.com
    path: /
    tls:
      secretName: old-tls
      hosts:
        - old.example.com`,
			domain:       "new.example.com",
			issuer:       "letsencrypt-prod",
			ingressClass: "traefik",
			wantContains: []string{
				"host: new.example.com",
			},
			wantAbsent: []string{
				"host: old.example.com",
				"secretName: old-tls",
				"- old.example.com",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "dse.yaml")
			if err := os.WriteFile(path, []byte(tt.input), 0644); err != nil {
				t.Fatal(err)
			}

			if err := patchDSEWithTLS(path, tt.domain, tt.issuer, tt.ingressClass); err != nil {
				t.Fatalf("patchDSEWithTLS() error: %v", err)
			}

			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			content := string(data)

			for _, want := range tt.wantContains {
				if !strings.Contains(content, want) {
					t.Errorf("output missing %q\n---\n%s", want, content)
				}
			}
			for _, absent := range tt.wantAbsent {
				if strings.Contains(content, absent) {
					t.Errorf("output should not contain %q\n---\n%s", absent, content)
				}
			}
		})
	}
}

// ────────────────────────────────────────────────────────────────────────────
// patchDSEWithTLS — file not found
// ────────────────────────────────────────────────────────────────────────────

func TestPatchDSEWithTLS_FileNotFound(t *testing.T) {
	err := patchDSEWithTLS("/nonexistent/file.yaml", "example.com", "issuer", "traefik")
	if err == nil {
		t.Error("expected error for nonexistent file, got nil")
	}
}
