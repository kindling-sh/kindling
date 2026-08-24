package cmd

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/huh"
	"github.com/mattn/go-isatty"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

// ── Non-interactive mode ──────────────────────────────────────────
//
// `kindling snapshot --deploy` needs to be able to run unattended from a
// GH Actions job, where no `huh` form can ever block on stdin.
// isInteractive() is the single source of truth every prompt in this
// command consults before deciding whether to show a form at all:
// explicit --non-interactive always wins, otherwise fall back to
// whether stdin is actually a TTY (defense in depth -- a CI job that
// forgets the flag shouldn't hang against a form indefinitely).
func isInteractive() bool {
	if snapshotNonInteractive {
		return false
	}
	return isatty.IsTerminal(os.Stdin.Fd())
}

// ── Credential config file (--creds-config) ───────────────────────
//
// A committed, non-secret YAML file mapping credential env var names to
// where their staging value actually lives -- almost always a reference
// to an environment variable a CI job already populated from a GH
// Actions secret, never a literal credential value. See
// snapshot-cli-credentials-improvements.md for the full design.

type credsConfigEntry struct {
	FromEnv string `yaml:"fromEnv"`
	Value   string `yaml:"value"`
}

type credsConfigFile struct {
	Credentials map[string]credsConfigEntry `yaml:"credentials"`
}

// resolvedCredsConfig holds credential values already resolved against
// the process environment at load time -- so a fromEnv reference to an
// env var that isn't actually set fails immediately, before any deploy
// work starts, instead of silently surfacing later as a "missing"
// credential (which never fails the deploy).
type resolvedCredsConfig struct {
	values map[string]string // credential env var name -> resolved value
}

func loadCredsConfig(path string) (*resolvedCredsConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("cannot read --creds-config file %q: %w", path, err)
	}

	var raw credsConfigFile
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&raw); err != nil {
		return nil, fmt.Errorf("cannot parse --creds-config file %q: %w", path, err)
	}

	resolved := &resolvedCredsConfig{values: make(map[string]string, len(raw.Credentials))}
	for name, entry := range raw.Credentials {
		hasFromEnv := entry.FromEnv != ""
		hasValue := entry.Value != ""
		switch {
		case hasFromEnv == hasValue:
			return nil, fmt.Errorf("--creds-config: credential %q must set exactly one of fromEnv or value", name)
		case hasFromEnv:
			v := os.Getenv(entry.FromEnv)
			if v == "" {
				return nil, fmt.Errorf("--creds-config: credential %q references fromEnv %q, but that environment variable is not set (or empty) in this process", name, entry.FromEnv)
			}
			resolved.values[name] = v
		default:
			resolved.values[name] = entry.Value
		}
	}
	return resolved, nil
}

// ── Missing-credential handling ────────────────────────────────────

// missingCredential is a credential with no resolvable value anywhere --
// not in --creds-config, and the dev-cluster value itself is empty.
// Never fails the deploy; see writeMissingCredentialsFile.
type missingCredential struct {
	EnvVarName string
	Services   []string
}

// writeMissingCredentialsFile writes outDir/MISSING_CREDENTIALS.md
// listing every missing credential and the two remediation options, so
// a later GH Actions workflow step can check for the file's existence
// (e.g. to post a PR comment) without kindling itself ever failing the
// deploy over a missing credential.
func writeMissingCredentialsFile(outDir string, missing []missingCredential) (string, error) {
	var buf strings.Builder
	buf.WriteString("# Missing staging credentials\n\n")
	buf.WriteString("The credentials below had no value configured in `--creds-config` and\n")
	buf.WriteString("no dev-cluster default was found, so the deploy proceeded without them\n")
	buf.WriteString("(whatever default the chart itself bakes in, if any, is what's actually\n")
	buf.WriteString("running in staging right now).\n\n")
	buf.WriteString("For each one, either:\n\n")
	buf.WriteString("- set it manually in the staging cluster, or\n")
	buf.WriteString("- add it to `--creds-config` and re-run `snapshot --deploy`\n\n")

	for _, m := range missing {
		buf.WriteString(fmt.Sprintf("## %s\n\n", m.EnvVarName))
		buf.WriteString(fmt.Sprintf("Used by: %s\n\n", strings.Join(m.Services, ", ")))
	}

	path := filepath.Join(outDir, "MISSING_CREDENTIALS.md")
	if err := os.WriteFile(path, []byte(buf.String()), 0644); err != nil {
		return "", fmt.Errorf("cannot write %s: %w", path, err)
	}
	return path, nil
}

