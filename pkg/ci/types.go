// Package ci defines interfaces for abstracting CI/CD platform providers.
//
// The core interface is [Provider], which wraps a [RunnerAdapter] (for CI
// runner registration/lifecycle) and a [WorkflowGenerator] (for AI-assisted
// CI workflow generation). Platform-specific implementations live in this
// package (e.g. [GitHubProvider] for GitHub Actions).
package ci

// Provider represents a CI/CD platform (GitHub Actions, GitLab CI, etc.).
type Provider interface {
	// Name returns the short identifier (e.g., "github", "gitlab").
	Name() string

	// DisplayName returns a human-readable name (e.g., "GitHub Actions").
	DisplayName() string

	// Runner returns the runner adapter for this provider.
	Runner() RunnerAdapter

	// Workflow returns the workflow generator for this provider.
	Workflow() WorkflowGenerator

	// CLILabels returns the human-facing labels used in CLI prompts and output.
	CLILabels() CLILabels
}

// RunnerAdapter abstracts CI runner registration and lifecycle management.
//
// Implementations translate provider-agnostic runner pool configuration into
// provider-specific container specs: environment variables, startup scripts,
// Kubernetes labels, and naming conventions.
//
// All methods that accept a "username" parameter expect a pre-sanitized value
// (via SanitizeDNS). Callers — typically the controller — must sanitize raw
// user input once at the boundary before passing it to any adapter method.
type RunnerAdapter interface {
	// DefaultImage returns the default container image for self-hosted runners.
	// Example: "ghcr.io/actions/actions-runner:latest"
	DefaultImage() string

	// DefaultWorkDir returns the default working directory inside the runner
	// container. Each CI platform image has different filesystem layouts:
	//   GitHub Actions: "/home/runner/_work"  (runner user owns /home/runner)
	//   GitLab:         "/builds"             (gitlab-runner convention)
	DefaultWorkDir() string

	// DefaultTokenKey returns the default key name within the CI token secret.
	// Example: "github-token"
	DefaultTokenKey() string

	// APIBaseURL computes the platform API URL from the provider base URL.
	// For GitHub: "https://github.com" -> "https://api.github.com"
	// For GHE:    "https://git.corp.com" -> "https://git.corp.com/api/v3"
	APIBaseURL(platformURL string) string

	// RunnerEnvVars returns the environment variables needed to register and
	// run the CI runner container.
	RunnerEnvVars(cfg RunnerEnvConfig) []ContainerEnvVar

	// StartupScript returns the shell script that registers the runner with
	// the CI platform, sets up a de-registration trap, and starts polling.
	StartupScript() string

	// RunnerLabels returns Kubernetes labels for runner pool resources.
	RunnerLabels(username string, crName string) map[string]string

	// DeploymentName returns the runner Deployment name for a user.
	// Example: "jeff-runner"
	DeploymentName(username string) string

	// ServiceAccountName returns the ServiceAccount name for a user's runners.
	// Example: "jeff-runner"
	ServiceAccountName(username string) string

	// ClusterRoleName returns the ClusterRole name for a user's runners.
	ClusterRoleName(username string) string

	// ClusterRoleBindingName returns the ClusterRoleBinding name.
	ClusterRoleBindingName(username string) string
}

// RunnerEnvConfig holds the provider-agnostic configuration needed to generate
// runner container environment variables.
type RunnerEnvConfig struct {
	Username        string
	Repository      string
	PlatformURL     string // e.g., "https://github.com"
	TokenSecretName string // k8s Secret name
	TokenSecretKey  string // key within the Secret
	Labels          []string
	RunnerGroup     string
	WorkDir         string
	CRName          string // name of the CR object
}

// ContainerEnvVar represents an environment variable for a container.
// Either Value or SecretRef is set, not both.
type ContainerEnvVar struct {
	Name      string
	Value     string     // plain text value
	SecretRef *SecretRef // value from a Kubernetes secret
}

// SecretRef points to a key within a Kubernetes Secret.
type SecretRef struct {
	Name string
	Key  string
}

