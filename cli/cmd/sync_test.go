package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jeffvincent/kindling/cli/core"
)

// ════════════════════════════════════════════════════════════════════
// normalizeProcName
// ════════════════════════════════════════════════════════════════════

func TestNormalizeProcName(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		// Python versioned variants
		{"python3.12", "python3"},
		{"python3.11", "python3"},
		{"python3.9", "python3"},
		{"python3", "python3"},
		{"python2.7", "python"},
		{"python", "python"},

		// Trailing colon (e.g. "nginx: master process")
		{"nginx:", "nginx"},

		// Other runtimes with version suffixes
		{"ruby3.2", "ruby3"},
		{"node18.2", "node18"},

		// Plain names — should pass through unchanged
		{"node", "node"},
		{"gunicorn", "gunicorn"},
		{"java", "java"},
		{"deno", "deno"},
		{"bun", "bun"},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			got := normalizeProcName(tt.in)
			if got != tt.want {
				t.Errorf("normalizeProcName(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

// ════════════════════════════════════════════════════════════════════
// isPythonProc
// ════════════════════════════════════════════════════════════════════

func TestIsPythonProc(t *testing.T) {
	trueCases := []string{
		"python", "python3", "python3.12", "python3.9",
		"python2.7", "python3.11",
	}
	for _, proc := range trueCases {
		if !isPythonProc(proc) {
			t.Errorf("isPythonProc(%q) = false, want true", proc)
		}
	}

	falseCases := []string{
		"node", "uvicorn", "gunicorn", "ruby", "deno", "java",
		"pythonic", "cpython", "",
	}
	for _, proc := range falseCases {
		if isPythonProc(proc) {
			t.Errorf("isPythonProc(%q) = true, want false", proc)
		}
	}
}

// ════════════════════════════════════════════════════════════════════
// matchRuntime
// ════════════════════════════════════════════════════════════════════

func TestMatchRuntime_DirectLookup(t *testing.T) {
	direct := []struct {
		proc string
		want string
	}{
		{"node", "Node.js"},
		{"deno", "Deno"},
		{"bun", "Bun"},
		{"uvicorn", "Python (uvicorn)"},
		{"gunicorn", "Python (gunicorn)"},
		{"ruby", "Ruby"},
		{"puma", "Ruby (Puma)"},
		{"php", "PHP"},
		{"nginx", "Nginx"},
		{"mix", "Elixir (Mix)"},
		{"perl", "Perl"},
		{"lua", "Lua"},
		{"nodemon", "Node.js (nodemon)"},
	}
	for _, tt := range direct {
		t.Run("direct_"+tt.proc, func(t *testing.T) {
			p, ok := matchRuntime(tt.proc, []string{tt.proc})
			if !ok {
				t.Fatalf("matchRuntime(%q) returned false", tt.proc)
			}
			if p.Name != tt.want {
				t.Errorf("matchRuntime(%q).Name = %q, want %q", tt.proc, p.Name, tt.want)
			}
		})
	}
}

func TestMatchRuntime_PythonDashM(t *testing.T) {
	// "python3 -m uvicorn main:app" should match uvicorn
	fields := []string{"python3", "-m", "uvicorn", "main:app"}
	p, ok := matchRuntime("python3", fields)
	if !ok {
		t.Fatal("matchRuntime(python3 -m uvicorn) returned false")
	}
	if p.Name != "Python (uvicorn)" {
		t.Errorf("got Name=%q, want %q", p.Name, "Python (uvicorn)")
	}
	if p.Mode != modeSignal {
		t.Errorf("got Mode=%d, want modeSignal(%d)", p.Mode, modeSignal)
	}

	// "python3 -m gunicorn app:app"
	fields2 := []string{"python3", "-m", "gunicorn", "app:app"}
	p2, ok := matchRuntime("python3", fields2)
	if !ok {
		t.Fatal("matchRuntime(python3 -m gunicorn) returned false")
	}
	if p2.Name != "Python (gunicorn)" {
		t.Errorf("got Name=%q, want %q", p2.Name, "Python (gunicorn)")
	}

	// "python3.12 -m flask run" — versioned python
	fields3 := []string{"python3.12", "-m", "flask", "run"}
	p3, ok := matchRuntime("python3.12", fields3)
	if !ok {
		t.Fatal("matchRuntime(python3.12 -m flask) returned false")
	}
	if p3.Name != "Python (Flask)" {
		t.Errorf("got Name=%q, want %q", p3.Name, "Python (Flask)")
	}
}

func TestMatchRuntime_PythonArgBasename(t *testing.T) {
	// "python3 /usr/local/bin/uvicorn main:app" → should match uvicorn
	fields := []string{"python3", "/usr/local/bin/uvicorn", "main:app"}
	p, ok := matchRuntime("python3", fields)
	if !ok {
		t.Fatal("matchRuntime(python3 /usr/local/bin/uvicorn) returned false")
	}
	if p.Name != "Python (uvicorn)" {
		t.Errorf("got Name=%q, want %q", p.Name, "Python (uvicorn)")
	}
}

func TestMatchRuntime_BundleExec(t *testing.T) {
	// "bundle exec puma -C config/puma.rb"
	fields := []string{"bundle", "exec", "puma", "-C", "config/puma.rb"}
	p, ok := matchRuntime("bundle", fields)
	if !ok {
		t.Fatal("matchRuntime(bundle exec puma) returned false")
	}
	if p.Name != "Ruby (Puma)" {
		t.Errorf("got Name=%q, want %q", p.Name, "Ruby (Puma)")
	}

	// "bundle exec rails server"
	fields2 := []string{"bundle", "exec", "rails", "server"}
	p2, ok := matchRuntime("bundle", fields2)
	if !ok {
		t.Fatal("matchRuntime(bundle exec rails) returned false")
	}
	if p2.Name != "Ruby on Rails" {
		t.Errorf("got Name=%q, want %q", p2.Name, "Ruby on Rails")
	}
}

func TestMatchRuntime_Npx(t *testing.T) {
	// "npx ts-node src/index.ts"
	fields := []string{"npx", "ts-node", "src/index.ts"}
	p, ok := matchRuntime("npx", fields)
	if !ok {
		t.Fatal("matchRuntime(npx ts-node) returned false")
	}
	if p.Name != "TypeScript (ts-node)" {
		t.Errorf("got Name=%q, want %q", p.Name, "TypeScript (ts-node)")
	}

	// "npx nodemon app.js"
	fields2 := []string{"npx", "nodemon", "app.js"}
	p2, ok := matchRuntime("npx", fields2)
	if !ok {
		t.Fatal("matchRuntime(npx nodemon) returned false")
	}
	if p2.Name != "Node.js (nodemon)" {
		t.Errorf("got Name=%q, want %q", p2.Name, "Node.js (nodemon)")
	}
}

func TestMatchRuntime_PhpArtisan(t *testing.T) {
	// "php artisan serve"
	fields := []string{"php", "artisan", "serve"}
	p, ok := matchRuntime("php", fields)
	if !ok {
		t.Fatal("matchRuntime(php artisan serve) returned false")
	}
	if p.Name != "PHP" {
		t.Errorf("got Name=%q, want %q", p.Name, "PHP")
	}
	if p.Mode != modeNone {
		t.Errorf("got Mode=%d, want modeNone(%d)", p.Mode, modeNone)
	}
}

func TestMatchRuntime_Dotnet(t *testing.T) {
	// "dotnet run"
	fields := []string{"dotnet", "run"}
	p, ok := matchRuntime("dotnet", fields)
	if !ok {
		t.Fatal("matchRuntime(dotnet run) returned false")
	}
	if p.Name != ".NET" {
		t.Errorf("got Name=%q, want %q", p.Name, ".NET")
	}
	if p.Mode != modeRebuild {
		t.Errorf("got Mode=%d, want modeRebuild(%d)", p.Mode, modeRebuild)
	}
}

func TestMatchRuntime_NormalizedFallback(t *testing.T) {
	// "python3.12" without -m — should normalize to python3 and match
	fields := []string{"python3.12", "app.py"}
	p, ok := matchRuntime("python3.12", fields)
	if !ok {
		t.Fatal("matchRuntime(python3.12 app.py) returned false")
	}
	if p.Name != "Python 3" {
		t.Errorf("got Name=%q, want %q", p.Name, "Python 3")
	}
}

func TestMatchRuntime_Unknown(t *testing.T) {
	// Completely unknown process
	_, ok := matchRuntime("myapp", []string{"myapp"})
	if ok {
		t.Error("matchRuntime(myapp) should return false for unknown process")
	}

	_, ok = matchRuntime("custom-server", []string{"custom-server", "--port", "8080"})
	if ok {
		t.Error("matchRuntime(custom-server) should return false for unknown process")
	}
}

// ════════════════════════════════════════════════════════════════════
// shouldExclude
// ════════════════════════════════════════════════════════════════════

func TestShouldExclude(t *testing.T) {
	excludes := []string{".git", "node_modules", "*.pyc", "__pycache__", ".DS_Store"}

	tests := []struct {
		relPath string
		want    bool
	}{
		// Direct matches
		{".git", true},
		{"node_modules", true},
		{"__pycache__", true},
		{".DS_Store", true},

		// Wildcard matches
		{"cache.pyc", true},
		{"module.pyc", true},

		// Nested paths — component matching
		{filepath.Join("src", "node_modules", "express"), true},
		{filepath.Join("deep", ".git", "objects"), true},
		{filepath.Join("src", "__pycache__", "mod.cpython-312.pyc"), true},

		// Non-matching paths
		{"src", false},
		{filepath.Join("src", "main.go"), false},
		{filepath.Join("cmd", "root.go"), false},
		{"README.md", false},
		{"package.json", false},
	}
	for _, tt := range tests {
		t.Run(tt.relPath, func(t *testing.T) {
			got := shouldExclude(tt.relPath, excludes)
			if got != tt.want {
				t.Errorf("shouldExclude(%q) = %v, want %v", tt.relPath, got, tt.want)
			}
		})
	}
}

func TestShouldExclude_EmptyExcludes(t *testing.T) {
	if shouldExclude("anything.go", nil) {
		t.Error("shouldExclude should return false with nil excludes")
	}
	if shouldExclude("anything.go", []string{}) {
		t.Error("shouldExclude should return false with empty excludes")
	}
}

func TestShouldExclude_DefaultExcludes(t *testing.T) {
	// Verify the default excludes work as expected
	mustExclude := []string{".git", "node_modules", "__pycache__", ".DS_Store", ".venv", "vendor"}
	for _, pattern := range mustExclude {
		if !shouldExclude(pattern, defaultExcludes) {
			t.Errorf("shouldExclude(%q, defaultExcludes) = false, want true", pattern)
		}
	}

	// Wildcard patterns in defaults
	if !shouldExclude("app.pyc", defaultExcludes) {
		t.Error("shouldExclude(app.pyc, defaultExcludes) should be true")
	}
}

// ════════════════════════════════════════════════════════════════════
// detectLanguageFromSource
// ════════════════════════════════════════════════════════════════════

func TestDetectLanguageFromSource(t *testing.T) {
	tests := []struct {
		name       string
		markerFile string
		wantLang   string
	}{
		{"go", "go.mod", "go"},
		{"rust", "Cargo.toml", "cargo"},
		{"node", "package.json", "node"},
		{"java_maven", "pom.xml", "java"},
		{"java_gradle", "build.gradle", "java"},
		{"kotlin", "build.gradle.kts", "kotlin"},
		{"python_requirements", "requirements.txt", "python3"},
		{"python_setup", "setup.py", "python3"},
		{"python_pyproject", "pyproject.toml", "python3"},
		{"ruby", "Gemfile", "ruby"},
		{"elixir", "mix.exs", "elixir"},
		{"php", "composer.json", "php"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, tt.markerFile), []byte(""), 0644); err != nil {
				t.Fatal(err)
			}
			got := detectLanguageFromSource(dir)
			if got != tt.wantLang {
				t.Errorf("detectLanguageFromSource(%q with %s) = %q, want %q",
					dir, tt.markerFile, got, tt.wantLang)
			}
		})
	}
}

