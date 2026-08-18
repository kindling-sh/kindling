package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ════════════════════════════════════════════════════════════════
// Credential detection
// ════════════════════════════════════════════════════════════════

func TestDetectDevCredentials_Basic(t *testing.T) {
	dses := []snapshotDSE{
		{
			Name: "orders",
			Deps: []snapshotDep{{Type: "postgres"}, {Type: "redis"}},
		},
		{
			Name: "inventory",
			Deps: []snapshotDep{{Type: "mongodb"}},
		},
	}

	entries := detectDevCredentials("kindling-snapshot", dses)

	if len(entries) != 3 {
		t.Fatalf("expected 3 credential entries, got %d", len(entries))
	}

	// Check each entry has the right env var name
	envVars := make(map[string]bool)
	for _, e := range entries {
		envVars[e.EnvVarName] = true
	}
	for _, expected := range []string{"DATABASE_URL", "REDIS_URL", "MONGO_URL"} {
		if !envVars[expected] {
			t.Errorf("expected %s in detected credentials, not found", expected)
		}
	}
}

func TestDetectDevCredentials_DeduplicatesByDepType(t *testing.T) {
	// Two services using the same dependency type → single entry
	dses := []snapshotDSE{
		{Name: "api", Deps: []snapshotDep{{Type: "postgres"}}},
		{Name: "worker", Deps: []snapshotDep{{Type: "postgres"}}},
	}

	entries := detectDevCredentials("test", dses)

	if len(entries) != 1 {
		t.Fatalf("expected 1 deduplicated entry, got %d", len(entries))
	}
	if len(entries[0].Services) != 2 {
		t.Errorf("expected 2 services listed, got %d", len(entries[0].Services))
	}
	if entries[0].Services[0] != "api" || entries[0].Services[1] != "worker" {
		t.Errorf("expected services [api, worker], got %v", entries[0].Services)
	}
}

func TestDetectDevCredentials_NoDeps(t *testing.T) {
	dses := []snapshotDSE{
		{Name: "ui", Deps: nil},
	}

	entries := detectDevCredentials("test", dses)
	if len(entries) != 0 {
		t.Errorf("expected 0 entries for service with no deps, got %d", len(entries))
	}
}

func TestDetectDevCredentials_EmptyDSEs(t *testing.T) {
	entries := detectDevCredentials("test", nil)
	if len(entries) != 0 {
		t.Errorf("expected 0 entries for nil DSEs, got %d", len(entries))
	}
}

func TestDetectDevCredentials_UnknownDepType(t *testing.T) {
	dses := []snapshotDSE{
		{Name: "svc", Deps: []snapshotDep{{Type: "custom-thing"}}},
	}

	entries := detectDevCredentials("test", dses)
	if len(entries) != 0 {
		t.Errorf("expected 0 entries for unknown dep type, got %d", len(entries))
	}
}

func TestDetectDevCredentials_DevValueContainsChart(t *testing.T) {
	dses := []snapshotDSE{
		{Name: "api", Deps: []snapshotDep{{Type: "postgres"}}},
	}

	entries := detectDevCredentials("my-chart", dses)

	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	// The dev value should reference the chart name as the release prefix
	if !strings.Contains(entries[0].DevValue, "my-chart-postgres") {
		t.Errorf("dev value should contain chart-prefixed host, got: %s", entries[0].DevValue)
	}
}

func TestDetectDevCredentials_AllSupportedTypes(t *testing.T) {
	// One service with every supported dependency type
	var deps []snapshotDep
	for depType := range depRegistry {
		deps = append(deps, snapshotDep{Type: depType})
	}
	dses := []snapshotDSE{{Name: "mega-svc", Deps: deps}}

	entries := detectDevCredentials("test", dses)
	if len(entries) != len(depRegistry) {
		t.Errorf("expected %d entries (one per supported dep), got %d", len(depRegistry), len(entries))
	}
}

