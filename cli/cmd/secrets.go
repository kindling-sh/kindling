package cmd

import (
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/jeffvincent/kindling/cli/core"
	"github.com/spf13/cobra"
)

const (
	// secretsNamespace is the namespace where kindling user secrets are stored.
	secretsNamespace = "default"
	// secretsDirName is the local config directory for kindling.
	secretsDirName = ".kindling"
	// secretsFileName is the local plaintext secrets mapping file (gitignored).
	secretsFileName = "secrets.yaml"
)

var secretsCmd = &cobra.Command{
	Use:   "secrets",
	Short: "Manage external secrets for your kindling environment",
	Long: `Manage external service credentials (API keys, tokens, connection
strings) that your app needs but that the kindling operator cannot
auto-generate.

Secrets are stored as Kubernetes Secrets in the Kind cluster and
optionally backed up to .kindling/secrets.yaml (gitignored) so they
survive cluster rebuilds.

Examples:
  kindling secrets set STRIPE_API_KEY sk_test_abc123
  kindling secrets set DATABASE_URL "postgres://user:pass@host/db"
  kindling secrets list
  kindling secrets delete STRIPE_API_KEY
  kindling secrets restore`,
}

var secretsSetCmd = &cobra.Command{
	Use:   "set <name> <value>",
	Short: "Create or update an external secret",
	Long: `Creates a Kubernetes Secret in the cluster and saves the mapping to
.kindling/secrets.yaml for persistence across cluster rebuilds.

The secret is stored in the "default" namespace with the label
app.kubernetes.io/managed-by=kindling so it can be discovered and
referenced in generated workflows.

Examples:
  kindling secrets set STRIPE_API_KEY sk_test_abc123
  kindling secrets set AUTH0_CLIENT_SECRET "my-secret-value"`,
	Args: cobra.ExactArgs(2),
	RunE: runSecretsSet,
}

var secretsListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all kindling-managed secrets",
	Long: `Lists the names of all secrets managed by kindling in the cluster.
Values are not displayed for security.`,
	RunE: runSecretsList,
}

var secretsDeleteCmd = &cobra.Command{
	Use:   "delete <name>",
	Short: "Delete an external secret",
	Long: `Removes a secret from both the Kubernetes cluster and the local
.kindling/secrets.yaml backup.

Examples:
  kindling secrets delete STRIPE_API_KEY`,
	Args: cobra.ExactArgs(1),
	RunE: runSecretsDelete,
}

var secretsRestoreCmd = &cobra.Command{
	Use:   "restore",
	Short: "Restore secrets from .kindling/secrets.yaml into the cluster",
	Long: `Re-creates Kubernetes Secrets from the local .kindling/secrets.yaml
file. Use this after recreating a cluster (kindling init) to restore
all your external credentials without re-entering them.

Examples:
  kindling secrets restore`,
	RunE: runSecretsRestore,
}

func init() {
	secretsCmd.AddCommand(secretsSetCmd)
	secretsCmd.AddCommand(secretsListCmd)
	secretsCmd.AddCommand(secretsDeleteCmd)
	secretsCmd.AddCommand(secretsRestoreCmd)
	rootCmd.AddCommand(secretsCmd)
}

// ── Set ─────────────────────────────────────────────────────────

func runSecretsSet(cmd *cobra.Command, args []string) error {
	name := args[0]
	value := args[1]

	header("Setting secret")

	k8sName := core.KindlingSecretName(name)

	step("☸️", fmt.Sprintf("Creating K8s Secret %s in namespace %s", k8sName, secretsNamespace))

	_, err := core.CreateSecret(core.SecretConfig{
		ClusterName: clusterName,
		Name:        name,
		Value:       value,
		Namespace:   secretsNamespace,
	})
	if err != nil {
		return err
	}

	success(fmt.Sprintf("Secret %s created in cluster", k8sName))

	// Persist to local file
	if err := saveSecretLocally(name, value); err != nil {
		warn(fmt.Sprintf("Could not save to local backup: %v", err))
		warn("Secret exists in cluster but won't survive a cluster rebuild")
	} else {
		success(fmt.Sprintf("Backed up to %s/%s", secretsDirName, secretsFileName))
	}

	fmt.Println()
	fmt.Printf("  %sUsage in your app:%s\n", colorBold, colorReset)
	fmt.Printf("    env var:     %s%s%s\n", colorCyan, name, colorReset)
	fmt.Printf("    secret ref:  %s%s%s (key: %s%s%s)\n",
		colorCyan, k8sName, colorReset, colorCyan, name, colorReset)
	fmt.Println()

	return nil
}

// ── List ────────────────────────────────────────────────────────

func runSecretsList(cmd *cobra.Command, args []string) error {
	header("Kindling-managed secrets")

	output, err := core.ListSecrets(clusterName, secretsNamespace)
	if err != nil {
		return fmt.Errorf("failed to list secrets: %w", err)
	}

	if strings.TrimSpace(output) == "" {
		step("📭", "No kindling-managed secrets found")
		fmt.Println()
		fmt.Printf("  Run %skindling secrets set <NAME> <VALUE>%s to add one.\n", colorCyan, colorReset)
		fmt.Println()
		return nil
	}

	// Parse and display nicely
	lines := strings.Split(strings.TrimSpace(output), "\n")
	fmt.Println()
	fmt.Printf("  %-40s %s\n", colorBold+"SECRET"+colorReset, colorBold+"KEYS"+colorReset)
	fmt.Printf("  %-40s %s\n", strings.Repeat("─", 38), strings.Repeat("─", 30))

	for _, line := range lines {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		secretName := fields[0]

		keyNames, _ := core.GetSecretKeys(clusterName, secretName, secretsNamespace)

		fmt.Printf("  %-40s %s\n", secretName, strings.Join(keyNames, ", "))
	}

	fmt.Println()
	return nil
}