func TestDetectLanguageFromSource_EmptyDir(t *testing.T) {
	dir := t.TempDir()
	got := detectLanguageFromSource(dir)
	if got != "" {
		t.Errorf("detectLanguageFromSource(empty dir) = %q, want empty", got)
	}
}

func TestDetectLanguageFromSource_Priority(t *testing.T) {
	// go.mod should be detected first when multiple markers exist
	dir := t.TempDir()
	for _, f := range []string{"go.mod", "package.json", "requirements.txt"} {
		if err := os.WriteFile(filepath.Join(dir, f), []byte(""), 0644); err != nil {
			t.Fatal(err)
		}
	}
	got := detectLanguageFromSource(dir)
	if got != "go" {
		t.Errorf("detectLanguageFromSource(multi-marker) = %q, want %q (go.mod first)", got, "go")
	}
}

// ════════════════════════════════════════════════════════════════════
// isFrontendProject
// ════════════════════════════════════════════════════════════════════

func TestIsFrontendProject(t *testing.T) {
	t.Run("with_build_script", func(t *testing.T) {
		dir := t.TempDir()
		pkg := map[string]interface{}{
			"name":    "my-app",
			"scripts": map[string]string{"build": "vite build", "dev": "vite"},
		}
		data, _ := json.Marshal(pkg)
		os.WriteFile(filepath.Join(dir, "package.json"), data, 0644)

		if !isFrontendProject(dir) {
			t.Error("isFrontendProject should return true when build script exists")
		}
	})

	t.Run("without_build_script", func(t *testing.T) {
		dir := t.TempDir()
		pkg := map[string]interface{}{
			"name":    "api-server",
			"scripts": map[string]string{"start": "node index.js"},
		}
		data, _ := json.Marshal(pkg)
		os.WriteFile(filepath.Join(dir, "package.json"), data, 0644)

		if isFrontendProject(dir) {
			t.Error("isFrontendProject should return false when no build script")
		}
	})

	t.Run("no_package_json", func(t *testing.T) {
		dir := t.TempDir()
		if isFrontendProject(dir) {
			t.Error("isFrontendProject should return false without package.json")
		}
	})

	t.Run("empty_string", func(t *testing.T) {
		if isFrontendProject("") {
			t.Error("isFrontendProject should return false for empty string")
		}
	})

	t.Run("no_scripts_key", func(t *testing.T) {
		dir := t.TempDir()
		pkg := map[string]interface{}{"name": "bare-pkg"}
		data, _ := json.Marshal(pkg)
		os.WriteFile(filepath.Join(dir, "package.json"), data, 0644)

		if isFrontendProject(dir) {
			t.Error("isFrontendProject should return false when no scripts key")
		}
	})
}