func TestDetectDevCredentials_PreservesOrder(t *testing.T) {
	dses := []snapshotDSE{
		{Name: "svc", Deps: []snapshotDep{
			{Type: "redis"},
			{Type: "postgres"},
			{Type: "mongodb"},
		}},
	}

	entries := detectDevCredentials("test", dses)

	if len(entries) != 3 {
		t.Fatalf("expected 3 entries, got %d", len(entries))
	}
	// Order should match input order
	expected := []string{"redis", "postgres", "mongodb"}
	for i, e := range entries {
		if e.DepType != expected[i] {
			t.Errorf("entry[%d]: expected dep type %q, got %q", i, expected[i], e.DepType)
		}
	}
}

// ════════════════════════════════════════════════════════════════
// Encryption round-trip
// ════════════════════════════════════════════════════════════════

func TestEncryptDecryptRoundTrip(t *testing.T) {
	plaintext := []byte(`{"context":"test","creds":{"DATABASE_URL":"postgres://prod:secret@db:5432/app"}}`)

	encrypted, err := encryptBytes(plaintext)
	if err != nil {
		t.Fatalf("encryptBytes failed: %v", err)
	}

	// Encrypted output should be different from plaintext
	if string(encrypted) == string(plaintext) {
		t.Error("encrypted output should differ from plaintext")
	}

	// Should be longer (nonce + tag overhead)
	if len(encrypted) <= len(plaintext) {
		t.Error("encrypted output should be longer than plaintext")
	}

	decrypted, err := decryptBytes(encrypted)
	if err != nil {
		t.Fatalf("decryptBytes failed: %v", err)
	}

	if string(decrypted) != string(plaintext) {
		t.Errorf("round-trip failed: got %q, want %q", string(decrypted), string(plaintext))
	}
}

func TestDecryptBytes_CorruptedData(t *testing.T) {
	_, err := decryptBytes([]byte("too-short"))
	if err == nil {
		t.Error("expected error for corrupted data, got nil")
	}
}

func TestDecryptBytes_TamperedData(t *testing.T) {
	encrypted, err := encryptBytes([]byte("test data"))
	if err != nil {
		t.Fatalf("encryptBytes failed: %v", err)
	}

	// Tamper with the ciphertext
	encrypted[len(encrypted)-1] ^= 0xFF

	_, err = decryptBytes(encrypted)
	if err == nil {
		t.Error("expected error for tampered data, got nil")
	}
}

// ════════════════════════════════════════════════════════════════
// Credential cache save/load round-trip
// ════════════════════════════════════════════════════════════════

