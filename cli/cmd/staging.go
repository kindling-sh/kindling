package cmd

import (
	"bufio"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

// ── Staging parent command ───────────────────────────────────

var stagingCmd = &cobra.Command{
	Use:   "staging",
	Short: "Staging cluster utilities (TLS, metrics)",
	Long: `Utilities for managing external staging Kubernetes clusters.

To deploy your app to staging, use 'kindling snapshot --deploy'.

Subcommands:
  tls      Install cert-manager and configure TLS for Ingress resources
  metrics  Install lightweight metrics (VictoriaMetrics + kube-state-metrics)`,
}

func init() {
	rootCmd.AddCommand(stagingCmd)
}

// ── staging tls ──────────────────────────────────────────────

var (
	stagingTLSDomain       string
	stagingTLSContext      string
	stagingTLSEmail        string
	stagingTLSIssuer       string
	stagingTLSUseACMEStaging      bool
	stagingTLSDSEFile      string
	stagingTLSIngressClass string
)

var stagingTLSCmd = &cobra.Command{
	Use:   "tls",
	Short: "Configure TLS with cert-manager for a staging Ingress",
	Long: `Installs cert-manager (if not already present), creates a ClusterIssuer
for Let's Encrypt, and optionally patches a DSE YAML file to enable TLS on
its Ingress.

Examples:
  kindling staging tls --context my-staging --domain app.example.com --email admin@example.com
  kindling staging tls --context my-staging --domain app.example.com --staging
  kindling staging tls --context my-staging --domain app.example.com -f dev-environment.yaml`,
	RunE: runStagingTLS,
}

func init() {
	stagingTLSCmd.Flags().StringVar(&stagingTLSContext, "context", "", "Kubeconfig context for the staging cluster (required)")
	stagingTLSCmd.Flags().StringVar(&stagingTLSDomain, "domain", "", "Domain name for the TLS certificate (required)")
	stagingTLSCmd.Flags().StringVar(&stagingTLSEmail, "email", "", "Email for Let's Encrypt registration (required)")
	stagingTLSCmd.Flags().StringVar(&stagingTLSIssuer, "issuer", "letsencrypt-prod", "ClusterIssuer name")
	stagingTLSCmd.Flags().BoolVar(&stagingTLSUseACMEStaging, "staging", false, "Use Let's Encrypt staging server (for testing)")
	stagingTLSCmd.Flags().StringVarP(&stagingTLSDSEFile, "file", "f", "", "Optional: DSE YAML to patch with TLS config")
	stagingTLSCmd.Flags().StringVar(&stagingTLSIngressClass, "ingress-class", "traefik", "IngressClass for the ACME solver")
	_ = stagingTLSCmd.MarkFlagRequired("context")
	_ = stagingTLSCmd.MarkFlagRequired("domain")
	_ = stagingTLSCmd.MarkFlagRequired("email")
	stagingCmd.AddCommand(stagingTLSCmd)
}

func runStagingTLS(cmd *cobra.Command, args []string) error {
	ctx := stagingTLSContext

	// Safety: refuse Kind contexts
	if strings.HasPrefix(ctx, "kind-") {
		return fmt.Errorf("context %q looks like a Kind cluster — use 'kindling expose' for local dev TLS", ctx)
	}

	header("TLS setup with cert-manager")
	step("🔗", fmt.Sprintf("Target context: %s%s%s", colorBold, ctx, colorReset))

	// ── Install cert-manager ────────────────────────────────────
	step("🔍", "Checking for cert-manager")
	_, err := runSilent("kubectl", "--context", ctx, "get", "namespace", "cert-manager")
	if err != nil {
		step("📦", "Installing cert-manager v1.17.1")
		certManagerURL := "https://github.com/cert-manager/cert-manager/releases/download/v1.17.1/cert-manager.yaml"
		if err := run("kubectl", "--context", ctx, "apply", "-f", certManagerURL); err != nil {
			return fmt.Errorf("cert-manager installation failed: %w", err)
		}

		step("⏳", "Waiting for cert-manager webhook to be ready")
		for i := 0; i < 30; i++ {
			_, err := runSilent("kubectl", "--context", ctx, "-n", "cert-manager",
				"rollout", "status", "deployment/cert-manager-webhook", "--timeout=5s")
			if err == nil {
				break
			}
			time.Sleep(2 * time.Second)
		}
		success("cert-manager installed")
	} else {
		success("cert-manager already installed")
	}

	// ── Create ClusterIssuer ────────────────────────────────────
	acmeServer := "https://acme-v02.api.letsencrypt.org/directory"
	if stagingTLSUseACMEStaging {
		acmeServer = "https://acme-staging-v02.api.letsencrypt.org/directory"
		step("🧪", "Using Let's Encrypt staging server")
	}

	issuerYAML := fmt.Sprintf(`apiVersion: cert-manager.io/v1
kind: ClusterIssuer
metadata:
  name: %s
spec:
  acme:
    server: %s
    email: %s
    privateKeySecretRef:
      name: %s-account-key
    solvers:
    - http01:
        ingress:
          ingressClassName: %s
`, stagingTLSIssuer, acmeServer, stagingTLSEmail, stagingTLSIssuer, stagingTLSIngressClass)

	step("🔐", fmt.Sprintf("Creating ClusterIssuer %q", stagingTLSIssuer))
	if err := runStdin(issuerYAML, "kubectl", "--context", ctx, "apply", "-f", "-"); err != nil {
		return fmt.Errorf("ClusterIssuer creation failed: %w", err)
	}
	success("ClusterIssuer created")

	// ── Optionally patch a DSE file ─────────────────────────────
	if stagingTLSDSEFile != "" {
		step("📝", fmt.Sprintf("Patching %s with TLS config", stagingTLSDSEFile))
		if err := patchDSEWithTLS(stagingTLSDSEFile, stagingTLSDomain, stagingTLSIssuer, stagingTLSIngressClass); err != nil {
			return fmt.Errorf("failed to patch DSE: %w", err)
		}
		success(fmt.Sprintf("Updated %s with TLS config", stagingTLSDSEFile))
		fmt.Println()
		fmt.Fprintf(os.Stderr, "  Deploy with: %skindling snapshot -r <registry> --deploy --context %s%s\n", colorCyan, ctx, colorReset)
	}

	// ── Done ────────────────────────────────────────────────────
	fmt.Println()
	fmt.Fprintf(os.Stderr, "  %s🔒 TLS is configured!%s\n", colorGreen+colorBold, colorReset)
	fmt.Println()
	fmt.Println("  Your Ingress resources will get automatic TLS certificates from Let's Encrypt.")
	fmt.Println()
	fmt.Println("  To enable TLS on a DSE, add this to the ingress spec:")
	fmt.Println()
	fmt.Fprintf(os.Stderr, "    ingress:\n")
	fmt.Fprintf(os.Stderr, "      enabled: true\n")
	fmt.Fprintf(os.Stderr, "      host: %s\n", stagingTLSDomain)
	fmt.Fprintf(os.Stderr, "      ingressClassName: %s\n", stagingTLSIngressClass)
	fmt.Fprintf(os.Stderr, "      annotations:\n")
	fmt.Fprintf(os.Stderr, "        cert-manager.io/cluster-issuer: %s\n", stagingTLSIssuer)
	fmt.Fprintf(os.Stderr, "      tls:\n")
	fmt.Fprintf(os.Stderr, "        secretName: %s-tls\n", strings.ReplaceAll(stagingTLSDomain, ".", "-"))
	fmt.Fprintf(os.Stderr, "        hosts:\n")
	fmt.Fprintf(os.Stderr, "          - %s\n", stagingTLSDomain)
	fmt.Println()

	return nil
}

// patchDSEWithTLS reads a DSE YAML file and adds/updates the ingress TLS section.
func patchDSEWithTLS(yamlFile, domain, issuer, ingressClass string) error {
	data, err := os.ReadFile(yamlFile)
	if err != nil {
		return err
	}

	content := string(data)
	lines := strings.Split(content, "\n")
	var result []string

	secretName := strings.ReplaceAll(domain, ".", "-") + "-tls"
	ingressFound := false
	inTLS := false
	tlsInserted := false

	for i := 0; i < len(lines); i++ {
		line := lines[i]
		trimmed := strings.TrimSpace(line)

		if trimmed == "ingress:" {
			ingressFound = true
		}

		if ingressFound && strings.HasPrefix(trimmed, "enabled:") {
			result = append(result, line)
			hasHost := false
			for j := i + 1; j < len(lines) && j < i+10; j++ {
				if strings.HasPrefix(strings.TrimSpace(lines[j]), "host:") {
					hasHost = true
					break
				}
				if strings.TrimSpace(lines[j]) != "" && !strings.HasPrefix(strings.TrimSpace(lines[j]), " ") {
					break
				}
			}
			if !hasHost {
				indent := strings.Repeat(" ", len(line)-len(strings.TrimLeft(line, " ")))
				result = append(result, indent+"host: "+domain)
			}
			continue
		}

		if ingressFound && strings.HasPrefix(trimmed, "host:") {
			indent := strings.Repeat(" ", len(line)-len(strings.TrimLeft(line, " ")))
			result = append(result, indent+"host: "+domain)
			continue
		}

		if ingressFound && strings.HasPrefix(trimmed, "ingressClassName:") {
			indent := strings.Repeat(" ", len(line)-len(strings.TrimLeft(line, " ")))
			result = append(result, indent+"ingressClassName: "+ingressClass)
			continue
		}

		if ingressFound && trimmed == "tls:" {
			inTLS = true
		}

		if inTLS {
			if trimmed == "tls:" || strings.HasPrefix(trimmed, "secretName:") ||
				strings.HasPrefix(trimmed, "hosts:") || strings.HasPrefix(trimmed, "- ") {
				continue
			}
			inTLS = false
		}

		result = append(result, line)

		if ingressFound && !tlsInserted && (strings.HasPrefix(trimmed, "pathType:") ||
			strings.HasPrefix(trimmed, "path:") || strings.HasPrefix(trimmed, "host:")) {
			nextNonEmpty := ""
			for j := i + 1; j < len(lines); j++ {
				t := strings.TrimSpace(lines[j])
				if t != "" {
					nextNonEmpty = t
					break
				}
			}
			if nextNonEmpty == "annotations:" || nextNonEmpty == "tls:" ||
				strings.HasPrefix(nextNonEmpty, "ingressClassName:") {
				continue
			}

			indent := strings.Repeat(" ", len(line)-len(strings.TrimLeft(line, " ")))
			result = append(result, indent+"ingressClassName: "+ingressClass)
			result = append(result, indent+"annotations:")
			result = append(result, indent+"  cert-manager.io/cluster-issuer: "+issuer)
			result = append(result, indent+"tls:")
			result = append(result, indent+"  secretName: "+secretName)
			result = append(result, indent+"  hosts:")
			result = append(result, indent+"    - "+domain)
			tlsInserted = true
		}
	}

	return os.WriteFile(yamlFile, []byte(strings.Join(result, "\n")), 0644)
}

// confirmPrompt asks the user for Y/n confirmation.
func confirmPrompt(question string) bool {
	fmt.Fprintf(os.Stderr, "  %s (Y/n): ", question)
	scanner := bufio.NewScanner(os.Stdin)
	if scanner.Scan() {
		answer := strings.TrimSpace(strings.ToLower(scanner.Text()))
		return answer == "" || answer == "y" || answer == "yes"
	}
	return false
}
