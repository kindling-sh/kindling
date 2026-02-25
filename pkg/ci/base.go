package ci

import "fmt"

// BaseRunnerAdapter provides shared naming conventions used by all CI
// providers. Embed it in your provider's RunnerAdapter to inherit these
// defaults; override any method that needs provider-specific behavior.
type BaseRunnerAdapter struct{}

func (b *BaseRunnerAdapter) DeploymentName(username string) string {
	return fmt.Sprintf("%s-runner", username)
}

func (b *BaseRunnerAdapter) ServiceAccountName(username string) string {
	return fmt.Sprintf("%s-runner", username)
}

func (b *BaseRunnerAdapter) ClusterRoleName(username string) string {
	return fmt.Sprintf("%s-runner", username)
}

func (b *BaseRunnerAdapter) ClusterRoleBindingName(username string) string {
	return fmt.Sprintf("%s-runner", username)
}