func TestCredCacheRoundTrip(t *testing.T) {
	// Use a temporary directory to avoid touching the real cache
	origHome := os.Getenv("HOME")
	tmpDir := t.TempDir()
	os.Setenv("HOME", tmpDir)
	defer os.Setenv("HOME", origHome)

	ctx := "test-context-abc"
	chart := "kindling-snapshot"
	creds := map[string]string{
		"DATABASE_URL": "postgres://prod:secret@db.example.com:5432/app",
		"REDIS_URL":    "redis://prod-redis.example.com:6379/0",
	}

	// Save
	err := saveCredCache(ctx, chart, creds)
	if err != nil {
		t.Fatalf("saveCredCache failed: %v", err)
	}

	// Verify file exists with correct permissions
	cacheFile := credsCacheFile(ctx)
	info, err := os.Stat(cacheFile)
	if err != nil {
		t.Fatalf("cache file not found: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0600 {
		t.Errorf("cache file permissions = %o, want 0600", perm)
	}

	// Verify directory permissions
	dirInfo, _ := os.Stat(filepath.Dir(cacheFile))
	if perm := dirInfo.Mode().Perm(); perm != 0700 {
		t.Errorf("cache dir permissions = %o, want 0700", perm)
	}

	// Load
	loaded := loadCredCache(ctx)
	if loaded == nil {
		t.Fatal("loadCredCache returned nil")
	}
	if loaded.Context != ctx {
		t.Errorf("context = %q, want %q", loaded.Context, ctx)
	}
	if loaded.Chart != chart {
		t.Errorf("chart = %q, want %q", loaded.Chart, chart)
	}
	if len(loaded.Creds) != 2 {
		t.Errorf("expected 2 cached creds, got %d", len(loaded.Creds))
	}
	if loaded.Creds["DATABASE_URL"] != creds["DATABASE_URL"] {
		t.Errorf("DATABASE_URL = %q, want %q", loaded.Creds["DATABASE_URL"], creds["DATABASE_URL"])
	}
	if loaded.Creds["REDIS_URL"] != creds["REDIS_URL"] {
		t.Errorf("REDIS_URL = %q, want %q", loaded.Creds["REDIS_URL"], creds["REDIS_URL"])
	}
}

func TestLoadCredCache_Missing(t *testing.T) {
	origHome := os.Getenv("HOME")
	os.Setenv("HOME", t.TempDir())
	defer os.Setenv("HOME", origHome)

	result := loadCredCache("nonexistent-context")
	if result != nil {
		t.Error("expected nil for missing cache, got non-nil")
	}
}

func TestLoadCredCache_CorruptedFile(t *testing.T) {
	origHome := os.Getenv("HOME")
	tmpDir := t.TempDir()
	os.Setenv("HOME", tmpDir)
	defer os.Setenv("HOME", origHome)

	ctx := "corrupt-test"
	cacheFile := credsCacheFile(ctx)
	os.MkdirAll(filepath.Dir(cacheFile), 0700)
	os.WriteFile(cacheFile, []byte("not-valid-encrypted-data-at-all"), 0600)

	result := loadCredCache(ctx)
	if result != nil {
		t.Error("expected nil for corrupted cache file, got non-nil")
	}
}

func TestClearCredCache(t *testing.T) {
	origHome := os.Getenv("HOME")
	tmpDir := t.TempDir()
	os.Setenv("HOME", tmpDir)
	defer os.Setenv("HOME", origHome)

	ctx := "clear-test"
	saveCredCache(ctx, "chart", map[string]string{"KEY": "val"})

	// Verify it exists
	if loadCredCache(ctx) == nil {
		t.Fatal("cache should exist before clearing")
	}

	clearCredCache(ctx)

	if loadCredCache(ctx) != nil {
		t.Error("cache should be nil after clearing")
	}
}

// ════════════════════════════════════════════════════════════════
// buildOverrideMap
// ════════════════════════════════════════════════════════════════

func TestBuildOverrideMap_Basic(t *testing.T) {
	entries := []stagingCredEntry{
		{DepType: "postgres", EnvVarName: "DATABASE_URL", Services: []string{"orders", "api"}},
		{DepType: "redis", EnvVarName: "REDIS_URL", Services: []string{"orders"}},
	}
	dses := []snapshotDSE{
		{Name: "orders", Deps: []snapshotDep{{Type: "postgres"}, {Type: "redis"}}},
		{Name: "api", Deps: []snapshotDep{{Type: "postgres"}}},
	}
	creds := map[string]string{
		"DATABASE_URL": "postgres://prod:secret@db:5432/app",
		"REDIS_URL":    "redis://prod-redis:6379/0",
	}

	result := buildOverrideMap(entries, dses, creds)

	// orders should have both DATABASE_URL and REDIS_URL
	ordersKey := helmValuesKey("orders")
	if result[ordersKey] == nil {
		t.Fatalf("expected overrides for %q", ordersKey)
	}
	if result[ordersKey]["DATABASE_URL"].Value != creds["DATABASE_URL"] {
		t.Errorf("orders DATABASE_URL = %q, want %q", result[ordersKey]["DATABASE_URL"].Value, creds["DATABASE_URL"])
	}
	if result[ordersKey]["REDIS_URL"].Value != creds["REDIS_URL"] {
		t.Errorf("orders REDIS_URL = %q, want %q", result[ordersKey]["REDIS_URL"].Value, creds["REDIS_URL"])
	}

	// api should have only DATABASE_URL
	apiKey := helmValuesKey("api")
	if result[apiKey] == nil {
		t.Fatalf("expected overrides for %q", apiKey)
	}
	if result[apiKey]["DATABASE_URL"].Value != creds["DATABASE_URL"] {
		t.Errorf("api DATABASE_URL = %q, want %q", result[apiKey]["DATABASE_URL"].Value, creds["DATABASE_URL"])
	}
	if _, ok := result[apiKey]["REDIS_URL"]; ok {
		t.Error("api should not have REDIS_URL override")
	}
}

func TestBuildOverrideMap_EmptyCreds(t *testing.T) {
	result := buildOverrideMap(
		[]stagingCredEntry{{DepType: "postgres", EnvVarName: "DATABASE_URL"}},
		[]snapshotDSE{{Name: "api", Deps: []snapshotDep{{Type: "postgres"}}}},
		map[string]string{},
	)
	if result != nil {
		t.Error("expected nil for empty creds, got non-nil")
	}
}

func TestBuildOverrideMap_NilCreds(t *testing.T) {
	result := buildOverrideMap(
		[]stagingCredEntry{{DepType: "postgres", EnvVarName: "DATABASE_URL"}},
		[]snapshotDSE{{Name: "api", Deps: []snapshotDep{{Type: "postgres"}}}},
		nil,
	)
	if result != nil {
		t.Error("expected nil for nil creds, got non-nil")
	}
}

func TestBuildOverrideMap_PartialCreds(t *testing.T) {
	entries := []stagingCredEntry{
		{DepType: "postgres", EnvVarName: "DATABASE_URL", Services: []string{"api"}},
		{DepType: "redis", EnvVarName: "REDIS_URL", Services: []string{"api"}},
	}
	dses := []snapshotDSE{
		{Name: "api", Deps: []snapshotDep{{Type: "postgres"}, {Type: "redis"}}},
	}
	// Only provide DATABASE_URL, leave REDIS_URL blank
	creds := map[string]string{
		"DATABASE_URL": "postgres://prod@db:5432/app",
	}

	result := buildOverrideMap(entries, dses, creds)

	apiKey := helmValuesKey("api")
	if result[apiKey]["DATABASE_URL"].Value != creds["DATABASE_URL"] {
		t.Errorf("expected DATABASE_URL override")
	}
	if _, ok := result[apiKey]["REDIS_URL"]; ok {
		t.Error("REDIS_URL should not be in overrides when not provided")
	}
}

// ════════════════════════════════════════════════════════════════
// writeCredsOverrideFile
// ════════════════════════════════════════════════════════════════

func TestWriteCredsOverrideFile_Basic(t *testing.T) {
	overrides := map[string]map[string]credOverride{
		"orders": {
			"DATABASE_URL": {Value: "postgres://prod:secret@db:5432/app", IsSecret: false},
			"REDIS_URL":    {Value: "redis://prod-redis:6379/0", IsSecret: false},
		},
		"inventory": {
			"MONGO_URL": {Value: "mongodb://prod:secret@mongo:27017", IsSecret: false},
		},
	}

	path, err := writeCredsOverrideFile(overrides)
	if err != nil {
		t.Fatalf("writeCredsOverrideFile failed: %v", err)
	}
	defer os.Remove(path)

	// Check file exists
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("file not found: %v", err)
	}

	// Check permissions
	if perm := info.Mode().Perm(); perm != 0600 {
		t.Errorf("file permissions = %o, want 0600", perm)
	}

	// Read and verify content
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("cannot read file: %v", err)
	}
	s := string(content)

	if !strings.Contains(s, "orders:") {
		t.Error("file should contain orders key")
	}
	if !strings.Contains(s, "inventory:") {
		t.Error("file should contain inventory key")
	}
	if !strings.Contains(s, "DATABASE_URL:") {
		t.Error("file should contain DATABASE_URL")
	}
	if !strings.Contains(s, "MONGO_URL:") {
		t.Error("file should contain MONGO_URL")
	}
	if !strings.Contains(s, "env:") {
		t.Error("file should contain env: nesting")
	}
}

