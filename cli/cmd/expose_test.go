package cmd

import (
	"net/url"
	"strings"
	"testing"
)

// ════════════════════════════════════════════════════════════════════
// Constants
// ════════════════════════════════════════════════════════════════════

func TestCallbackIngressNameConstant(t *testing.T) {
	if callbackIngressName != "kindling-callback" {
		t.Errorf("callbackIngressName = %q, want %q", callbackIngressName, "kindling-callback")
	}
}

func TestOriginalHostAnnotation(t *testing.T) {
	if originalHostAnnotation != "kindling.dev/original-host" {
		t.Errorf("originalHostAnnotation = %q, want %q", originalHostAnnotation, "kindling.dev/original-host")
	}
}

func TestOriginalTLSAnnotation(t *testing.T) {
	if originalTLSAnnotation != "kindling.dev/original-tls" {
		t.Errorf("originalTLSAnnotation = %q, want %q", originalTLSAnnotation, "kindling.dev/original-tls")
	}
}

// ════════════════════════════════════════════════════════════════════
// Route flag parsing (--route /path=service)
// ════════════════════════════════════════════════════════════════════

func TestParseRouteFlags(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantKey string
		wantVal string
		wantErr bool
	}{
		{"auth route", "/auth=gateway", "/auth", "gateway", false},
		{"webhook route", "/webhooks=gateway", "/webhooks", "gateway", false},
		{"root route", "/=ui", "/", "ui", false},
		{"nested path", "/api/v1=backend", "/api/v1", "backend", false},
		{"missing equals", "/auth", "", "", true},
		{"empty service", "/auth=", "", "", true},
		{"empty path", "=gateway", "", "", true},
		{"completely empty", "", "", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			parts := strings.SplitN(tt.input, "=", 2)
			if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
				if !tt.wantErr {
					t.Errorf("unexpected parse failure for %q", tt.input)
				}
				return
			}
			if tt.wantErr {
				t.Errorf("expected error for %q but parsed ok", tt.input)
				return
			}
			path := parts[0]
			if !strings.HasPrefix(path, "/") {
				path = "/" + path
			}
			svc := parts[1]
			if path != tt.wantKey {
				t.Errorf("path = %q, want %q", path, tt.wantKey)
			}
			if svc != tt.wantVal {
				t.Errorf("service = %q, want %q", svc, tt.wantVal)
			}
		})
	}
}

func TestRoutePathAutoPrefixSlash(t *testing.T) {
	// Routes without leading slash should get one prepended
	input := "auth=gateway"
	parts := strings.SplitN(input, "=", 2)
	path := parts[0]
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	if path != "/auth" {
		t.Errorf("path = %q, want %q", path, "/auth")
	}
}

// ════════════════════════════════════════════════════════════════════
// Tunnel URL → hostname extraction
// ════════════════════════════════════════════════════════════════════

func TestTunnelHostnameExtraction(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		wantHost string
	}{
		{"cloudflared URL", "https://abc-def-ghi.trycloudflare.com", "abc-def-ghi.trycloudflare.com"},
		{"ngrok URL", "https://abc123.ngrok-free.app", "abc123.ngrok-free.app"},
		{"with trailing slash", "https://test.trycloudflare.com/", "test.trycloudflare.com"},
		{"bare hostname", "test.example.com", "test.example.com"},
		{"with port", "https://test.example.com:8443", "test.example.com:8443"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hostname := tt.input
			if u, err := url.Parse(tt.input); err == nil && u.Host != "" {
				hostname = u.Host
			}
			if hostname != tt.wantHost {
				t.Errorf("hostname = %q, want %q", hostname, tt.wantHost)
			}
		})
	}
}

// ════════════════════════════════════════════════════════════════════
// Ingress annotation JSON-patch escaping
// ════════════════════════════════════════════════════════════════════

func TestAnnotationPathEscaping(t *testing.T) {
	// JSON-Patch RFC 6901 requires "/" in annotation keys to be escaped as "~1"
	escaped := strings.ReplaceAll(originalHostAnnotation, "/", "~1")
	expected := "kindling.dev~1original-host"
	if escaped != expected {
		t.Errorf("escaped annotation = %q, want %q", escaped, expected)
	}

	tlsEscaped := strings.ReplaceAll(originalTLSAnnotation, "/", "~1")
	tlsExpected := "kindling.dev~1original-tls"
	if tlsEscaped != tlsExpected {
		t.Errorf("escaped TLS annotation = %q, want %q", tlsEscaped, tlsExpected)
	}
}

// ════════════════════════════════════════════════════════════════════
// Route merging behavior
// ════════════════════════════════════════════════════════════════════

func TestRouteMerging(t *testing.T) {
	// Simulates the merging logic in runCallbackDomain:
	// existing routes + new routes → merged (new overrides existing)
	existing := map[string]string{
		"/auth":     "old-gateway",
		"/webhooks": "old-gateway",
	}
	newRoutes := map[string]string{
		"/auth":     "new-gateway",
		"/payments": "billing",
	}

	for path, svc := range newRoutes {
		existing[path] = svc
	}

	if existing["/auth"] != "new-gateway" {
		t.Errorf("Route /auth = %q, want %q (new should override)", existing["/auth"], "new-gateway")
	}
	if existing["/webhooks"] != "old-gateway" {
		t.Errorf("Route /webhooks = %q, want %q (untouched)", existing["/webhooks"], "old-gateway")
	}
	if existing["/payments"] != "billing" {
		t.Errorf("Route /payments = %q, want %q (new addition)", existing["/payments"], "billing")
	}
	if len(existing) != 3 {
		t.Errorf("merged routes count = %d, want 3", len(existing))
	}
}

func TestRouteRemoval(t *testing.T) {
	routes := map[string]string{
		"/auth":     "gateway",
		"/webhooks": "gateway",
		"/":         "ui",
	}

	path := "/webhooks"
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}

	if _, exists := routes[path]; !exists {
		t.Fatalf("route %q should exist", path)
	}
	delete(routes, path)

	if _, exists := routes[path]; exists {
		t.Errorf("route %q should have been removed", path)
	}
	if len(routes) != 2 {
		t.Errorf("routes count = %d, want 2", len(routes))
	}
}

func TestRemoveNonExistentRoute(t *testing.T) {
	routes := map[string]string{"/auth": "gateway"}

	if _, exists := routes["/nonexistent"]; exists {
		t.Error("route /nonexistent should not exist")
	}
}

func TestRemovePathNormalization(t *testing.T) {
	// Same logic as removeCallbackRoute: ensure path gets "/" prefix
	path := "auth"
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	if path != "/auth" {
		t.Errorf("path = %q, want %q", path, "/auth")
	}
}
