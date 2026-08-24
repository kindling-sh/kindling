package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// ════════════════════════════════════════════════════════════════
// loadCredsConfig
// ════════════════════════════════════════════════════════════════

func writeTempCredsConfig(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "creds.yaml")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("cannot write temp creds config: %v", err)
	}
	return path
}

func TestLoadCredsConfig_ValidFile(t *testing.T) {
	t.Setenv("TEST_STAGING_DB_URL", "postgres://staging/real")
	path := writeTempCredsConfig(t, `
credentials:
  DATABASE_URL:
    fromEnv: TEST_STAGING_DB_URL
  FEATURE_FLAG:
    value: "true"
`)

	cfg, err := loadCredsConfig(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.values["DATABASE_URL"] != "postgres://staging/real" {
		t.Errorf("expected fromEnv to resolve, got %q", cfg.values["DATABASE_URL"])
	}
	if cfg.values["FEATURE_FLAG"] != "true" {
		t.Errorf("expected inline value, got %q", cfg.values["FEATURE_FLAG"])
	}
}

func TestLoadCredsConfig_MissingFromEnvVar(t *testing.T) {
	os.Unsetenv("TEST_DOES_NOT_EXIST_XYZ")
	path := writeTempCredsConfig(t, `
credentials:
  DATABASE_URL:
    fromEnv: TEST_DOES_NOT_EXIST_XYZ
`)

	_, err := loadCredsConfig(path)
	if err == nil {
		t.Fatal("expected error for unresolvable fromEnv, got nil")
	}
	if !strings.Contains(err.Error(), "TEST_DOES_NOT_EXIST_XYZ") {
		t.Errorf("expected error to name the missing env var, got: %v", err)
	}
}

func TestLoadCredsConfig_MutuallyExclusiveFields(t *testing.T) {
	t.Setenv("TEST_SOME_VAR", "x")
	path := writeTempCredsConfig(t, `
credentials:
  DATABASE_URL:
    fromEnv: TEST_SOME_VAR
    value: "literal"
`)

	_, err := loadCredsConfig(path)
	if err == nil {
		t.Fatal("expected error when both fromEnv and value are set, got nil")
	}
}

func TestLoadCredsConfig_NeitherFieldSet(t *testing.T) {
	path := writeTempCredsConfig(t, `
credentials:
  DATABASE_URL: {}
`)

	_, err := loadCredsConfig(path)
	if err == nil {
		t.Fatal("expected error when neither fromEnv nor value is set, got nil")
	}
}

func TestLoadCredsConfig_UnknownTopLevelKey(t *testing.T) {
	path := writeTempCredsConfig(t, `
credentials:
  DATABASE_URL:
    value: "x"
unknown_key: true
`)

	_, err := loadCredsConfig(path)
	if err == nil {
		t.Fatal("expected error for unknown top-level key, got nil")
	}
}

func TestLoadCredsConfig_FileNotFound(t *testing.T) {
	_, err := loadCredsConfig("/does/not/exist.yaml")
	if err == nil {
		t.Fatal("expected error for missing file, got nil")
	}
}

// ════════════════════════════════════════════════════════════════
// resolveIngressSelection
// ════════════════════════════════════════════════════════════════

func ingressSelectionCmd(t *testing.T) *cobra.Command {
	t.Helper()
	cmd := &cobra.Command{Use: "snapshot"}
	cmd.Flags().StringVar(&snapshotIngressServices, "ingress-services", "", "")
	return cmd
}

func TestResolveIngressSelection_ExplicitList(t *testing.T) {
	cmd := ingressSelectionCmd(t)
	if err := cmd.Flags().Set("ingress-services", "frontend, api"); err != nil {
		t.Fatalf("cannot set flag: %v", err)
	}
	defer func() { snapshotIngressServices = "" }()

	dses := []snapshotDSE{{Name: "frontend"}, {Name: "api"}, {Name: "worker"}}
	selected, err := resolveIngressSelection(cmd, dses, true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(selected) != 2 || selected[0] != "frontend" || selected[1] != "api" {
		t.Errorf("expected [frontend api], got %v", selected)
	}
}

func TestResolveIngressSelection_ExplicitEmptyDisablesAll(t *testing.T) {
	cmd := ingressSelectionCmd(t)
	if err := cmd.Flags().Set("ingress-services", ""); err != nil {
		t.Fatalf("cannot set flag: %v", err)
	}
	defer func() { snapshotIngressServices = "" }()

	dses := []snapshotDSE{
		{Name: "frontend", Ingress: &snapshotIngress{Enabled: true}},
	}
	selected, err := resolveIngressSelection(cmd, dses, true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(selected) != 0 {
		t.Errorf("expected no services selected, got %v", selected)
	}
}

func TestResolveIngressSelection_UnknownServiceErrors(t *testing.T) {
	cmd := ingressSelectionCmd(t)
	if err := cmd.Flags().Set("ingress-services", "does-not-exist"); err != nil {
		t.Fatalf("cannot set flag: %v", err)
	}
	defer func() { snapshotIngressServices = "" }()

	dses := []snapshotDSE{{Name: "frontend"}}
	_, err := resolveIngressSelection(cmd, dses, true)
	if err == nil {
		t.Fatal("expected error for unknown service name, got nil")
	}
}

func TestResolveIngressSelection_NonInteractiveDefaultsToDevEnabled(t *testing.T) {
	cmd := ingressSelectionCmd(t)
	defer func() { snapshotIngressServices = "" }()

	dses := []snapshotDSE{
		{Name: "frontend", Ingress: &snapshotIngress{Enabled: true}},
		{Name: "worker"},
		{Name: "api", Ingress: &snapshotIngress{Enabled: false}},
	}
	selected, err := resolveIngressSelection(cmd, dses, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(selected) != 1 || selected[0] != "frontend" {
		t.Errorf("expected [frontend], got %v", selected)
	}
}

// ════════════════════════════════════════════════════════════════
// resolveRegistryCredentials
// ════════════════════════════════════════════════════════════════

func TestResolveRegistryCredentials_FromFlagAndEnv(t *testing.T) {
	orig := snapshotRegistryUsername
	defer func() { snapshotRegistryUsername = orig }()
	snapshotRegistryUsername = "myuser"
	t.Setenv("KINDLING_REGISTRY_PASSWORD", "mypass")

	user, pass, err := resolveRegistryCredentials(false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if user != "myuser" || pass != "mypass" {
		t.Errorf("expected myuser/mypass, got %s/%s", user, pass)
	}
}

func TestResolveRegistryCredentials_FromEnvVarUsername(t *testing.T) {
	orig := snapshotRegistryUsername
	defer func() { snapshotRegistryUsername = orig }()
	snapshotRegistryUsername = ""
	t.Setenv("KINDLING_REGISTRY_USERNAME", "envuser")
	t.Setenv("KINDLING_REGISTRY_PASSWORD", "envpass")

	user, pass, err := resolveRegistryCredentials(false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if user != "envuser" || pass != "envpass" {
		t.Errorf("expected envuser/envpass, got %s/%s", user, pass)
	}
}

func TestResolveRegistryCredentials_NonInteractiveMissingFailsFast(t *testing.T) {
	orig := snapshotRegistryUsername
	defer func() { snapshotRegistryUsername = orig }()
	snapshotRegistryUsername = ""
	os.Unsetenv("KINDLING_REGISTRY_USERNAME")
	os.Unsetenv("KINDLING_REGISTRY_PASSWORD")

	_, _, err := resolveRegistryCredentials(false)
	if err == nil {
		t.Fatal("expected error in non-interactive mode with no credentials resolvable, got nil")
	}
}

// ════════════════════════════════════════════════════════════════
// writeMissingCredentialsFile
// ════════════════════════════════════════════════════════════════

func TestWriteMissingCredentialsFile(t *testing.T) {
	dir := t.TempDir()
	missing := []missingCredential{
		{EnvVarName: "DATABASE_URL", Services: []string{"checkout", "orders"}},
	}

	path, err := writeMissingCredentialsFile(dir, missing)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("cannot read written file: %v", err)
	}
	content := string(data)
	if !strings.Contains(content, "DATABASE_URL") {
		t.Errorf("expected file to mention DATABASE_URL, got: %s", content)
	}
	if !strings.Contains(content, "checkout") || !strings.Contains(content, "orders") {
		t.Errorf("expected file to list affected services, got: %s", content)
	}
}
