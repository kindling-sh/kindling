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

// sanitizeDNS converts an arbitrary string into a valid RFC 1123 label
// (lowercase alphanumeric and hyphens, max 63 chars, starts/ends with
// alphanumeric). Usernames like "jeff.d.vincent@gmail.com" become
// "jeff.d.vincent-gmail.com".
var notDNSSafe = regexp.MustCompile(`[^a-z0-9.\-]`)

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
