package core

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// LoadConfig holds parameters for a build-load-deploy cycle.
type LoadConfig struct {
	ClusterName string
	Service     string
	Context     string // Docker build context directory
	Dockerfile  string // Path to Dockerfile (optional)
	Namespace   string // defaults to "default"
	NoDeploy    bool   // Build and load only — don't patch the DSE
	Platform    string // Docker build --platform
}

func (c *LoadConfig) namespace() string {
	if c.Namespace == "" {
		return "default"
	}
	return c.Namespace
}

// LoadImageTag returns a deterministic image name:tag for the service.
func LoadImageTag(service string) string {
	ts := time.Now().Unix()
	return fmt.Sprintf("%s:%d", service, ts)
}

// BuildAndLoad builds a Docker image, loads it into Kind, and optionally
// patches the DSE/deployment. Returns a list of status messages.
func BuildAndLoad(cfg LoadConfig) ([]string, error) {
	ns := cfg.namespace()

	// Resolve context
	ctx := cfg.Context
	if ctx == "" {
		ctx = "."
	}
	ctx, err := filepath.Abs(ctx)
	if err != nil {
		return nil, fmt.Errorf("cannot resolve context path: %w", err)
	}
	if _, err := os.Stat(ctx); os.IsNotExist(err) {
		return nil, fmt.Errorf("context directory does not exist: %s", ctx)
	}

	// Resolve Dockerfile
	dockerfile := cfg.Dockerfile
	if dockerfile == "" {
		dockerfile = filepath.Join(ctx, "Dockerfile")
	} else if !filepath.IsAbs(dockerfile) {
		dockerfile = filepath.Join(ctx, dockerfile)
	}
	if _, err := os.Stat(dockerfile); os.IsNotExist(err) {
		return nil, fmt.Errorf("Dockerfile not found: %s", dockerfile)
	}

	if !ClusterExists(cfg.ClusterName) {
		return nil, fmt.Errorf("Kind cluster %q not found — run kindling init first", cfg.ClusterName)
	}

	imageTag := LoadImageTag(cfg.Service)
	var outputs []string

	// 1. Docker build
	dockerArgs := []string{"build", "-t", imageTag, "-f", dockerfile}
	if cfg.Platform != "" {
		dockerArgs = append(dockerArgs, "--platform", cfg.Platform)
	}
	dockerArgs = append(dockerArgs, ctx)

	buildOut, err := RunCapture("docker", dockerArgs...)
	if err != nil {
		return nil, fmt.Errorf("docker build failed: %s", buildOut)
	}
	outputs = append(outputs, "✓ Image built: "+imageTag)

	// 2. Load into Kind
	loadOut, err := RunCapture("kind", "load", "docker-image", imageTag, "--name", cfg.ClusterName)
	if err != nil {
		return nil, fmt.Errorf("kind load failed: %s", loadOut)
	}
	outputs = append(outputs, "✓ Image loaded into cluster")

	// 3. Patch DSE/deployment (optional)
	if cfg.NoDeploy {
		outputs = append(outputs, "⏭ Skipped deploy (no_deploy=true)")
		return outputs, nil
	}

	// Check DSE exists
	if _, err := Kubectl(cfg.ClusterName, "get", "dse", cfg.Service, "-n", ns); err != nil {
		// Try patching deployment directly if no DSE
		patch := fmt.Sprintf(`{"spec":{"template":{"spec":{"containers":[{"name":"%s","image":"%s"}]}}}}`,
			cfg.Service, imageTag)
		patchOut, err := Kubectl(cfg.ClusterName,
			"patch", "deployment", cfg.Service,
			"-n", ns,
			"--type=strategic",
			"-p", patch)
		if err != nil {
			return nil, fmt.Errorf("no DSE or deployment found for %s: %s", cfg.Service, patchOut)
		}
		outputs = append(outputs, "✓ Deployment patched: "+cfg.Service+" → "+imageTag)
	} else {
		// Patch the DSE image
		patch := fmt.Sprintf(`{"spec":{"deployment":{"image":"%s"}}}`, imageTag)
		patchOut, err := Kubectl(cfg.ClusterName,
			"patch", "dse", cfg.Service,
			"-n", ns,
			"--type=merge",
			"-p", patch)
		if err != nil {
			return nil, fmt.Errorf("failed to patch DSE: %s", patchOut)
		}
		outputs = append(outputs, "✓ DSE patched: "+cfg.Service+" → "+imageTag)
	}

	// Wait for rollout
	rollOut, _ := Kubectl(cfg.ClusterName,
		"rollout", "status", "deployment/"+cfg.Service,
		"-n", ns, "--timeout=60s")
	if strings.Contains(rollOut, "successfully rolled out") {
		outputs = append(outputs, "✓ Rollout complete")
	} else {
		outputs = append(outputs, "⚠ Rollout may still be in progress")
	}

	return outputs, nil
}