// ── Registry credentials (username/password) ───────────────────────
//
// Resolution order: explicit flag (username only) -> env var ->
// interactive prompt (human path only) -> error. Deliberately no
// --registry-password flag taking a literal value -- a password passed
// as a plain CLI argument is visible to any other process/user on the
// same host via ps/proc, and would leak into shell history.
func resolveRegistryCredentials(interactive bool) (string, string, error) {
	user := snapshotRegistryUsername
	if user == "" {
		user = os.Getenv("KINDLING_REGISTRY_USERNAME")
	}
	pass := os.Getenv("KINDLING_REGISTRY_PASSWORD")

	if user != "" && pass != "" {
		return user, pass, nil
	}

	if !interactive {
		var missing []string
		if user == "" {
			missing = append(missing, "--registry-username / KINDLING_REGISTRY_USERNAME")
		}
		if pass == "" {
			missing = append(missing, "KINDLING_REGISTRY_PASSWORD")
		}
		return "", "", fmt.Errorf("registry credentials required (non-interactive mode): %s not set", strings.Join(missing, ", "))
	}

	var fields []huh.Field
	if user == "" {
		fields = append(fields, huh.NewInput().Title("Username").Value(&user))
	}
	if pass == "" {
		fields = append(fields, huh.NewInput().
			Title("Password / Token").
			EchoMode(huh.EchoModePassword).
			Value(&pass))
	}
	if len(fields) > 0 {
		form := huh.NewForm(huh.NewGroup(fields...))
		if err := form.Run(); err != nil {
			return "", "", fmt.Errorf("registry credentials cancelled: %w", err)
		}
	}
	return user, pass, nil
}

// ── Ingress selection ────────────────────────────────────────────────
//
// Resolution order: explicit --ingress-services (including an
// explicitly-empty value, meaning "no services get Ingress") ->
// non-interactive default (whatever was already enabled in dev, no
// prompt) -> interactive multi-select (human path only).
func resolveIngressSelection(cmd *cobra.Command, dses []snapshotDSE, interactive bool) ([]string, error) {
	var preSelected []string
	names := make(map[string]bool, len(dses))
	for _, dse := range dses {
		names[dse.Name] = true
		if dse.Ingress != nil && dse.Ingress.Enabled {
			preSelected = append(preSelected, dse.Name)
		}
	}

	if cmd.Flags().Changed("ingress-services") {
		if strings.TrimSpace(snapshotIngressServices) == "" {
			return nil, nil
		}
		var selected []string
		for _, name := range strings.Split(snapshotIngressServices, ",") {
			name = strings.TrimSpace(name)
			if name == "" {
				continue
			}
			if !names[name] {
				return nil, fmt.Errorf("--ingress-services: %q is not one of this cluster's services", name)
			}
			selected = append(selected, name)
		}
		return selected, nil
	}

	if !interactive {
		return preSelected, nil
	}

	if len(dses) == 0 {
		return nil, nil
	}

	options := make([]huh.Option[string], len(dses))
	for i, dse := range dses {
		label := dse.Name
		if dse.Ingress != nil && dse.Ingress.Enabled {
			label += " (ingress in dev)"
		}
		options[i] = huh.NewOption(label, dse.Name)
	}
	selected := append([]string(nil), preSelected...)

	form := huh.NewForm(
		huh.NewGroup(
			huh.NewMultiSelect[string]().
				Title("Which services should be publicly accessible?").
				Description("Selected services will have Ingress enabled.\nUse space to toggle, enter to confirm.").
				Options(options...).
				Value(&selected),
		),
	)
	if err := form.Run(); err != nil {
		return nil, fmt.Errorf("ingress selection cancelled: %w", err)
	}
	return selected, nil
}