func TestWriteCredsOverrideFile_SpecialChars(t *testing.T) {
	overrides := map[string]map[string]credOverride{
		"api": {
			"DATABASE_URL": {Value: `postgres://user:p@ss"word@db:5432/app?sslmode=require`, IsSecret: false},
		},
	}

	path, err := writeCredsOverrideFile(overrides)
	if err != nil {
		t.Fatalf("writeCredsOverrideFile failed: %v", err)
	}
	defer os.Remove(path)

	content, _ := os.ReadFile(path)
	s := string(content)

	// Quotes in values should be escaped
	if !strings.Contains(s, `\"`) {
		t.Error("internal quotes should be escaped")
	}
}

// ════════════════════════════════════════════════════════════════
// detectUserSecrets
// ════════════════════════════════════════════════════════════════

func TestDetectUserSecrets_Basic(t *testing.T) {
	dses := []snapshotDSE{
		{
			Name: "gateway",
			Env: []snapshotEnvVar{
				{Name: "AUTH0_DOMAIN", Value: "dev.auth0.com", IsSecret: true},
				{Name: "AUTH0_CLIENT_ID", Value: "abc123", IsSecret: true},
				{Name: "ORDERS_URL", Value: "http://orders:5000", IsSecret: false},
			},
		},
	}

	entries := detectUserSecrets(dses)
	if len(entries) != 2 {
		t.Fatalf("expected 2 secret entries, got %d", len(entries))
	}
	if entries[0].EnvVarName != "AUTH0_DOMAIN" {
		t.Errorf("first entry should be AUTH0_DOMAIN, got %s", entries[0].EnvVarName)
	}
	if entries[0].DepType != "secret" {
		t.Errorf("dep type should be 'secret', got %s", entries[0].DepType)
	}
	if entries[0].DevValue != "dev.auth0.com" {
		t.Errorf("dev value should be carried over, got %s", entries[0].DevValue)
	}
}

