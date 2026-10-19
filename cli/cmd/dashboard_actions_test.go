package cmd

import (
	"testing"

	"github.com/jeffvincent/kindling/cli/core"
)

// ────────────────────────────────────────────────────────────────────────────
// runnerPoolConfigFromRequest
// ────────────────────────────────────────────────────────────────────────────

func TestRunnerPoolConfigFromRequest(t *testing.T) {
	tests := []struct {
		name    string
		body    createRunnerRequest
		want    core.RunnerPoolConfig
		wantErr string
	}{
		{
			name: "minimal request -- parity with CLI defaults (no snapshot-deploy, no env, no image override)",
			body: createRunnerRequest{
				Username: "jeff",
				Repo:     "jeff/repo",
				Token:    "ghp_fake",
			},
			want: core.RunnerPoolConfig{
				ClusterName: "dev",
				Username:    "jeff",
				Repo:        "jeff/repo",
				Token:       "ghp_fake",
				Namespace:   "default",
			},
		},
		{
			name: "full request -- snapshot deploy, image override, and buildAgentEnv all map through",
			body: createRunnerRequest{
				Username:             "jeff",
				Repo:                 "jeff/repo",
				Token:                "ghp_fake",
				Provider:             "github",
				EnableSnapshotDeploy: true,
				BuildAgentImage:      "ghcr.io/myorg/build-agent:v2",
				BuildAgentEnv: []createRunnerEnvEntry{
					{Name: "KINDLING_REGISTRY_PASSWORD", SecretName: "registry-credentials", SecretKey: "password"},
					{Name: "KINDLING_REGISTRY_USERNAME", SecretName: "registry-credentials", SecretKey: "username"},
				},
			},
			want: core.RunnerPoolConfig{
				ClusterName:          "dev",
				Username:             "jeff",
				Repo:                 "jeff/repo",
				Token:                "ghp_fake",
				Namespace:            "default",
				Provider:             "github",
				EnableSnapshotDeploy: true,
				BuildAgentImage:      "ghcr.io/myorg/build-agent:v2",
				BuildAgentEnv: []core.BuildAgentEnvVar{
					{Name: "KINDLING_REGISTRY_PASSWORD", SecretName: "registry-credentials", SecretKey: "password"},
					{Name: "KINDLING_REGISTRY_USERNAME", SecretName: "registry-credentials", SecretKey: "username"},
				},
			},
		},
		{
			name:    "missing username",
			body:    createRunnerRequest{Repo: "jeff/repo", Token: "ghp_fake"},
			wantErr: "username, repo, and token are all required",
		},
		{
			name:    "missing repo",
			body:    createRunnerRequest{Username: "jeff", Token: "ghp_fake"},
			wantErr: "username, repo, and token are all required",
		},
		{
			name:    "missing token",
			body:    createRunnerRequest{Username: "jeff", Repo: "jeff/repo"},
			wantErr: "username, repo, and token are all required",
		},
		{
			name: "buildAgentEnv entry missing secretKey",
			body: createRunnerRequest{
				Username: "jeff",
				Repo:     "jeff/repo",
				Token:    "ghp_fake",
				BuildAgentEnv: []createRunnerEnvEntry{
					{Name: "FOO", SecretName: "some-secret"},
				},
			},
			wantErr: "buildAgentEnv[0]: name, secretName, and secretKey are all required",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := runnerPoolConfigFromRequest(tt.body, "dev")
			if tt.wantErr != "" {
				if err == nil || err.Error() != tt.wantErr {
					t.Fatalf("err = %v, want %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got.ClusterName != tt.want.ClusterName || got.Username != tt.want.Username ||
				got.Repo != tt.want.Repo || got.Token != tt.want.Token || got.Namespace != tt.want.Namespace ||
				got.Provider != tt.want.Provider || got.EnableSnapshotDeploy != tt.want.EnableSnapshotDeploy ||
				got.BuildAgentImage != tt.want.BuildAgentImage {
				t.Fatalf("got = %+v, want %+v", got, tt.want)
			}
			if len(got.BuildAgentEnv) != len(tt.want.BuildAgentEnv) {
				t.Fatalf("BuildAgentEnv len = %d, want %d", len(got.BuildAgentEnv), len(tt.want.BuildAgentEnv))
			}
			for i := range got.BuildAgentEnv {
				if got.BuildAgentEnv[i] != tt.want.BuildAgentEnv[i] {
					t.Errorf("BuildAgentEnv[%d] = %+v, want %+v", i, got.BuildAgentEnv[i], tt.want.BuildAgentEnv[i])
				}
			}
		})
	}
}

// ────────────────────────────────────────────────────────────────────────────
// extractQuotedPath
// ────────────────────────────────────────────────────────────────────────────

func TestExtractQuotedPath(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"double quotes", `app.get("/orders", handler)`, "/orders"},
		{"single quotes", `app.get('/orders', handler)`, "/orders"},
		{"with path param", `app.get("/orders/:id", handler)`, "/orders/:id"},
		{"nested path", `router.post("/api/v1/users", handler)`, "/api/v1/users"},
		{"no quotes", `app.get(path, handler)`, ""},
		{"non-path string", `app.get("not-a-path")`, ""},
		{"empty string", "", ""},
		{"path in python decorator", `@app.get("/health")`, "/health"},
		{"go HandleFunc", `http.HandleFunc("/api/items", handler)`, "/api/items"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractQuotedPath(tt.input)
			if got != tt.want {
				t.Errorf("extractQuotedPath(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

// ────────────────────────────────────────────────────────────────────────────
// extractPathParams
// ────────────────────────────────────────────────────────────────────────────

func TestExtractPathParams(t *testing.T) {
	tests := []struct {
		name  string
		path  string
		count int
		names []string
	}{
		{"no params", "/orders", 0, nil},
		{"single param", "/orders/{id}", 1, []string{"id"}},
		{"multiple params", "/users/{user_id}/orders/{order_id}", 2, []string{"user_id", "order_id"}},
		{"root path", "/", 0, nil},
		{"empty", "", 0, nil},
		{"mixed segments", "/api/{version}/items/{id}", 2, []string{"version", "id"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractPathParams(tt.path)
			if len(got) != tt.count {
				t.Fatalf("extractPathParams(%q) returned %d params, want %d", tt.path, len(got), tt.count)
			}
			for i, p := range got {
				if p.Name != tt.names[i] {
					t.Errorf("param[%d].Name = %q, want %q", i, p.Name, tt.names[i])
				}
				if p.In != "path" {
					t.Errorf("param[%d].In = %q, want %q", i, p.In, "path")
				}
				if !p.Required {
					t.Errorf("param[%d].Required = false, want true", i)
				}
			}
		})
	}
}

// ────────────────────────────────────────────────────────────────────────────
// parsePythonRoute
// ────────────────────────────────────────────────────────────────────────────

func TestParsePythonRoute(t *testing.T) {
	tests := []struct {
		name       string
		line       string
		wantMethod string
		wantPath   string
	}{
		{
			name:       "fastapi GET",
			line:       `@app.get("/orders/{order_id}")`,
			wantMethod: "GET",
			wantPath:   "/orders/{order_id}",
		},
		{
			name:       "fastapi POST",
			line:       `@app.post("/items")`,
			wantMethod: "POST",
			wantPath:   "/items",
		},
		{
			name:       "router DELETE",
			line:       `@router.delete("/users/{id}")`,
			wantMethod: "DELETE",
			wantPath:   "/users/{id}",
		},
		{
			name:       "fastapi PUT",
			line:       `@app.put("/items/{item_id}")`,
			wantMethod: "PUT",
			wantPath:   "/items/{item_id}",
		},
		{
			name:       "no match",
			line:       `def get_orders():`,
			wantMethod: "",
			wantPath:   "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m, p := parsePythonRoute(tt.line)
			if m != tt.wantMethod || p != tt.wantPath {
				t.Errorf("parsePythonRoute(%q) = (%q, %q), want (%q, %q)",
					tt.line, m, p, tt.wantMethod, tt.wantPath)
			}
		})
	}
}

// ────────────────────────────────────────────────────────────────────────────
// parseExpressRoute
// ────────────────────────────────────────────────────────────────────────────

func TestParseExpressRoute(t *testing.T) {
	tests := []struct {
		name       string
		line       string
		wantMethod string
		wantPath   string
	}{
		{
			name:       "app.get",
			line:       `app.get('/orders', (req, res) => {`,
			wantMethod: "GET",
			wantPath:   "/orders",
		},
		{
			name:       "router.post",
			line:       `router.post("/items", createItem)`,
			wantMethod: "POST",
			wantPath:   "/items",
		},
		{
			name:       "express param converted",
			line:       `app.get('/orders/:id', handler)`,
			wantMethod: "GET",
			wantPath:   "/orders/{id}",
		},
		{
			name:       "nested params",
			line:       `router.put('/users/:userId/orders/:orderId', handler)`,
			wantMethod: "PUT",
			wantPath:   "/users/{userId}/orders/{orderId}",
		},
		{
			name:       "app.delete",
			line:       `app.delete("/items/:id", handler)`,
			wantMethod: "DELETE",
			wantPath:   "/items/{id}",
		},
		{
			name:       "not a route",
			line:       `const orders = getOrders()`,
			wantMethod: "",
			wantPath:   "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m, p := parseExpressRoute(tt.line)
			if m != tt.wantMethod || p != tt.wantPath {
				t.Errorf("parseExpressRoute(%q) = (%q, %q), want (%q, %q)",
					tt.line, m, p, tt.wantMethod, tt.wantPath)
			}
		})
	}
}

// ────────────────────────────────────────────────────────────────────────────
// parseGoRoute
// ────────────────────────────────────────────────────────────────────────────

func TestParseGoRoute(t *testing.T) {
	tests := []struct {
		name       string
		line       string
		wantMethod string
		wantPath   string
	}{
		{
			name:       "HandleFunc",
			line:       `http.HandleFunc("/api/health", healthHandler)`,
			wantMethod: "GET",
			wantPath:   "/api/health",
		},
		{
			name:       "Handle",
			line:       `mux.Handle("/api/items", itemsHandler)`,
			wantMethod: "GET",
			wantPath:   "/api/items",
		},
		{
			name:       "gin GET",
			line:       `r.GET("/orders", listOrders)`,
			wantMethod: "GET",
			wantPath:   "/orders",
		},
		{
			name:       "gin POST",
			line:       `r.POST("/orders", createOrder)`,
			wantMethod: "POST",
			wantPath:   "/orders",
		},
		{
			name:       "echo DELETE",
			line:       `e.DELETE("/items/:id", deleteItem)`,
			wantMethod: "DELETE",
			wantPath:   "/items/:id",
		},
		{
			name:       "not a route",
			line:       `fmt.Println("hello")`,
			wantMethod: "",
			wantPath:   "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m, p := parseGoRoute(tt.line)
			if m != tt.wantMethod || p != tt.wantPath {
				t.Errorf("parseGoRoute(%q) = (%q, %q), want (%q, %q)",
					tt.line, m, p, tt.wantMethod, tt.wantPath)
			}
		})
	}
}

// ────────────────────────────────────────────────────────────────────────────
// parseGrepRoutes
// ────────────────────────────────────────────────────────────────────────────

func TestParseGrepRoutes(t *testing.T) {
	tests := []struct {
		name      string
		output    string
		framework string
		wantCount int
	}{
		{
			name: "FastAPI routes",
			output: `./main.py:10:@app.get("/health")
./main.py:15:@app.post("/orders")
./main.py:25:@app.get("/orders/{order_id}")`,
			framework: "FastAPI",
			wantCount: 3,
		},
		{
			name: "Express routes",
			output: `./routes/orders.ts:5:router.get('/orders', handler)
./routes/orders.ts:10:router.post('/orders', handler)`,
			framework: "Express",
			wantCount: 2,
		},
		{
			name: "Go routes",
			output: `./main.go:20:http.HandleFunc("/health", healthHandler)
./main.go:21:r.GET("/items", listItems)`,
			framework: "Go",
			wantCount: 2,
		},
		{
			name:      "deduplication",
			output:    "./a.py:1:@app.get(\"/health\")\n./b.py:2:@app.get(\"/health\")",
			framework: "FastAPI",
			wantCount: 1,
		},
		{
			name:      "empty output",
			output:    "",
			framework: "FastAPI",
			wantCount: 0,
		},
		{
			name:      "unknown framework",
			output:    "some output",
			framework: "Unknown",
			wantCount: 0,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseGrepRoutes(tt.output, tt.framework)
			if len(got) != tt.wantCount {
				t.Errorf("parseGrepRoutes() returned %d endpoints, want %d", len(got), tt.wantCount)
			}
		})
	}
}

// ────────────────────────────────────────────────────────────────────────────
// parseGrepRoutes: POST/PUT/PATCH have RequestBody=true
// ────────────────────────────────────────────────────────────────────────────

func TestParseGrepRoutes_RequestBody(t *testing.T) {
	output := `./main.py:1:@app.get("/items")
./main.py:2:@app.post("/items")
./main.py:3:@app.put("/items/{id}")
./main.py:4:@app.patch("/items/{id}")
./main.py:5:@app.delete("/items/{id}")`

	endpoints := parseGrepRoutes(output, "FastAPI")
	if len(endpoints) != 5 {
		t.Fatalf("expected 5 endpoints, got %d", len(endpoints))
	}

	expectations := map[string]bool{
		"GET":    false,
		"POST":   true,
		"PUT":    true,
		"PATCH":  true,
		"DELETE": false,
	}
	for _, ep := range endpoints {
		wantBody := expectations[ep.Method]
		hasBody := ep.RequestBody != nil && ep.RequestBody != false
		if hasBody != wantBody {
			t.Errorf("%s %s: RequestBody = %v, want hasBody=%v",
				ep.Method, ep.Path, ep.RequestBody, wantBody)
		}
	}
}