// ════════════════════════════════════════════════════════════════════
// detectPackageManager
// ════════════════════════════════════════════════════════════════════

func TestDetectPackageManager(t *testing.T) {
	t.Run("default_npm", func(t *testing.T) {
		dir := t.TempDir()
		got := detectPackageManager(dir)
		if got != "npm" {
			t.Errorf("detectPackageManager(empty) = %q, want npm", got)
		}
	})

	t.Run("pnpm", func(t *testing.T) {
		dir := t.TempDir()
		os.WriteFile(filepath.Join(dir, "pnpm-lock.yaml"), []byte(""), 0644)
		got := detectPackageManager(dir)
		if got != "pnpm" {
			t.Errorf("detectPackageManager(pnpm) = %q, want pnpm", got)
		}
	})

	t.Run("yarn", func(t *testing.T) {
		dir := t.TempDir()
		os.WriteFile(filepath.Join(dir, "yarn.lock"), []byte(""), 0644)
		got := detectPackageManager(dir)
		if got != "yarn" {
			t.Errorf("detectPackageManager(yarn) = %q, want yarn", got)
		}
	})

	t.Run("pnpm_takes_priority_over_yarn", func(t *testing.T) {
		dir := t.TempDir()
		os.WriteFile(filepath.Join(dir, "pnpm-lock.yaml"), []byte(""), 0644)
		os.WriteFile(filepath.Join(dir, "yarn.lock"), []byte(""), 0644)
		got := detectPackageManager(dir)
		if got != "pnpm" {
			t.Errorf("detectPackageManager(both) = %q, want pnpm (pnpm checked first)", got)
		}
	})
}

