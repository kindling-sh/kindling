package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/jeffvincent/kindling/cli/core"
	"github.com/spf13/cobra"
)

// ── kindling ingress ─────────────────────────────────────────────
//
// Manages extra path -> service routes on a DSE's managed Ingress
// (spec.ingress.routes), so a frontend that calls several backend
// services via same-origin path prefixes (e.g. /orgs, /auth, /billing)
// can be routed under one shared host without a second, unmanaged
// Ingress object.
//
// Workflow: patch a route onto the live DSE with `add-route`, confirm it
// works, then `save` writes the same routes into the DSE's YAML file so
// they're in place on every future deploy.

var ingressCmd = &cobra.Command{
	Use:   "ingress",
	Short: "Manage extra path -> service routes on a DSE's Ingress",
	Long: `Add, remove, list, and persist extra path -> service routes on a
DevStagingEnvironment's managed Ingress (spec.ingress.routes) — additional
paths merged onto the same host/Ingress alongside the DSE's primary route.

Typical workflow: patch a route onto the already-deployed DSE with
add-route, confirm it works, then run 'save' to write the same routes
into the DSE's YAML file so they're applied on every future deploy.

Examples:
  kindling ingress list jeff-vincent-gateway
  kindling ingress add-route jeff-vincent-gateway --path /orders --service jeff-vincent-orders --port 5000
  kindling ingress remove-route jeff-vincent-gateway --path /orders
  kindling ingress save jeff-vincent-gateway -f dev-environment.yaml`,
}

var (
	ingressRoutePath     string
	ingressRouteService  string
	ingressRoutePort     int
	ingressRoutePathType string
	ingressSaveFile      string
)

var ingressListCmd = &cobra.Command{
	Use:   "list <dse-name>",
	Short: "List the primary route and extra routes on a DSE's Ingress",
	Args:  cobra.ExactArgs(1),
	RunE:  runIngressList,
}

var ingressAddRouteCmd = &cobra.Command{
	Use:   "add-route <dse-name>",
	Short: "Add (or update) a path -> service route on a DSE's live Ingress",
	Long: `Patches the live DevStagingEnvironment so its managed Ingress routes
an additional path to another Service, alongside the DSE's primary route.
This only patches the live cluster resource — run 'kindling ingress save'
afterwards to persist the same routes into the DSE's YAML file.`,
	Args: cobra.ExactArgs(1),
	RunE: runIngressAddRoute,
}

var ingressRemoveRouteCmd = &cobra.Command{
	Use:   "remove-route <dse-name>",
	Short: "Remove a path route from a DSE's live Ingress",
	Args:  cobra.ExactArgs(1),
	RunE:  runIngressRemoveRoute,
}

var ingressSaveCmd = &cobra.Command{
	Use:   "save <dse-name>",
	Short: "Write a DSE's live extra routes into its DSE YAML file",
	Long: `Reads the extra routes currently on the live DSE's Ingress
(spec.ingress.routes) and writes them into the ingress: block of the given
DSE YAML file, so they're applied automatically on every future deploy —
not just this session's live patch.`,
	Args: cobra.ExactArgs(1),
	RunE: runIngressSave,
}

