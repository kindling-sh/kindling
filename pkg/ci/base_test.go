package ci

import (
	"testing"
)

func TestSanitizeDNS(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"jeff", "jeff"},
		{"alice", "alice"},
		{"bob-123", "bob-123"},
		{"", "runner"},
		{"Jeff.D.Vincent@gmail.com", "jeff-d-vincent-gmail-com"},
		{"user_name", "user-name"},
		{"UPPER", "upper"},
		{"dots.in.name", "dots-in-name"},
		{"---leading", "leading"},
		{"trailing---", "trailing"},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			if got := SanitizeDNS(tt.input); got != tt.want {
				t.Errorf("SanitizeDNS(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestBaseRunnerAdapterDeploymentName(t *testing.T) {
	b := &BaseRunnerAdapter{}
	// Adapter methods receive pre-sanitized usernames.
	tests := []struct {
		user string
		want string
	}{
		{"jeff", "jeff-runner"},
		{"alice", "alice-runner"},
		{"bob-123", "bob-123-runner"},
		{"jeff-d-vincent-gmail-com", "jeff-d-vincent-gmail-com-runner"},
	}
	for _, tt := range tests {
		t.Run(tt.user, func(t *testing.T) {
			if got := b.DeploymentName(tt.user); got != tt.want {
				t.Errorf("DeploymentName(%q) = %q, want %q", tt.user, got, tt.want)
			}
		})
	}
}

func TestBaseRunnerAdapterServiceAccountName(t *testing.T) {
	b := &BaseRunnerAdapter{}
	if got := b.ServiceAccountName("jeff"); got != "jeff-runner" {
		t.Errorf("ServiceAccountName(jeff) = %q, want jeff-runner", got)
	}
}

func TestBaseRunnerAdapterClusterRoleName(t *testing.T) {
	b := &BaseRunnerAdapter{}
	if got := b.ClusterRoleName("jeff"); got != "jeff-runner" {
		t.Errorf("ClusterRoleName(jeff) = %q, want jeff-runner", got)
	}
}

func TestBaseRunnerAdapterClusterRoleBindingName(t *testing.T) {
	b := &BaseRunnerAdapter{}
	if got := b.ClusterRoleBindingName("jeff"); got != "jeff-runner" {
		t.Errorf("ClusterRoleBindingName(jeff) = %q, want jeff-runner", got)
	}
}

func TestBaseRunnerAdapterConsistentNaming(t *testing.T) {
	b := &BaseRunnerAdapter{}
	user := "testuser"
	// All base names should be the same: "<user>-runner"
	expected := user + "-runner"
	if b.DeploymentName(user) != expected {
		t.Errorf("DeploymentName mismatch")
	}
	if b.ServiceAccountName(user) != expected {
		t.Errorf("ServiceAccountName mismatch")
	}
	if b.ClusterRoleName(user) != expected {
		t.Errorf("ClusterRoleName mismatch")
	}
	if b.ClusterRoleBindingName(user) != expected {
		t.Errorf("ClusterRoleBindingName mismatch")
	}
}