// ── Delete ──────────────────────────────────────────────────────

func runSecretsDelete(cmd *cobra.Command, args []string) error {
	name := args[0]
	k8sName := core.KindlingSecretName(name)

	header("Deleting secret")

	step("☸️", fmt.Sprintf("Removing K8s Secret %s", k8sName))
	_, err := core.DeleteSecret(clusterName, name, secretsNamespace)
	if err != nil {
		warn(fmt.Sprintf("Could not delete from cluster: %v", err))
	} else {
		success("Removed from cluster")
	}

	if err := removeSecretLocally(name); err != nil {
		warn(fmt.Sprintf("Could not remove from local backup: %v", err))
	} else {
		success(fmt.Sprintf("Removed from %s/%s", secretsDirName, secretsFileName))
	}

	return nil
}

// ── Restore ─────────────────────────────────────────────────────

func runSecretsRestore(cmd *cobra.Command, args []string) error {
	header("Restoring secrets from local backup")

	secrets, err := loadSecretsLocally()
	if err != nil {
		return fmt.Errorf("could not read %s/%s: %w", secretsDirName, secretsFileName, err)
	}

	if len(secrets) == 0 {
		step("📭", "No secrets found in local backup")
		return nil
	}

	restored := 0
	for name, value := range secrets {
		k8sName := core.KindlingSecretName(name)
		step("☸️", fmt.Sprintf("Restoring %s → %s", name, k8sName))

		_, err := core.CreateSecret(core.SecretConfig{
			ClusterName: clusterName,
			Name:        name,
			Value:       value,
			Namespace:   secretsNamespace,
		})
		if err != nil {
			warn(fmt.Sprintf("Failed to restore %s: %v", name, err))
			continue
		}
		restored++
	}

	success(fmt.Sprintf("Restored %d/%d secrets", restored, len(secrets)))
	return nil
}

// ────────────────────────────────────────────────────────────────────────────
// Helpers
// ────────────────────────────────────────────────────────────────────────────

// ── Local secrets file (plaintext, gitignored) ──────────────────

func secretsFilePath() string {
	dir, _ := resolveProjectDir()
	if dir == "" {
		dir, _ = os.Getwd()
	}
	return filepath.Join(dir, secretsDirName, secretsFileName)
}

// saveSecretLocally appends or updates a secret in .kindling/secrets.yaml.
// Format is simple key: base64(value) pairs.
func saveSecretLocally(name, value string) error {
	secrets, _ := loadSecretsLocally()
	if secrets == nil {
		secrets = make(map[string]string)
	}
	secrets[name] = value

	return writeSecretsFile(secrets)
}

func removeSecretLocally(name string) error {
	secrets, err := loadSecretsLocally()
	if err != nil {
		return err
	}
	delete(secrets, name)
	return writeSecretsFile(secrets)
}

func loadSecretsLocally() (map[string]string, error) {
	path := secretsFilePath()
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return make(map[string]string), nil
		}
		return nil, err
	}

	secrets := make(map[string]string)
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, ": ", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		encoded := strings.TrimSpace(parts[1])
		decoded, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil {
			continue
		}
		secrets[key] = string(decoded)
	}
	return secrets, nil
}

func writeSecretsFile(secrets map[string]string) error {
	path := secretsFilePath()

	// Ensure .kindling/ directory exists
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("cannot create %s: %w", dir, err)
	}

	// Ensure .kindling/ is gitignored
	ensureGitignored(dir)

	var sb strings.Builder
	sb.WriteString("# kindling secrets backup\n")
	sb.WriteString("# This file is gitignored. Do not commit.\n")
	sb.WriteString("# Values are base64-encoded.\n")
	sb.WriteString("# Restore with: kindling secrets restore\n\n")

	for name, value := range secrets {
		encoded := base64.StdEncoding.EncodeToString([]byte(value))
		sb.WriteString(fmt.Sprintf("%s: %s\n", name, encoded))
	}

	return os.WriteFile(path, []byte(sb.String()), 0600)
}

// ensureGitignored makes sure the .kindling directory's secrets.yaml is in .gitignore.
func ensureGitignored(kindlingDir string) {
	projectRoot := filepath.Dir(kindlingDir)
	gitignorePath := filepath.Join(projectRoot, ".gitignore")

	data, err := os.ReadFile(gitignorePath)
	if err != nil && !os.IsNotExist(err) {
		return
	}

	content := string(data)
	pattern := ".kindling/secrets.yaml"

	if strings.Contains(content, pattern) {
		return
	}

	f, err := os.OpenFile(gitignorePath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return
	}
	defer f.Close()

	entry := "\n# kindling secrets (do not commit)\n" + pattern + "\n"
	_, _ = f.WriteString(entry)
}