func init() {
	ingressAddRouteCmd.Flags().StringVar(&ingressRoutePath, "path", "", "URL path to route, e.g. /orders (required)")
	ingressAddRouteCmd.Flags().StringVar(&ingressRouteService, "service", "", "Kubernetes Service name to route to (required)")
	ingressAddRouteCmd.Flags().IntVar(&ingressRoutePort, "port", 0, "Service port to route to (required)")
	ingressAddRouteCmd.Flags().StringVar(&ingressRoutePathType, "path-type", "Prefix", "Prefix, Exact, or ImplementationSpecific")
	_ = ingressAddRouteCmd.MarkFlagRequired("path")
	_ = ingressAddRouteCmd.MarkFlagRequired("service")
	_ = ingressAddRouteCmd.MarkFlagRequired("port")

	ingressRemoveRouteCmd.Flags().StringVar(&ingressRoutePath, "path", "", "URL path to remove (required)")
	_ = ingressRemoveRouteCmd.MarkFlagRequired("path")

	ingressSaveCmd.Flags().StringVarP(&ingressSaveFile, "file", "f", "", "DSE YAML file to update (required)")
	_ = ingressSaveCmd.MarkFlagRequired("file")

	ingressCmd.AddCommand(ingressListCmd)
	ingressCmd.AddCommand(ingressAddRouteCmd)
	ingressCmd.AddCommand(ingressRemoveRouteCmd)
	ingressCmd.AddCommand(ingressSaveCmd)
	rootCmd.AddCommand(ingressCmd)
}

// ── Shared: read the live DSE's ingress spec ────────────────────

type liveIngress struct {
	Enabled          bool
	Host             string
	Path             string
	IngressClassName string
	ServicePort      int32
	Routes           []snapshotRoute
}

func getLiveDSEIngress(dseName string) (*liveIngress, error) {
	out, err := core.Kubectl(clusterName, "get", "devstagingenvironment", dseName, "-n", "default", "-o", "json")
	if err != nil {
		return nil, fmt.Errorf("DSE %q not found: %s", dseName, out)
	}

	var dse struct {
		Spec struct {
			Service struct {
				Port int32 `json:"port"`
			} `json:"service"`
			Ingress *struct {
				Enabled          bool    `json:"enabled"`
				Host             string  `json:"host,omitempty"`
				Path             string  `json:"path,omitempty"`
				IngressClassName *string `json:"ingressClassName,omitempty"`
				Routes           []struct {
					Path     string `json:"path"`
					PathType string `json:"pathType,omitempty"`
					Service  string `json:"service"`
					Port     int    `json:"port"`
				} `json:"routes,omitempty"`
			} `json:"ingress,omitempty"`
		} `json:"spec"`
	}
	if err := json.Unmarshal([]byte(out), &dse); err != nil {
		return nil, fmt.Errorf("cannot parse DSE %q: %w", dseName, err)
	}

	li := &liveIngress{ServicePort: dse.Spec.Service.Port}
	if dse.Spec.Ingress != nil {
		li.Enabled = dse.Spec.Ingress.Enabled
		li.Host = dse.Spec.Ingress.Host
		li.Path = dse.Spec.Ingress.Path
		if dse.Spec.Ingress.IngressClassName != nil {
			li.IngressClassName = *dse.Spec.Ingress.IngressClassName
		}
		for _, r := range dse.Spec.Ingress.Routes {
			pathType := r.PathType
			if pathType == "" {
				pathType = "Prefix"
			}
			li.Routes = append(li.Routes, snapshotRoute{
				Path: r.Path, PathType: pathType, Service: r.Service, Port: r.Port,
			})
		}
	}
	return li, nil
}

// patchLiveDSERoutes replaces spec.ingress.routes on the live DSE with the
// given list via a JSON merge patch (JSON merge patch replaces arrays
// wholesale, so callers must pass the full desired route list).
func patchLiveDSERoutes(dseName string, routes []snapshotRoute) error {
	routeVals := make([]map[string]interface{}, 0, len(routes))
	for _, r := range routes {
		pathType := r.PathType
		if pathType == "" {
			pathType = "Prefix"
		}
		routeVals = append(routeVals, map[string]interface{}{
			"path":     r.Path,
			"pathType": pathType,
			"service":  r.Service,
			"port":     r.Port,
		})
	}
	patch := map[string]interface{}{
		"spec": map[string]interface{}{
			"ingress": map[string]interface{}{
				"routes": routeVals,
			},
		},
	}
	data, err := json.Marshal(patch)
	if err != nil {
		return fmt.Errorf("cannot build patch: %w", err)
	}
	if out, err := core.Kubectl(clusterName, "patch", "devstagingenvironment", dseName,
		"-n", "default", "--type=merge", "-p", string(data)); err != nil {
		return fmt.Errorf("kubectl patch failed: %s", out)
	}
	return nil
}

