package ci

import (
	"fmt"
	"regexp"
	"strings"
)

// BaseRunnerAdapter provides shared naming conventions used by all CI
// providers. Embed it in your provider's RunnerAdapter to inherit these
// defaults; override any method that needs provider-specific behavior.
type BaseRunnerAdapter struct{}

// SanitizeDNS converts an arbitrary string into a valid DNS-1035 label
// (lowercase alphanumeric and hyphens only, max 63 chars, starts with a
// letter, ends with alphanumeric). Usernames like "jeff.d.vincent@gmail.com"
// become "jeff-d-vincent-gmail-com".
//
// The result is safe for K8s resource names (including Services which
// require DNS-1035), container names, Docker image tags, and label values.
// Generated CI configs must apply equivalent shell-side sanitization
// (see SanitizeShellSnippet) to the CI username before using it in
// image tags, K8s resource names, ingress hosts, or file paths.
var notDNSSafe = regexp.MustCompile(`[^a-z0-9\-]`)

// SanitizeShellSnippet is the shell (bash) equivalent of SanitizeDNS.
// Generated CI configs should include this snippet in a "Set image tag"
// run step to derive SAFE_USER from the CI provider's raw username
// variable (e.g. CIRCLE_USERNAME). Use SAFE_USER everywhere a
// DNS-safe / tag-safe identifier is required.
const SanitizeShellSnippet = `SAFE_USER=$(echo "$CIRCLE_USERNAME" | tr '[:upper:]' '[:lower:]' | sed 's/@/-/g; s/_/-/g; s/\./-/g' | tr -cd 'a-z0-9-' | sed 's/--*/-/g; s/^[-]*//; s/[-]*$//')`

func SanitizeDNS(s string) string {
	s = strings.ToLower(s)
	s = strings.ReplaceAll(s, "@", "-")
	s = strings.ReplaceAll(s, "_", "-")
	s = strings.ReplaceAll(s, ".", "-")
	s = notDNSSafe.ReplaceAllString(s, "")
	for strings.Contains(s, "--") {
		s = strings.ReplaceAll(s, "--", "-")
	}
	s = strings.Trim(s, "-")
	if len(s) > 63 {
		s = s[:63]
		s = strings.TrimRight(s, "-")
	}
	if s == "" {
		s = "runner"
	}
	return s
}

// DeploymentName returns the runner Deployment name.
// The username parameter must already be sanitized via SanitizeDNS.
func (b *BaseRunnerAdapter) DeploymentName(username string) string {
	return fmt.Sprintf("%s-runner", username)
}

// ServiceAccountName returns the ServiceAccount name.
// The username parameter must already be sanitized via SanitizeDNS.
func (b *BaseRunnerAdapter) ServiceAccountName(username string) string {
	return fmt.Sprintf("%s-runner", username)
}

// ClusterRoleName returns the ClusterRole name.
// The username parameter must already be sanitized via SanitizeDNS.
func (b *BaseRunnerAdapter) ClusterRoleName(username string) string {
	return fmt.Sprintf("%s-runner", username)
}

// ClusterRoleBindingName returns the ClusterRoleBinding name.
// The username parameter must already be sanitized via SanitizeDNS.
func (b *BaseRunnerAdapter) ClusterRoleBindingName(username string) string {
	return fmt.Sprintf("%s-runner", username)
}
