package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
)

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show the status of the cluster, operator, runners, and environments",
	Long: `Displays a dashboard-style overview of the Kind cluster including:
  • Cluster info and node status
  • kindling operator health
  • CI runner pools
  • Dev staging environments and their dependencies`,
	RunE: runStatus,
}

var statusProvider string

func init() {
	statusCmd.Flags().StringVar(&statusProvider, "ci-provider", "", "CI provider (github, gitlab)")
	rootCmd.AddCommand(statusCmd)
}

func runStatus(cmd *cobra.Command, args []string) error {
	// ── Cluster ─────────────────────────────────────────────────
	header("Cluster")

	if !clusterExists(clusterName) {
		fail(fmt.Sprintf("Kind cluster %q not found. Run: kindling init", clusterName))
		return nil
	}
	success(fmt.Sprintf("Kind cluster %q exists", clusterName))

	nodesOut, err := runCapture("kubectl", "get", "nodes",
		"-o", "custom-columns=NAME:.metadata.name,STATUS:.status.conditions[-1].type,VERSION:.status.nodeInfo.kubeletVersion",
		"--no-headers")
	if err == nil && nodesOut != "" {
		for _, line := range strings.Split(nodesOut, "\n") {
			fmt.Printf("    %s\n", strings.TrimSpace(line))
		}
	}

	// ── Operator ────────────────────────────────────────────────
	header("Operator")

	operatorOut, err := runCapture("kubectl", "get", "deployment",
		"-n", "kindling-system",
		"-o", "custom-columns=NAME:.metadata.name,READY:.status.readyReplicas,DESIRED:.spec.replicas,AGE:.metadata.creationTimestamp",
		"--no-headers")
	if err != nil || operatorOut == "" {
		warn("Controller not found in kindling-system namespace")
	} else {
		for _, line := range strings.Split(operatorOut, "\n") {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			if strings.Contains(line, "<none>") {
				fmt.Printf("    %s⚠%s  %s\n", colorYellow, colorReset, line)
			} else {
				fmt.Printf("    %s✓%s  %s\n", colorGreen, colorReset, line)
			}
		}
	}

	// ── Registry ────────────────────────────────────────────────
	header("Registry")

	regOut, err := runCapture("kubectl", "get", "deployment/registry",
		"-o", "custom-columns=READY:.status.readyReplicas,DESIRED:.spec.replicas",
		"--no-headers")
	if err != nil {
		warn("In-cluster registry not found")
	} else {
		fmt.Printf("    registry:5000  %s\n", strings.TrimSpace(regOut))
	}

	// ── Ingress ─────────────────────────────────────────────────
	header("Ingress Controller")

	ingOut, err := runCapture("kubectl", "get", "pods",
		"-n", "ingress-nginx",
		"-l", "app.kubernetes.io/component=controller",
		"-o", "custom-columns=NAME:.metadata.name,STATUS:.status.phase,RESTARTS:.status.containerStatuses[0].restartCount",
		"--no-headers")
	if err != nil || ingOut == "" {
		warn("ingress-nginx controller not found")
	} else {
		for _, line := range strings.Split(ingOut, "\n") {
			fmt.Printf("    %s\n", strings.TrimSpace(line))
		}
	}

	// ── Runner Pools ────────────────────────────────────────────
	statusProviderObj, _ := resolveProvider(statusProvider)
	labels := statusProviderObj.CLILabels()
	header(labels.CRDListHeader)

	rpOut, err := runCapture("kubectl", "get", labels.CRDPlural,
		"-o", "custom-columns=NAME:.metadata.name,USERNAME:.spec.githubUsername,REPO:.spec.repository",
		"--no-headers")
	if err != nil || rpOut == "" || strings.Contains(rpOut, "No resources") {
		fmt.Printf("    %sNone — run:%s kindling runners\n", colorDim, colorReset)
	} else {
		for _, line := range strings.Split(rpOut, "\n") {
			line = strings.TrimSpace(line)
			if line != "" {
				fmt.Printf("    🏃 %s\n", line)
			}
		}

		// Show runner deployment status
		fmt.Println()
		runnerDeploys, _ := runCapture("kubectl", "get", "deployments",
			"-l", "app.kubernetes.io/managed-by=kindling",
			"-o", "custom-columns=NAME:.metadata.name,READY:.status.readyReplicas,DESIRED:.spec.replicas",
			"--no-headers")
		if runnerDeploys != "" {
			for _, line := range strings.Split(runnerDeploys, "\n") {
				line = strings.TrimSpace(line)
				if line != "" {
					fmt.Printf("      ↳ %s\n", line)
				}
			}
		}
	}

	// ── Dev Staging Environments ────────────────────────────────
	header("Dev Staging Environments")

	dseOut, err := runCapture("kubectl", "get", "devstagingenvironments",
		"-o", "custom-columns=NAME:.metadata.name,IMAGE:.spec.deployment.image,PORT:.spec.deployment.port,INGRESS:.spec.ingress.host",
		"--no-headers")
	if err != nil || dseOut == "" || strings.Contains(dseOut, "No resources") {
		fmt.Printf("    %sNone — run:%s kindling deploy -f <file.yaml>\n", colorDim, colorReset)
	} else {
		for _, line := range strings.Split(dseOut, "\n") {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			fmt.Printf("    📦 %s\n", line)
		}
	}

	// ── Active Dev Sessions ─────────────────────────────────────
	header("Active Dev Sessions")

	sessionOut, _ := runCapture("kubectl", "get", "deployments",
		"-l", "kindling.dev/mode",
		"-o", "custom-columns=NAME:.metadata.name,MODE:.metadata.labels.kindling\\.dev/mode,RUNTIME:.metadata.labels.kindling\\.dev/runtime,READY:.status.readyReplicas",
		"--no-headers", "--context", kindContext())
	if sessionOut == "" || strings.Contains(sessionOut, "No resources") {
		fmt.Printf("    %sNone — run:%s kindling debug -d <deploy> %sor%s kindling dev -d <deploy>\n", colorDim, colorReset, colorDim, colorReset)
	} else {
		for _, line := range strings.Split(sessionOut, "\n") {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			parts := strings.Fields(line)
			if len(parts) >= 3 {
				icon := "🔧"
				if parts[1] == "dev" {
					icon = "🖥️"
				}
				fmt.Printf("    %s %s  %s[%s / %s]%s\n", icon, parts[0], colorCyan, parts[1], parts[2], colorReset)
			} else {
				fmt.Printf("    🔧 %s\n", line)
			}
		}
	}

	// ── Deployments ─────────────────────────────────────────────
	header("All Deployments")

	depOut, _ := runCapture("kubectl", "get", "deployments",
		"-o", "custom-columns=NAME:.metadata.name,READY:.status.readyReplicas,UP-TO-DATE:.status.updatedReplicas,AVAILABLE:.status.availableReplicas",
		"--no-headers")
	if depOut != "" {
		for _, line := range strings.Split(depOut, "\n") {
			line = strings.TrimSpace(line)
			if line != "" {
				fmt.Printf("    %s\n", line)
			}
		}
	}

	// ── Unhealthy Pods ──────────────────────────────────────────
	// Show CrashLoopBackOff / Error pods with their last log lines
	// so the developer doesn't have to manually run kubectl logs.
	crashPods, _ := runCapture("kubectl", "get", "pods",
		"--field-selector=status.phase!=Running,status.phase!=Succeeded",
		"-o", "custom-columns=NAME:.metadata.name,STATUS:.status.phase,REASON:.status.containerStatuses[0].state.waiting.reason",
		"--no-headers")
	if crashPods != "" {
		hasCrash := false
		for _, line := range strings.Split(crashPods, "\n") {
			line = strings.TrimSpace(line)
			if line == "" || strings.Contains(line, "<none>") {
				continue
			}
			if !hasCrash {
				header("Unhealthy Pods")
				hasCrash = true
			}
			parts := strings.Fields(line)
			podName := parts[0]
			fmt.Printf("    %s❌ %s%s\n", colorRed, line, colorReset)

			// Show last few log lines for this pod
			logs, _ := runCapture("kubectl", "logs", podName, "--tail=10")
			if logs != "" {
				for _, logLine := range strings.Split(logs, "\n") {
					logLine = strings.TrimSpace(logLine)
					if logLine != "" {
						fmt.Printf("       %s%s%s\n", colorDim, logLine, colorReset)
					}
				}
				fmt.Println()
			}
		}
	}

	// ── Ingress Routes ──────────────────────────────────────────
	header("Ingress Routes")

	ingRoutes, err := runCapture("kubectl", "get", "ingress",
		"-o", "custom-columns=NAME:.metadata.name,HOST:.spec.rules[*].host,SERVICE:.spec.rules[*].http.paths[*].backend.service.name",
		"--no-headers")
	if err != nil || ingRoutes == "" || strings.Contains(ingRoutes, "No resources") {
		fmt.Printf("    %sNo ingress routes configured%s\n", colorDim, colorReset)
	} else {
		for _, line := range strings.Split(ingRoutes, "\n") {
			line = strings.TrimSpace(line)
			if line != "" {
				// Extract host for a clickable link
				parts := strings.Fields(line)
				if len(parts) >= 2 {
					fmt.Printf("    🌐 http://%s  →  %s\n", parts[1], line)
				} else {
					fmt.Printf("    🌐 %s\n", line)
				}
			}
		}
	}

	// ── Agent Intel ─────────────────────────────────────────────
	header("Agent Intel")

	repoRoot, repoErr := findRepoRoot()
	if repoErr == nil {
		// Check disabled flag
		if _, err := os.Stat(filepath.Join(repoRoot, intelDisabledFile)); err == nil {
			fmt.Printf("    %sdisabled%s — auto-activation off\n", colorDim, colorReset)
		} else {
			intelSt, _ := loadIntelState(repoRoot)
			if intelSt != nil && intelSt.Active {
				fmt.Printf("    %s⚡ active%s — %d agent(s) configured\n", colorGreen, colorReset, len(intelSt.Written))
				for _, f := range intelSt.Written {
					fmt.Printf("       %s%s%s\n", colorCyan, f, colorReset)
				}
			} else {
				fmt.Printf("    %sinactive%s — will activate on next command\n", colorDim, colorReset)
			}
		}
	} else {
		fmt.Printf("    %s(not in a git repo)%s\n", colorDim, colorReset)
	}

	fmt.Println()
	return nil
}