// ── list ─────────────────────────────────────────────────────────

func runIngressList(cmd *cobra.Command, args []string) error {
	dseName := args[0]
	li, err := getLiveDSEIngress(dseName)
	if err != nil {
		return err
	}
	if !li.Enabled {
		warn(fmt.Sprintf("Ingress is not enabled on %s", dseName))
		return nil
	}

	header(fmt.Sprintf("Ingress: %s", dseName))
	step("🌐", fmt.Sprintf("Host: %s%s%s", colorCyan, li.Host, colorReset))
	if li.IngressClassName != "" {
		step("🏷", fmt.Sprintf("IngressClass: %s", li.IngressClassName))
	}
	fmt.Println()

	path := li.Path
	if path == "" {
		path = "/"
	}
	fmt.Printf("  %-20s → %s:%d %s(primary)%s\n", path, dseName, li.ServicePort, colorDim, colorReset)
	for _, r := range li.Routes {
		fmt.Printf("  %-20s → %s:%d %s(%s)%s\n", r.Path, r.Service, r.Port, colorDim, r.PathType, colorReset)
	}
	if len(li.Routes) == 0 {
		fmt.Printf("  %s(no extra routes — kindling ingress add-route to add one)%s\n", colorDim, colorReset)
	}
	return nil
}

// ── add-route ────────────────────────────────────────────────────

func runIngressAddRoute(cmd *cobra.Command, args []string) error {
	dseName := args[0]
	li, err := getLiveDSEIngress(dseName)
	if err != nil {
		return err
	}
	if !li.Enabled {
		return fmt.Errorf("ingress is not enabled on %s — enable spec.ingress.enabled first", dseName)
	}

	// Replace an existing route with the same path, otherwise append.
	routes := li.Routes
	replaced := false
	for i, r := range routes {
		if r.Path == ingressRoutePath {
			routes[i] = snapshotRoute{Path: ingressRoutePath, PathType: ingressRoutePathType, Service: ingressRouteService, Port: ingressRoutePort}
			replaced = true
			break
		}
	}
	if !replaced {
		routes = append(routes, snapshotRoute{Path: ingressRoutePath, PathType: ingressRoutePathType, Service: ingressRouteService, Port: ingressRoutePort})
	}

	header(fmt.Sprintf("Adding route to %s", dseName))
	if err := patchLiveDSERoutes(dseName, routes); err != nil {
		return err
	}
	step("➕", fmt.Sprintf("%s → %s:%d (%s)", ingressRoutePath, ingressRouteService, ingressRoutePort, ingressRoutePathType))
	success("Route added to the live Ingress")
	fmt.Println()
	fmt.Printf("  Once you've confirmed it works, persist it with:\n")
	fmt.Printf("    %skindling ingress save %s -f <dse.yaml>%s\n\n", colorCyan, dseName, colorReset)
	return nil
}

// ── remove-route ─────────────────────────────────────────────────

func runIngressRemoveRoute(cmd *cobra.Command, args []string) error {
	dseName := args[0]
	li, err := getLiveDSEIngress(dseName)
	if err != nil {
		return err
	}

	var routes []snapshotRoute
	found := false
	for _, r := range li.Routes {
		if r.Path == ingressRoutePath {
			found = true
			continue
		}
		routes = append(routes, r)
	}
	if !found {
		return fmt.Errorf("no route for path %q found on %s", ingressRoutePath, dseName)
	}

	header(fmt.Sprintf("Removing route from %s", dseName))
	if err := patchLiveDSERoutes(dseName, routes); err != nil {
		return err
	}
	success(fmt.Sprintf("Removed route %s", ingressRoutePath))
	return nil
}