// ════════════════════════════════════════════════════════════════════
// detectFrontendOutputDir
// ════════════════════════════════════════════════════════════════════

func TestDetectFrontendOutputDir(t *testing.T) {
	t.Run("vite_ts", func(t *testing.T) {
		dir := t.TempDir()
		os.WriteFile(filepath.Join(dir, "vite.config.ts"), []byte(""), 0644)
		if got := detectFrontendOutputDir(dir); got != "dist" {
			t.Errorf("detectFrontendOutputDir(vite) = %q, want dist", got)
		}
	})

	t.Run("vite_js", func(t *testing.T) {
		dir := t.TempDir()
		os.WriteFile(filepath.Join(dir, "vite.config.js"), []byte(""), 0644)
		if got := detectFrontendOutputDir(dir); got != "dist" {
			t.Errorf("detectFrontendOutputDir(vite.js) = %q, want dist", got)
		}
	})

	t.Run("vite_mts", func(t *testing.T) {
		dir := t.TempDir()
		os.WriteFile(filepath.Join(dir, "vite.config.mts"), []byte(""), 0644)
		if got := detectFrontendOutputDir(dir); got != "dist" {
			t.Errorf("detectFrontendOutputDir(vite.mts) = %q, want dist", got)
		}
	})

	t.Run("nextjs", func(t *testing.T) {
		dir := t.TempDir()
		os.WriteFile(filepath.Join(dir, "next.config.js"), []byte(""), 0644)
		if got := detectFrontendOutputDir(dir); got != "out" {
			t.Errorf("detectFrontendOutputDir(next) = %q, want out", got)
		}
	})

	t.Run("nextjs_mjs", func(t *testing.T) {
		dir := t.TempDir()
		os.WriteFile(filepath.Join(dir, "next.config.mjs"), []byte(""), 0644)
		if got := detectFrontendOutputDir(dir); got != "out" {
			t.Errorf("detectFrontendOutputDir(next.mjs) = %q, want out", got)
		}
	})

	t.Run("angular", func(t *testing.T) {
		dir := t.TempDir()
		os.WriteFile(filepath.Join(dir, "angular.json"), []byte(""), 0644)
		if got := detectFrontendOutputDir(dir); got != "dist" {
			t.Errorf("detectFrontendOutputDir(angular) = %q, want dist", got)
		}
	})

	t.Run("sveltekit", func(t *testing.T) {
		dir := t.TempDir()
		os.WriteFile(filepath.Join(dir, "svelte.config.js"), []byte(""), 0644)
		if got := detectFrontendOutputDir(dir); got != "build" {
			t.Errorf("detectFrontendOutputDir(svelte) = %q, want build", got)
		}
	})

	t.Run("existing_dist_dir", func(t *testing.T) {
		dir := t.TempDir()
		os.Mkdir(filepath.Join(dir, "dist"), 0755)
		if got := detectFrontendOutputDir(dir); got != "dist" {
			t.Errorf("detectFrontendOutputDir(existing dist) = %q, want dist", got)
		}
	})

	t.Run("existing_build_dir", func(t *testing.T) {
		dir := t.TempDir()
		os.Mkdir(filepath.Join(dir, "build"), 0755)
		if got := detectFrontendOutputDir(dir); got != "build" {
			t.Errorf("detectFrontendOutputDir(existing build) = %q, want build", got)
		}
	})

	t.Run("fallback_dist", func(t *testing.T) {
		dir := t.TempDir()
		if got := detectFrontendOutputDir(dir); got != "dist" {
			t.Errorf("detectFrontendOutputDir(empty) = %q, want dist (fallback)", got)
		}
	})
}

