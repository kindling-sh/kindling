package cmd

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

var envCmd = &cobra.Command{
	Use:   "env",
	Short: "Manage environment variables on running deployments",
	Long: `Set, list, or remove environment variables on a running deployment
without redeploying. Changes take effect immediately via a rolling restart.

Examples:
  kindling env set jeff-vincent-compute DATABASE_PORT=5432
  kindling env set jeff-vincent-compute DB_HOST=my-db DB_PORT=5432
  kindling env list jeff-vincent-compute
  kindling env unset jeff-vincent-compute DATABASE_PORT`,
}

var envSetCmd = &cobra.Command{
	Use:   "set <deployment> KEY=VALUE [KEY=VALUE ...]",
	Short: "Set environment variables on a running deployment",
	Long: `Sets one or more environment variables on a deployment. The deployment
will do a rolling restart to pick up the new values.

Examples:
  kindling env set jeff-vincent-compute DATABASE_PORT=5432
  kindling env set jeff-vincent-compute DB_HOST=my-db DB_PORT=5432 DEBUG=true`,
	Args: cobra.MinimumNArgs(2),
	RunE: runEnvSet,
}

var envListCmd = &cobra.Command{
	Use:   "list <deployment>",
	Short: "List environment variables on a deployment",
	Args:  cobra.ExactArgs(1),
	RunE:  runEnvList,
}

var envUnsetCmd = &cobra.Command{
	Use:   "unset <deployment> KEY [KEY ...]",
	Short: "Remove environment variables from a deployment",
	Args:  cobra.MinimumNArgs(2),
	RunE:  runEnvUnset,
}

func init() {
	envCmd.AddCommand(envSetCmd)
	envCmd.AddCommand(envListCmd)
	envCmd.AddCommand(envUnsetCmd)
	rootCmd.AddCommand(envCmd)
}

func runEnvSet(cmd *cobra.Command, args []string) error {
	deploy := args[0]
	pairs := args[1:]

	// Validate KEY=VALUE format
	for _, p := range pairs {
		if !strings.Contains(p, "=") {
			return fmt.Errorf("invalid format %q — expected KEY=VALUE", p)
		}
	}

	// Verify deployment exists
	if _, err := runSilent("kubectl", "get", "deployment/"+deploy); err != nil {
		return fmt.Errorf("deployment %q not found", deploy)
	}

	header("Setting environment variables")

	// kubectl set env handles rolling restart automatically
	setArgs := append([]string{"set", "env", "deployment/" + deploy}, pairs...)
	if err := run("kubectl", setArgs...); err != nil {
		return fmt.Errorf("failed to set env: %w", err)
	}

	for _, p := range pairs {
		parts := strings.SplitN(p, "=", 2)
		step("✏️ ", fmt.Sprintf("%s=%s", parts[0], parts[1]))
	}

	success(fmt.Sprintf("Updated deployment/%s — rolling restart triggered", deploy))
	return nil
}

func runEnvList(cmd *cobra.Command, args []string) error {
	deploy := args[0]

	if _, err := runSilent("kubectl", "get", "deployment/"+deploy); err != nil {
		return fmt.Errorf("deployment %q not found", deploy)
	}

	header(fmt.Sprintf("Environment: %s", deploy))

	// Get env vars from the first container
	out, err := runCapture("kubectl", "get", "deployment/"+deploy,
		"-o", "jsonpath={range .spec.template.spec.containers[0].env[*]}{.name}={.value}{\"\\n\"}{end}")
	if err != nil {
		return fmt.Errorf("failed to read env: %w", err)
	}

	if strings.TrimSpace(out) == "" {
		fmt.Printf("    %sNo environment variables set%s\n", colorDim, colorReset)
	} else {
		for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
			if line == "" {
				continue
			}
			parts := strings.SplitN(line, "=", 2)
			if len(parts) == 2 {
				fmt.Printf("    %s%s%s=%s\n", colorCyan, parts[0], colorReset, parts[1])
			} else {
				fmt.Printf("    %s\n", line)
			}
		}
	}

	fmt.Println()
	return nil
}

func runEnvUnset(cmd *cobra.Command, args []string) error {
	deploy := args[0]
	keys := args[1:]

	if _, err := runSilent("kubectl", "get", "deployment/"+deploy); err != nil {
		return fmt.Errorf("deployment %q not found", deploy)
	}

	header("Removing environment variables")

	// kubectl set env KEY- removes the variable
	unsetArgs := []string{"set", "env", "deployment/" + deploy}
	for _, k := range keys {
		unsetArgs = append(unsetArgs, k+"-")
		step("🗑️ ", k)
	}

	if err := run("kubectl", unsetArgs...); err != nil {
		return fmt.Errorf("failed to unset env: %w", err)
	}

	success(fmt.Sprintf("Updated deployment/%s — rolling restart triggered", deploy))
	return nil
}
