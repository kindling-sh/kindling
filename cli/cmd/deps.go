package cmd

import (
	"encoding/json"
	"fmt"

	"github.com/jeffvincent/kindling/cli/core"
	"github.com/spf13/cobra"
)

// ── kindling deps ────────────────────────────────────────────────
//
// Manages shared dependency resources (spec.dependencies[].shared) —
// dependency instances that converge across multiple DSEs instead of each
// DSE getting its own dedicated Deployment/Service. Shared instances are
// intentionally not owned by any single DSE (so deleting one DSE doesn't
// take the shared instance down for the others), and aren't automatically
// deleted once unused — this command finds and (optionally) cleans those
// up.

var depsCmd = &cobra.Command{
	Use:   "deps",
	Short: "Manage shared dependency resources (e.g. a Redis shared by several DSEs)",
	Long: `Lists and cleans up shared dependency instances (spec.dependencies[].shared:
true) — dependency resources that converge across multiple DSEs of the
same type (and sharedKey, if set) instead of each DSE getting its own
dedicated pod.

Shared instances are intentionally not owned by any single DSE, so
deleting one DSE doesn't take the shared instance down for the others.
They also aren't deleted automatically once unused — run
'kindling deps prune-shared' to clean those up explicitly.`,
}

var depsListSharedCmd = &cobra.Command{
	Use:   "list-shared",
	Short: "List shared dependency instances and which DSEs reference them",
	RunE:  runDepsListShared,
}

var depsPruneSharedCmd = &cobra.Command{
	Use:   "prune-shared",
	Short: "Delete shared dependency instances that no DSE references anymore",
	RunE:  runDepsPruneShared,
}

func init() {
	depsCmd.AddCommand(depsListSharedCmd)
	depsCmd.AddCommand(depsPruneSharedCmd)
	rootCmd.AddCommand(depsCmd)
}

// sharedDepInfo describes one shared dependency instance found in the
// cluster, and which DSEs (if any) currently reference it.
type sharedDepInfo struct {
	Name          string // e.g. "shared-redis"
	Type          string
	SharedKey     string
	ReferencedBy  []string
	ReplicasReady int32
}

// listSharedDependencies finds every shared dependency Deployment in the
// cluster and cross-references it against every DSE's spec.dependencies[]
// to see which DSEs (if any) still reference it.
func listSharedDependencies() ([]sharedDepInfo, error) {
	// 1. Find shared Deployments.
	out, err := core.Kubectl(clusterName, "get", "deployments", "-n", "default",
		"-l", "app.kubernetes.io/managed-by=devstagingenvironment-operator,app.kubernetes.io/part-of=shared",
		"-o", "json")
	if err != nil {
		return nil, fmt.Errorf("cannot list shared dependencies: %s", out)
	}
	var deployList struct {
		Items []struct {
			Metadata struct {
				Name   string            `json:"name"`
				Labels map[string]string `json:"labels"`
			} `json:"metadata"`
			Status struct {
				ReadyReplicas int32 `json:"readyReplicas"`
			} `json:"status"`
		} `json:"items"`
	}
	if err := json.Unmarshal([]byte(out), &deployList); err != nil {
		return nil, fmt.Errorf("cannot parse shared deployments: %w", err)
	}

	infos := make(map[string]*sharedDepInfo, len(deployList.Items))
	for _, item := range deployList.Items {
		infos[item.Metadata.Name] = &sharedDepInfo{
			Name:          item.Metadata.Name,
			Type:          item.Metadata.Labels["app.kubernetes.io/component"],
			SharedKey:     item.Metadata.Labels["kindling.dev/shared-key"],
			ReplicasReady: item.Status.ReadyReplicas,
		}
	}

	// 2. Find every DSE's shared dependency references.
	dseOut, err := core.Kubectl(clusterName, "get", "devstagingenvironments", "-n", "default", "-o", "json")
	if err != nil {
		return nil, fmt.Errorf("cannot list DSEs: %s", dseOut)
	}
	var dseList struct {
		Items []struct {
			Metadata struct {
				Name string `json:"name"`
			} `json:"metadata"`
			Spec struct {
				Dependencies []struct {
					Type      string `json:"type"`
					Shared    bool   `json:"shared,omitempty"`
					SharedKey string `json:"sharedKey,omitempty"`
				} `json:"dependencies,omitempty"`
			} `json:"spec"`
		} `json:"items"`
	}
	if err := json.Unmarshal([]byte(dseOut), &dseList); err != nil {
		return nil, fmt.Errorf("cannot parse DSE list: %w", err)
	}

	for _, dse := range dseList.Items {
		for _, dep := range dse.Spec.Dependencies {
			if !dep.Shared {
				continue
			}
			key := dep.SharedKey
			if key == "" {
				key = dep.Type
			}
			name := "shared-" + key
			if info, ok := infos[name]; ok {
				info.ReferencedBy = append(info.ReferencedBy, dse.Metadata.Name)
			}
		}
	}

	result := make([]sharedDepInfo, 0, len(infos))
	for _, info := range infos {
		result = append(result, *info)
	}
	return result, nil
}

func runDepsListShared(cmd *cobra.Command, args []string) error {
	shared, err := listSharedDependencies()
	if err != nil {
		return err
	}

	header("Shared dependencies")
	if len(shared) == 0 {
		fmt.Printf("  %s(none found)%s\n", colorDim, colorReset)
		return nil
	}

	for _, s := range shared {
		status := fmt.Sprintf("%d ready", s.ReplicasReady)
		fmt.Printf("  %s%s%s (%s)\n", colorCyan, s.Name, colorReset, status)
		if len(s.ReferencedBy) == 0 {
			fmt.Printf("    %sno DSE references this — candidate for 'kindling deps prune-shared'%s\n", colorYellow, colorReset)
		} else {
			for _, name := range s.ReferencedBy {
				fmt.Printf("    used by %s\n", name)
			}
		}
	}
	return nil
}

func runDepsPruneShared(cmd *cobra.Command, args []string) error {
	shared, err := listSharedDependencies()
	if err != nil {
		return err
	}

	header("Pruning unused shared dependencies")
	pruned := 0
	for _, s := range shared {
		if len(s.ReferencedBy) > 0 {
			continue
		}
		step("🗑️ ", fmt.Sprintf("%s (no DSE references it)", s.Name))
		if out, err := core.Kubectl(clusterName, "delete", "deployment", s.Name, "-n", "default", "--ignore-not-found"); err != nil {
			warn(fmt.Sprintf("Could not delete deployment/%s: %s", s.Name, out))
			continue
		}
		if out, err := core.Kubectl(clusterName, "delete", "service", s.Name, "-n", "default", "--ignore-not-found"); err != nil {
			warn(fmt.Sprintf("Could not delete service/%s: %s", s.Name, out))
		}
		if out, err := core.Kubectl(clusterName, "delete", "secret", s.Name+"-credentials", "-n", "default", "--ignore-not-found"); err != nil {
			warn(fmt.Sprintf("Could not delete secret/%s-credentials: %s", s.Name, out))
		}
		pruned++
	}

	if pruned == 0 {
		success("Nothing to prune — every shared dependency is still referenced")
	} else {
		success(fmt.Sprintf("Pruned %d unused shared dependency instance(s)", pruned))
	}
	return nil
}
