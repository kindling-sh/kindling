package cmd

import (
	"os"
	"path/filepath"
	"testing"
)

// ────────────────────────────────────────────────────────────────────────────
// isFrontendDeployment
// ────────────────────────────────────────────────────────────────────────────

func TestIsFrontendDeployment(t *testing.T) {
	tests := []struct {
		name    string
		cmdline string
		want    bool
	}{
		{"empty", "", false},
		{"nginx", "nginx -g daemon off;", true},
		{"httpd", "/usr/sbin/httpd -DFOREGROUND", true},
		{"caddy", "caddy run --config /etc/caddy/Caddyfile", true},
		{"serve", "serve -s build -l 3000", true},
		{"http-server", "http-server ./dist", true},
		{"node app", "node server.js", false},
		{"python", "python app.py", false},
		{"go binary", "./main", false},
		{"gunicorn", "gunicorn app:app", false},
		{"apache2", "apache2 -DFOREGROUND", true},
		{"nginx with path", "/usr/sbin/nginx", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isFrontendDeployment(tt.cmdline)
			if got != tt.want {
				t.Errorf("isFrontendDeployment(%q) = %v, want %v", tt.cmdline, got, tt.want)
			}
		})
	}
}

// ────────────────────────────────────────────────────────────────────────────
// detectDevServerPort
// ────────────────────────────────────────────────────────────────────────────

func TestDetectDevServerPort(t *testing.T) {
	tests := []struct {
		name  string
		setup func(dir string)
		want  int
	}{
		{
			name: "vite default",
			setup: func(dir string) {
				os.WriteFile(filepath.Join(dir, "vite.config.ts"),
					[]byte(`export default defineConfig({ plugins: [react()] })`), 0644)
			},
			want: 5173,
		},
		{
			name: "vite custom port",
			setup: func(dir string) {
				os.WriteFile(filepath.Join(dir, "vite.config.ts"),
					[]byte(`export default defineConfig({ server: { port: 8080 } })`), 0644)
			},
			want: 8080,
		},
		{
			name: "next.js",
			setup: func(dir string) {
				os.WriteFile(filepath.Join(dir, "next.config.js"),
					[]byte(`module.exports = {}`), 0644)
			},
			want: 3000,
		},
		{
			name: "next.config.mjs",
			setup: func(dir string) {
				os.WriteFile(filepath.Join(dir, "next.config.mjs"),
					[]byte(`export default {}`), 0644)
			},
			want: 3000,
		},
		{
			name: "angular",
			setup: func(dir string) {
				os.WriteFile(filepath.Join(dir, "angular.json"),
					[]byte(`{}`), 0644)
			},
			want: 4200,
		},
		{
			name: "sveltekit",
			setup: func(dir string) {
				os.WriteFile(filepath.Join(dir, "svelte.config.js"),
					[]byte(`export default {}`), 0644)
			},
			want: 5173,
		},
		{
			name: "unknown framework defaults to 3000",
			setup: func(dir string) {
				// No framework config files
			},
			want: 3000,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			tt.setup(dir)
			got := detectDevServerPort(dir)
			if got != tt.want {
				t.Errorf("detectDevServerPort() = %d, want %d", got, tt.want)
			}
		})
	}
}

// ────────────────────────────────────────────────────────────────────────────
// detectFrontendOAuth
// ────────────────────────────────────────────────────────────────────────────

func TestDetectFrontendOAuth(t *testing.T) {
	tests := []struct {
		name  string
		setup func(dir string)
		want  bool
	}{
		{
			name: "nextauth in package.json",
			setup: func(dir string) {
				os.WriteFile(filepath.Join(dir, "package.json"),
					[]byte(`{"dependencies":{"next-auth":"^4.0.0"}}`), 0644)
			},
			want: true,
		},
		{
			name: "auth0 in package.json",
			setup: func(dir string) {
				os.WriteFile(filepath.Join(dir, "package.json"),
					[]byte(`{"dependencies":{"@auth0/nextjs-auth0":"^2.0.0"}}`), 0644)
			},
			want: true,
		},
		{
			name: "oauth in .env file",
			setup: func(dir string) {
				os.WriteFile(filepath.Join(dir, ".env"),
					[]byte("GOOGLE_CLIENT_ID=abc\nGOOGLE_CLIENT_SECRET=xyz"), 0644)
			},
			want: true,
		},
		{
			name: "oauth in .env.local",
			setup: func(dir string) {
				os.WriteFile(filepath.Join(dir, ".env.local"),
					[]byte("NEXTAUTH_URL=http://localhost:3000"), 0644)
			},
			want: true,
		},
		{
			name: "clerk in source file",
			setup: func(dir string) {
				os.MkdirAll(filepath.Join(dir, "src"), 0755)
				os.WriteFile(filepath.Join(dir, "src", "auth.ts"),
					[]byte(`import { clerk } from '@clerk/nextjs'`), 0644)
			},
			want: true,
		},
		{
			name: "no oauth patterns",
			setup: func(dir string) {
				os.WriteFile(filepath.Join(dir, "package.json"),
					[]byte(`{"dependencies":{"react":"^18.0.0"}}`), 0644)
			},
			want: false,
		},
		{
			name:  "empty directory",
			setup: func(dir string) {},
			want:  false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			tt.setup(dir)
			got := detectFrontendOAuth(dir)
			if got != tt.want {
				t.Errorf("detectFrontendOAuth() = %v, want %v", got, tt.want)
			}
		})
	}
}

// ────────────────────────────────────────────────────────────────────────────
// localSourceDirForDeployment
// ────────────────────────────────────────────────────────────────────────────

func TestLocalSourceDirForDeployment(t *testing.T) {
	// Save and restore the global projectDir
	origProjectDir := projectDir
	defer func() { projectDir = origProjectDir }()

	dir := t.TempDir()
	projectDir = dir

	// Create a subdirectory matching the last segment of a deployment name
	os.MkdirAll(filepath.Join(dir, "ui"), 0755)

	tests := []struct {
		name       string
		deployment string
		wantSuffix string // expected suffix of the returned path
	}{
		{"monorepo match", "jeff-vincent-ui", "/ui"},
		{"no subdir match", "jeff-vincent-nonexistent", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := localSourceDirForDeployment(tt.deployment)
			if tt.wantSuffix != "" {
				if !filepath.IsAbs(got) {
					t.Errorf("expected absolute path, got %q", got)
				}
				if got != filepath.Join(dir, tt.wantSuffix[1:]) {
					t.Errorf("localSourceDirForDeployment(%q) = %q, want suffix %q",
						tt.deployment, got, tt.wantSuffix)
				}
			} else {
				// Should fall back to root
				if got != dir {
					t.Errorf("localSourceDirForDeployment(%q) = %q, want %q (root)",
						tt.deployment, got, dir)
				}
			}
		})
	}
}
