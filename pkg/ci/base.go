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

// SanitizeDNS converts an arbitrary string into a valid RFC 1123 label
// (lowercase alphanumeric, dots, and hyphens, max 63 chars, starts/ends
// with alphanumeric). Usernames like "jeff.d.vincent@gmail.com" become
// "jeff.d.vincent-gmail.com".
//
// The result is also valid for Docker image tags and K8s label values.
// Generated CI configs must apply equivalent shell-side sanitization
// (see SanitizeShellSnippet) to the CI username before using it in
// image tags, K8s resource names, ingress hosts, or file paths.
var notDNSSafe = regexp.MustCompile(`[^a-z0-9.\-]`)

// SanitizeShellSnippet is the shell (bash) equivalent of SanitizeDNS.
// Generated CI configs should include this snippet in a "Set image tag"
// run step to derive SAFE_USER from the CI provider's raw username
// variable (e.g. CIRCLE_USERNAME). Use SAFE_USER everywhere a
// DNS-safe / tag-safe identifier is required.
const SanitizeShellSnippet = `SAFE_USER=$(echo "$CIRCLE_USERNAME" | tr '[:upper:]' '[:lower:]' | sed 's/@/-/g; s/_/-/g' | tr -cd 'a-z0-9.-' | sed 's/--*/-/g; s/^[-.]*//; s/[-.]*$//')`

func SanitizeDNS(s string) string {
	s = strings.ToLower(s)
	s = strings.ReplaceAll(s, "@", "-")
	s = strings.ReplaceAll(s, "_", "-")
	s = notDNSSafe.ReplaceAllString(s, "")
	for strings.Contains(s, "--") {
		s = strings.ReplaceAll(s, "--", "-")
	}
	s = strings.Trim(s, "-.")
	if len(s) > 63 {
		s = s[:63]
		s = strings.TrimRight(s, "-.")
	}
	if s == "" {
		s = "runner"
	}
	return s
}

func (b *BaseRunnerAdapter) DeploymentName(username string) string {
	return fmt.Sprintf("%s-runner", SanitizeDNS(username))
}

func (b *BaseRunnerAdapter) ServiceAccountName(username string) string {
	return fmt.Sprintf("%s-runner", SanitizeDNS(username))
}

func (b *BaseRunnerAdapter) ClusterRoleName(username string) string {
	return fmt.Sprintf("%s-runner", SanitizeDNS(username))
}

func (b *BaseRunnerAdapter) ClusterRoleBindingName(username string) string {
	return fmt.Sprintf("%s-runner", SanitizeDNS(username))
}