func TestDetectUserSecrets_NoSecrets(t *testing.T) {
	dses := []snapshotDSE{
		{Name: "ui", Env: []snapshotEnvVar{{Name: "FOO", Value: "bar"}}},
	}
	entries := detectUserSecrets(dses)
	if len(entries) != 0 {
		t.Errorf("expected 0 entries, got %d", len(entries))
	}
}

func TestDetectUserSecrets_DeduplicatesAcrossServices(t *testing.T) {
	dses := []snapshotDSE{
		{Name: "gateway", Env: []snapshotEnvVar{{Name: "SECRET_KEY", Value: "v1", IsSecret: true}}},
		{Name: "api", Env: []snapshotEnvVar{{Name: "SECRET_KEY", Value: "v1", IsSecret: true}}},
	}
	entries := detectUserSecrets(dses)
	if len(entries) != 1 {
		t.Fatalf("expected 1 deduplicated entry, got %d", len(entries))
	}
	if len(entries[0].Services) != 2 {
		t.Errorf("expected 2 services, got %d", len(entries[0].Services))
	}
}

// ════════════════════════════════════════════════════════════════
// buildOverrideMap with secrets
// ════════════════════════════════════════════════════════════════

func TestBuildOverrideMap_WithSecrets(t *testing.T) {
	entries := []stagingCredEntry{
		{DepType: "postgres", EnvVarName: "DATABASE_URL", Services: []string{"gateway"}},
		{DepType: "secret", EnvVarName: "AUTH0_DOMAIN", Services: []string{"gateway"}},
	}
	dses := []snapshotDSE{
		{
			Name: "gateway",
			Deps: []snapshotDep{{Type: "postgres"}},
			Env:  []snapshotEnvVar{{Name: "AUTH0_DOMAIN", Value: "dev.auth0.com", IsSecret: true}},
		},
	}
	creds := map[string]string{
		"DATABASE_URL": "postgres://prod@db:5432/app",
		"AUTH0_DOMAIN": "prod.auth0.com",
	}

	result := buildOverrideMap(entries, dses, creds)

	vk := helmValuesKey("gateway")
	if result[vk]["DATABASE_URL"].IsSecret {
		t.Error("DATABASE_URL should not be marked as secret")
	}
	if !result[vk]["AUTH0_DOMAIN"].IsSecret {
		t.Error("AUTH0_DOMAIN should be marked as secret")
	}
	if result[vk]["AUTH0_DOMAIN"].Value != "prod.auth0.com" {
		t.Errorf("AUTH0_DOMAIN value = %q, want %q", result[vk]["AUTH0_DOMAIN"].Value, "prod.auth0.com")
	}
}