// WorkflowGenerator abstracts CI workflow file generation for AI-assisted
// workflow creation (kindling generate).
type WorkflowGenerator interface {
	// DefaultOutputPath returns the default workflow file path relative to
	// the repository root.
	// Example: ".github/workflows/dev-deploy.yml"
	DefaultOutputPath() string

	// SystemPrompt returns the full system prompt for AI workflow generation.
	// hostArch is the target CPU architecture (e.g. "arm64", "amd64") and is
	// substituted into Kaniko Dockerfile-patching examples.
	//
	// Implementations assemble the prompt from shared building blocks in
	// prompt.go (Kaniko, dependencies, deploy philosophy) plus their own
	// CI-platform-specific syntax instructions.
	SystemPrompt(hostArch string) string

	// PromptContext returns CI-platform-specific values that are interpolated
	// into the kindling user prompt for AI workflow generation.
	PromptContext() PromptContext

	// ExampleWorkflows returns reference workflow examples for the AI prompt.
	ExampleWorkflows() (singleService, multiService string)

	// StripTemplateExpressions removes provider-specific template expressions
	// from content. Used by fuzz/analysis tools to normalize generated
	// workflows before comparison or static analysis.
	StripTemplateExpressions(content string) string
}

// PromptContext holds CI-platform-specific values that are interpolated into
// the kindling system prompt for AI workflow generation. The prompt text itself
// lives in generate.go; these values parameterize the CI-specific parts.
type PromptContext struct {
	// PlatformName is the human name (e.g., "GitHub Actions", "GitLab CI").
	PlatformName string

	// WorkflowNoun is what the CI config file is called
	// (e.g., "workflow" for GitHub, "pipeline" for GitLab).
	WorkflowNoun string

	// BuildActionRef is the action/step reference for kindling-build.
	// Example: "kindling-sh/kindling/.github/actions/kindling-build@main"
	BuildActionRef string

	// DeployActionRef is the action/step reference for kindling-deploy.
	// Example: "kindling-sh/kindling/.github/actions/kindling-deploy@main"
	DeployActionRef string

	// CheckoutAction is the step reference for checking out code.
	// Example: "actions/checkout@v4"
	CheckoutAction string

	// ActorExpr is the template expression for the current CI user/actor.
	// Example: "${{ github.actor }}"
	ActorExpr string

	// SHAExpr is the template expression for the current commit SHA.
	// Example: "${{ github.sha }}"
	SHAExpr string

	// WorkspaceExpr is the template expression for the workspace directory.
	// Example: "${{ github.workspace }}"
	WorkspaceExpr string

	// RunnerSpec is the runs-on/tags YAML for specifying the runner.
	// Example: `[self-hosted, "${{ github.actor }}"]`
	RunnerSpec string

	// EnvTagExpr is the expression used for the image tag.
	// Example: "${{ github.actor }}-${{ github.sha }}"
	EnvTagExpr string

	// TriggerBlock returns the trigger YAML block for a given branch.
	// Example for GitHub:
	//   on:
	//     push:
	//       branches: [main]
	//     workflow_dispatch:
	TriggerBlock func(branch string) string

	// WorkflowFileDescription describes what's being generated, for the
	// user prompt. Example: "GitHub Actions workflow"
	WorkflowFileDescription string
}

// CLILabels holds human-facing labels for CLI interactive prompts and display.
type CLILabels struct {
	// Username is the prompt label for the CI platform username.
	// Example: "GitHub username"
	Username string

	// Repository is the prompt label for the repository identifier.
	// Example: "GitHub repository (owner/repo)"
	Repository string

	// Token is the prompt label for the CI platform token.
	// Example: "GitHub PAT (repo scope)"
	Token string

	// SecretName is the default Kubernetes Secret name for the CI token.
	// Example: "github-runner-token"
	SecretName string

	// CRDKind is the CustomResourceDefinition kind name.
	// Example: "CIRunnerPool"
	CRDKind string

	// CRDPlural is the CRD plural resource name for kubectl.
	// Example: "cirunnerpools"
	CRDPlural string

	// CRDListHeader is the display header for listing runner pools.
	// Example: "GitHub Actions Runner Pools"
	CRDListHeader string

	// RunnerComponent is the Kubernetes label value for the runner component.
	// Example: "github-actions-runner"
	RunnerComponent string

	// ActionsURLFmt is the URL template to the CI platform's actions/pipelines page.
	// The %s placeholder is the repository slug (e.g., "owner/repo").
	// Example: "https://github.com/%s/actions"
	ActionsURLFmt string

	// CRDAPIVersion is the full API version for the CRD.
	// Example: "apps.example.com/v1alpha1"
	CRDAPIVersion string
}