// ════════════════════════════════════════════════════════════════════
// extractInnerBinaryFromWrapper
// ════════════════════════════════════════════════════════════════════

func TestExtractInnerBinaryFromWrapper(t *testing.T) {
	t.Run("kindling_wrapper", func(t *testing.T) {
		cmd := `sh -c touch /tmp/.kindling-sync-wrapper && while true; do node server.js & PID=$!; wait $PID; done`
		got := extractInnerBinaryFromWrapper(cmd)
		if got != "node" {
			t.Errorf("extractInnerBinaryFromWrapper(wrapper) = %q, want %q", got, "node")
		}
	})

	t.Run("kindling_wrapper_python", func(t *testing.T) {
		cmd := `sh -c touch /tmp/.kindling-sync-wrapper && while true; do python3 -m uvicorn main:app & PID=$!; wait $PID; done`
		got := extractInnerBinaryFromWrapper(cmd)
		if got != "python3" {
			t.Errorf("extractInnerBinaryFromWrapper(python wrapper) = %q, want %q", got, "python3")
		}
	})

	t.Run("unicode_escaped_ampersand", func(t *testing.T) {
		cmd := `sh -c touch /tmp/.kindling-sync-wrapper \u0026\u0026 while true; do node app.js \u0026 PID=$!; wait $PID; done`
		got := extractInnerBinaryFromWrapper(cmd)
		if got != "node" {
			t.Errorf("extractInnerBinaryFromWrapper(unicode) = %q, want %q", got, "node")
		}
	})

	t.Run("not_a_wrapper", func(t *testing.T) {
		cmd := "node server.js"
		got := extractInnerBinaryFromWrapper(cmd)
		if got != "node" {
			t.Errorf("extractInnerBinaryFromWrapper(plain) = %q, want %q", got, "node")
		}
	})

	t.Run("empty_string", func(t *testing.T) {
		got := extractInnerBinaryFromWrapper("")
		if got != "" {
			t.Errorf("extractInnerBinaryFromWrapper('') = %q, want empty", got)
		}
	})

	t.Run("full_path_binary", func(t *testing.T) {
		cmd := "/usr/local/bin/myapp --port 8080"
		got := extractInnerBinaryFromWrapper(cmd)
		if got != "/usr/local/bin/myapp" {
			t.Errorf("extractInnerBinaryFromWrapper(full path) = %q, want %q", got, "/usr/local/bin/myapp")
		}
	})
}

