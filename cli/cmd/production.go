package cmd

import (
	"bufio"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

// ── Production parent command ───────────────────────────────────

var productionCmd = &cobra.Command{
	Use:   "production",
	Short: "Production cluster utilities (TLS, metrics)",
	Long: `Utilities for managing production Kubernetes clusters.

To deploy your app to production, use 'kindling snapshot --deploy'.

Subcommands:
  tls      Install cert-manager and configure TLS for Ingress resources
  metrics  Install lightweight metrics (VictoriaMetrics + kube-state-metrics)`,
}

func init() {
	rootCmd.AddCommand(productionCmd)
}

// ── production tls ──────────────────────────────────────────────

var (
	prodTLSDomain       string
	prodTLSContext      string
	prodTLSEmail        string
	prodTLSIssuer       string
	prodTLSStaging      bool
	prodTLSDSEFile      string
	prodTLSIngressClass string
)

var productionTLSCmd = &cobra.Command{
	Use:   "tls",
	Short: "Configure TLS with cert-manager for production Ingress",
	Long: `Installs cert-manager (if not already present), creates a ClusterIssuer
for Let's Encrypt, and optionally patches a DSE YAML file to enable TLS on
its Ingress.

Examples:
  kindling production tls --context my-prod --domain app.example.com --email admin@example.com
  kindling production tls --context my-prod --domain app.example.com --staging
  kindling production tls --context my-prod --domain app.example.com -f dev-environment.yaml`,
	RunE: runProductionTLS,
}

func init() {
	productionTLSCmd.Flags().StringVar(&prodTLSContext, "context", "", "Kubeconfig context for the production cluster (required)")
	productionTLSCmd.Flags().StringVar(&prodTLSDomain, "domain", "", "Domain name for the TLS certificate (required)")
	productionTLSCmd.Flags().StringVar(&prodTLSEmail, "email", "", "Email for Let's Encrypt registration (required)")
	productionTLSCmd.Flags().StringVar(&prodTLSIssuer, "issuer", "letsencrypt-prod", "ClusterIssuer name")
	productionTLSCmd.Flags().BoolVar(&prodTLSStaging, "staging", false, "Use Let's Encrypt staging server (for testing)")
	productionTLSCmd.Flags().StringVarP(&prodTLSDSEFile, "file", "f", "", "Optional: DSE YAML to patch with TLS config")
	productionTLSCmd.Flags().StringVar(&prodTLSIngressClass, "ingress-class", "traefik", "IngressClass for the ACME solver")
	_ = productionTLSCmd.MarkFlagRequired("context")
	_ = productionTLSCmd.MarkFlagRequired("domain")
	_ = productionTLSCmd.MarkFlagRequired("email")
	productionCmd.AddCommand(productionTLSCmd)
}

func runProductionTLS(cmd *cobra.Command, args []string) error {
	ctx := prodTLSContext

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
	if prodTLSStaging {
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
`, prodTLSIssuer, acmeServer, prodTLSEmail, prodTLSIssuer, prodTLSIngressClass)

	step("🔐", fmt.Sprintf("Creating ClusterIssuer %q", prodTLSIssuer))
	if err := runStdin(issuerYAML, "kubectl", "--context", ctx, "apply", "-f", "-"); err != nil {
		return fmt.Errorf("ClusterIssuer creation failed: %w", err)
	}
	success("ClusterIssuer created")

	// ── Optionally patch a DSE file ─────────────────────────────
	if prodTLSDSEFile != "" {
		step("📝", fmt.Sprintf("Patching %s with TLS config", prodTLSDSEFile))
		if err := patchDSEWithTLS(prodTLSDSEFile, prodTLSDomain, prodTLSIssuer, prodTLSIngressClass); err != nil {
			return fmt.Errorf("failed to patch DSE: %w", err)
		}
		success(fmt.Sprintf("Updated %s with TLS config", prodTLSDSEFile))
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
	fmt.Fprintf(os.Stderr, "      host: %s\n", prodTLSDomain)
	fmt.Fprintf(os.Stderr, "      ingressClassName: %s\n", prodTLSIngressClass)
	fmt.Fprintf(os.Stderr, "      annotations:\n")
	fmt.Fprintf(os.Stderr, "        cert-manager.io/cluster-issuer: %s\n", prodTLSIssuer)
	fmt.Fprintf(os.Stderr, "      tls:\n")
	fmt.Fprintf(os.Stderr, "        secretName: %s-tls\n", strings.ReplaceAll(prodTLSDomain, ".", "-"))
	fmt.Fprintf(os.Stderr, "        hosts:\n")
	fmt.Fprintf(os.Stderr, "          - %s\n", prodTLSDomain)
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