// ════════════════════════════════════════════════════════════════
// writeCredsOverrideFile with secrets section
// ════════════════════════════════════════════════════════════════

func TestWriteCredsOverrideFile_SecretsSection(t *testing.T) {
	overrides := map[string]map[string]credOverride{
		"gateway": {
			"DATABASE_URL": {Value: "postgres://prod@db:5432/app", IsSecret: false},
			"AUTH0_DOMAIN": {Value: "prod.auth0.com", IsSecret: true},
		},
	}
	path, err := writeCredsOverrideFile(overrides)
	if err != nil {
		t.Fatalf("writeCredsOverrideFile failed: %v", err)
	}
	defer os.Remove(path)

	content, _ := os.ReadFile(path)
	s := string(content)

	if !strings.Contains(s, "env:") {
		t.Error("should contain env: section for DATABASE_URL")
	}
	if !strings.Contains(s, "secrets:") {
		t.Error("should contain secrets: section for AUTH0_DOMAIN")
	}
}

// ════════════════════════════════════════════════════════════════
// Helper functions
// ════════════════════════════════════════════════════════════════

func TestTruncateStr(t *testing.T) {
	tests := []struct {
		input  string
		maxLen int
		expect string
	}{
		{"short", 10, "short"},
		{"exactly10!", 10, "exactly10!"},
		{"this is a long string", 10, "this is a…"},
		{"", 5, ""},
		{"a", 1, "a"},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := truncateStr(tt.input, tt.maxLen)
			if got != tt.expect {
				t.Errorf("truncateStr(%q, %d) = %q, want %q", tt.input, tt.maxLen, got, tt.expect)
			}
		})
	}
}

func TestPluralize(t *testing.T) {
	tests := []struct {
		n      int
		s, p   string
		expect string
	}{
		{0, "credential", "credentials", "credentials"},
		{1, "credential", "credentials", "credential"},
		{2, "credential", "credentials", "credentials"},
		{5, "dependency", "dependencies", "dependencies"},
	}
	for _, tt := range tests {
		got := pluralize(tt.n, tt.s, tt.p)
		if got != tt.expect {
			t.Errorf("pluralize(%d, %q, %q) = %q, want %q", tt.n, tt.s, tt.p, got, tt.expect)
		}
	}
}

func TestCredsCacheFile_DifferentContexts(t *testing.T) {
	file1 := credsCacheFile("context-a")
	file2 := credsCacheFile("context-b")

	if file1 == file2 {
		t.Error("different contexts should produce different cache file paths")
	}

	// Same context should produce same path
	file1a := credsCacheFile("context-a")
	if file1 != file1a {
		t.Error("same context should produce same cache file path")
	}
}

func TestDeriveEncryptionKey_Stable(t *testing.T) {
	key1 := deriveEncryptionKey()
	key2 := deriveEncryptionKey()

	if len(key1) != 32 {
		t.Errorf("expected 32-byte key, got %d bytes", len(key1))
	}
	if string(key1) != string(key2) {
		t.Error("key derivation should be deterministic")
	}
}