// ════════════════════════════════════════════════════════════════════
// parseJSONStringArray
// ════════════════════════════════════════════════════════════════════

func TestParseJSONStringArray(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"normal", `["node","server.js"]`, "node server.js"},
		{"three_elements", `["python3","-m","uvicorn","main:app"]`, "python3 -m uvicorn main:app"},
		{"single_element", `["nginx"]`, "nginx"},
		{"empty_array", `[]`, ""},
		{"empty_string", ``, ""},
		{"whitespace", `  `, ""},
		{"with_spaces", `[ "node" , "index.js" ]`, "node index.js"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseJSONStringArray(tt.in)
			if got != tt.want {
				t.Errorf("parseJSONStringArray(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

// ════════════════════════════════════════════════════════════════════
// goarchToRust
// ════════════════════════════════════════════════════════════════════

func TestGoarchToRust(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{"arm64", "aarch64"},
		{"amd64", "x86_64"},
		{"riscv64", "riscv64"},    // passthrough
		{"unknown", "unknown"},    // passthrough
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			got := goarchToRust(tt.in)
			if got != tt.want {
				t.Errorf("goarchToRust(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

// ════════════════════════════════════════════════════════════════════
// goarchToDotnet
// ════════════════════════════════════════════════════════════════════

func TestGoarchToDotnet(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{"arm64", "arm64"},
		{"amd64", "x64"},
		{"386", "386"},         // passthrough
		{"unknown", "unknown"}, // passthrough
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			got := goarchToDotnet(tt.in)
			if got != tt.want {
				t.Errorf("goarchToDotnet(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

// ════════════════════════════════════════════════════════════════════
// runtimeTable completeness
// ════════════════════════════════════════════════════════════════════

func TestRuntimeTable_KeyEntries(t *testing.T) {
	// Verify all expected runtime keys exist
	expectedKeys := []string{
		// Node ecosystem
		"node", "deno", "bun", "nodemon", "ts-node", "tsx",
		// Python ecosystem
		"python", "python3", "uvicorn", "gunicorn", "flask", "celery",
		// Ruby ecosystem
		"ruby", "rails", "puma", "unicorn", "bundle",
		// PHP
		"php", "php-fpm", "apache2",
		// Elixir
		"mix", "elixir", "iex",
		// Scripting
		"perl", "lua", "luajit", "julia",
		// Static servers
		"nginx", "caddy",
		// Compiled
		"go", "java", "kotlin", "dotnet", "cargo", "rustc", "gcc", "zig",
	}
	for _, key := range expectedKeys {
		if _, ok := runtimeTable[key]; !ok {
			t.Errorf("runtimeTable missing key %q", key)
		}
	}
}

func TestRuntimeTable_Modes(t *testing.T) {
	// Verify signal-reload runtimes have signals set
	signalRuntimes := []string{"uvicorn", "gunicorn", "puma", "unicorn", "nginx", "caddy", "apache2"}
	for _, key := range signalRuntimes {
		p := runtimeTable[key]
		if p.Mode != modeSignal {
			t.Errorf("runtimeTable[%q].Mode = %d, want modeSignal(%d)", key, p.Mode, modeSignal)
		}
		if p.Signal == "" {
			t.Errorf("runtimeTable[%q].Signal is empty, should have a signal for modeSignal", key)
		}
	}

	// Verify compiled runtimes
	compiledRuntimes := []string{"go", "java", "kotlin", "dotnet", "cargo", "rustc", "gcc", "zig"}
	for _, key := range compiledRuntimes {
		p := runtimeTable[key]
		if p.Mode != modeRebuild {
			t.Errorf("runtimeTable[%q].Mode = %d, want modeRebuild(%d)", key, p.Mode, modeRebuild)
		}
		if p.Interpreted {
			t.Errorf("runtimeTable[%q].Interpreted should be false for compiled language", key)
		}
	}

	// Verify no-restart runtimes
	noneRuntimes := []string{"php", "php-fpm", "nodemon"}
	for _, key := range noneRuntimes {
		p := runtimeTable[key]
		if p.Mode != modeNone {
			t.Errorf("runtimeTable[%q].Mode = %d, want modeNone(%d)", key, p.Mode, modeNone)
		}
	}

	// Verify interpreted kill-restart runtimes
	killRuntimes := []string{"node", "deno", "bun", "python", "python3", "ruby", "perl", "lua"}
	for _, key := range killRuntimes {
		p := runtimeTable[key]
		if p.Mode != modeKill {
			t.Errorf("runtimeTable[%q].Mode = %d, want modeKill(%d)", key, p.Mode, modeKill)
		}
		if !p.Interpreted {
			t.Errorf("runtimeTable[%q].Interpreted should be true", key)
		}
	}
}

func TestRuntimeTable_GoProfile(t *testing.T) {
	p := runtimeTable["go"]
	if p.Name != "Go" {
		t.Errorf("go profile Name = %q, want %q", p.Name, "Go")
	}
	if p.BuildCmd == "" {
		t.Error("go profile should have a BuildCmd")
	}
	if p.LocalBuildFmt == "" {
		t.Error("go profile should have a LocalBuildFmt")
	}
}

func TestRuntimeTable_AllHaveNames(t *testing.T) {
	for key, p := range runtimeTable {
		if p.Name == "" {
			t.Errorf("runtimeTable[%q].Name is empty", key)
		}
		if p.WaitAfter < 0 {
			t.Errorf("runtimeTable[%q].WaitAfter is negative", key)
		}
	}
}

// ════════════════════════════════════════════════════════════════════
// defaultExcludes completeness
// ════════════════════════════════════════════════════════════════════

func TestDefaultExcludes(t *testing.T) {
	expected := []string{
		".git", "node_modules", ".DS_Store", "__pycache__", "*.pyc",
		".venv", "vendor", "target", "bin", "obj", "dist", ".next", "out",
	}
	excludeSet := map[string]bool{}
	for _, e := range defaultExcludes {
		excludeSet[e] = true
	}
	for _, want := range expected {
		if !excludeSet[want] {
			t.Errorf("defaultExcludes missing %q", want)
		}
	}
}

// ════════════════════════════════════════════════════════════════════
// restartMode constants
// ════════════════════════════════════════════════════════════════════

func TestRestartModeValues(t *testing.T) {
	// Ensure modes are distinct
	modes := []restartMode{modeKill, modeSignal, modeNone, modeRebuild}
	seen := map[restartMode]bool{}
	for _, m := range modes {
		if seen[m] {
			t.Errorf("duplicate restartMode value: %d", m)
		}
		seen[m] = true
	}
}

// ════════════════════════════════════════════════════════════════════
// runtimeProfile struct — verify key fields
// ════════════════════════════════════════════════════════════════════

func TestRuntimeProfileDefaults(t *testing.T) {
	// Verify all WaitAfter values are positive (for runtimes that restart)
	for key, p := range runtimeTable {
		if p.Mode == modeKill || p.Mode == modeSignal || p.Mode == modeRebuild {
			if p.WaitAfter <= 0 {
				t.Errorf("runtimeTable[%q].WaitAfter should be > 0 for mode %d, got %v",
					key, p.Mode, p.WaitAfter)
			}
		}
	}
}

// ════════════════════════════════════════════════════════════════════
// Integration-style tests for matchRuntime + normalizeProcName
// ════════════════════════════════════════════════════════════════════

func TestMatchRuntimeEndToEnd(t *testing.T) {
	// Simulate real-world /proc/1/cmdline scenarios
	tests := []struct {
		name     string
		cmdline  string
		wantName string
		wantMode restartMode
	}{
		{
			"plain_node",
			"node server.js",
			"Node.js",
			modeKill,
		},
		{
			"python3_uvicorn_module",
			"python3 -m uvicorn main:app --host 0.0.0.0",
			"Python (uvicorn)",
			modeSignal,
		},
		{
			"gunicorn_direct",
			"gunicorn app:app -w 4",
			"Python (gunicorn)",
			modeSignal,
		},
		{
			"bundle_exec_puma",
			"bundle exec puma -C config/puma.rb",
			"Ruby (Puma)",
			modeSignal,
		},
		{
			"nginx_master",
			"nginx: master process nginx -g daemon off;",
			// nginx: normalizes to nginx
			"Nginx",
			modeSignal,
		},
		{
			"php_fpm",
			"php-fpm",
			"PHP-FPM",
			modeNone,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fields := strings.Fields(tt.cmdline)
			proc := filepath.Base(fields[0])
			norm := normalizeProcName(proc)

			var p runtimeProfile
			var ok bool

			// Try with original proc first
			p, ok = matchRuntime(proc, fields)
			if !ok {
				// Try normalized
				p, ok = matchRuntime(norm, fields)
			}

			if !ok {
				t.Fatalf("matchRuntime failed for cmdline %q", tt.cmdline)
			}
			if p.Name != tt.wantName {
				t.Errorf("Name = %q, want %q", p.Name, tt.wantName)
			}
			if p.Mode != tt.wantMode {
				t.Errorf("Mode = %d, want %d", p.Mode, tt.wantMode)
			}
		})
	}
}

// ════════════════════════════════════════════════════════════════════
// LoadImageTag (from core/load.go)
// ════════════════════════════════════════════════════════════════════

func TestLoadImageTag(t *testing.T) {
	tag := core.LoadImageTag("orders")

	// Should start with "orders:"
	if !strings.HasPrefix(tag, "orders:") {
		t.Errorf("LoadImageTag(%q) = %q, should start with 'orders:'", "orders", tag)
	}

	// The timestamp part should be a valid unix timestamp
	parts := strings.SplitN(tag, ":", 2)
	if len(parts) != 2 {
		t.Fatalf("LoadImageTag(%q) = %q, expected service:timestamp format", "orders", tag)
	}

	// Parse as int — should be close to current time
	var ts int64
	if _, err := fmt.Sscanf(parts[1], "%d", &ts); err != nil {
		t.Fatalf("timestamp part %q is not a number: %v", parts[1], err)
	}

	now := time.Now().Unix()
	if ts < now-5 || ts > now+5 {
		t.Errorf("timestamp %d is not within 5 seconds of now (%d)", ts, now)
	}
}

func TestLoadImageTag_DifferentServices(t *testing.T) {
	services := []string{"orders", "gateway", "my-service", "frontend"}
	for _, svc := range services {
		tag := core.LoadImageTag(svc)
		if !strings.HasPrefix(tag, svc+":") {
			t.Errorf("LoadImageTag(%q) = %q, should start with %q:", svc, tag, svc)
		}
	}
}

func TestLoadImageTag_UniqueTimestamps(t *testing.T) {
	// Two calls in quick succession should produce the same timestamp
	// (within the same second) but unique-ish tags for different services
	tag1 := core.LoadImageTag("svc-a")
	tag2 := core.LoadImageTag("svc-b")

	if tag1 == tag2 {
		t.Errorf("tags for different services should differ: %q vs %q", tag1, tag2)
	}
}