// ── save ─────────────────────────────────────────────────────────

func runIngressSave(cmd *cobra.Command, args []string) error {
	dseName := args[0]
	li, err := getLiveDSEIngress(dseName)
	if err != nil {
		return err
	}

	header(fmt.Sprintf("Saving routes to %s", ingressSaveFile))
	if err := writeRoutesToYAML(ingressSaveFile, li.Routes); err != nil {
		return err
	}
	if len(li.Routes) == 0 {
		warn("No extra routes found on the live DSE — wrote an empty routes list")
	} else {
		for _, r := range li.Routes {
			step("💾", fmt.Sprintf("%s → %s:%d (%s)", r.Path, r.Service, r.Port, r.PathType))
		}
	}
	success(fmt.Sprintf("Routes written to %s", ingressSaveFile))
	return nil
}

// writeRoutesToYAML rewrites the "routes:" block inside the first
// "ingress:" block found in yamlFile to match routes, preserving the rest
// of the file. Follows the same line-based patching approach as
// patchDSEWithTLS in staging.go — this repo doesn't otherwise depend on
// a YAML parsing library, and this keeps formatting/comments in the rest
// of the file untouched.
func writeRoutesToYAML(yamlFile string, routes []snapshotRoute) error {
	data, err := os.ReadFile(yamlFile)
	if err != nil {
		return fmt.Errorf("cannot read %s: %w", yamlFile, err)
	}
	lines := strings.Split(string(data), "\n")

	ingressLineIdx := -1
	ingressIndent := 0
	for i, line := range lines {
		if strings.TrimSpace(line) == "ingress:" {
			ingressLineIdx = i
			ingressIndent = len(line) - len(strings.TrimLeft(line, " "))
			break
		}
	}
	if ingressLineIdx == -1 {
		return fmt.Errorf("no 'ingress:' block found in %s — enable ingress on this DSE first", yamlFile)
	}
	childIndent := ingressIndent + 2

	// Find the end of the ingress block: the next line (ignoring blanks/
	// comments) indented at or before ingressIndent.
	end := len(lines)
	for i := ingressLineIdx + 1; i < len(lines); i++ {
		trimmed := strings.TrimSpace(lines[i])
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		indent := len(lines[i]) - len(strings.TrimLeft(lines[i], " "))
		if indent <= ingressIndent {
			end = i
			break
		}
	}

	// Strip any existing "routes:" sub-block within the ingress block.
	var kept []string
	inRoutes := false
	for i := ingressLineIdx + 1; i < end; i++ {
		line := lines[i]
		trimmed := strings.TrimSpace(line)
		indent := len(line) - len(strings.TrimLeft(line, " "))
		if trimmed == "routes:" && indent == childIndent {
			inRoutes = true
			continue
		}
		if inRoutes {
			if trimmed == "" || indent > childIndent || (indent == childIndent && strings.HasPrefix(trimmed, "- ")) {
				continue
			}
			inRoutes = false
		}
		kept = append(kept, line)
	}

	var routesBlock []string
	if len(routes) > 0 {
		ind := strings.Repeat(" ", childIndent)
		routesBlock = append(routesBlock, ind+"routes:")
		for _, r := range routes {
			pathType := r.PathType
			if pathType == "" {
				pathType = "Prefix"
			}
			routesBlock = append(routesBlock,
				ind+"- path: "+r.Path,
				ind+"  pathType: "+pathType,
				ind+"  service: "+r.Service,
				ind+fmt.Sprintf("  port: %d", r.Port),
			)
		}
	}

	var result []string
	result = append(result, lines[:ingressLineIdx+1]...)
	result = append(result, kept...)
	result = append(result, routesBlock...)
	result = append(result, lines[end:]...)

	return os.WriteFile(yamlFile, []byte(strings.Join(result, "\n")), 0644)
}
