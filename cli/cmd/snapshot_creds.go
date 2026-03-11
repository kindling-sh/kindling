package cmd

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"strings"
	"time"

	"github.com/charmbracelet/huh"
)

// ── Production credential management for snapshot deploy ────────
//
// When deploying a snapshot to production, dependency connection strings
// (DATABASE_URL, REDIS_URL, etc.) default to dev credentials like
// devuser/devpass. This module:
//
//   1. Detects which dependencies have dev-default credentials
//   2. Prompts the user interactively for production connection strings
//   3. Caches credentials locally (AES-256-GCM encrypted) for convenience
//   4. Generates a temporary Helm values override file (never persisted)
//
// The encrypted cache lives at ~/.kindling/prod-credentials/ with 0600
// permissions. The encryption key is derived from the local machine
// identity (hostname + username) — reasonable protection for a localhost
// tool, preventing casual file browsing from exposing secrets.

// prodCredEntry represents one dependency that needs production credentials.
type prodCredEntry struct {
	DepType    string   // "postgres", "redis", etc.
	EnvVarName string   // "DATABASE_URL", "REDIS_URL", etc.
	DevValue   string   // the dev-default connection URL
	Services   []string // which services use this dependency
}

// prodCredCache is the on-disk structure for cached credentials.
type prodCredCache struct {
	Context   string            `json:"context"`
	Chart     string            `json:"chart"`
	Creds     map[string]string `json:"creds"` // EnvVarName → production value
	UpdatedAt time.Time         `json:"updated_at"`
}

// ── Detection ───────────────────────────────────────────────────

// detectDevCredentials scans DSEs for dependency connection strings that
// still have dev-default credentials. Returns deduplicated entries grouped
// by dependency type (e.g. one "postgres" entry listing all services that
// use it).
func detectDevCredentials(chartName string, dses []snapshotDSE) []prodCredEntry {
	type key struct {
		depType string
		envVar  string
	}
	seen := make(map[key]*prodCredEntry)
	var order []key

	for _, dse := range dses {
		for _, dep := range dse.Deps {
			def, ok := depRegistry[dep.Type]
			if !ok {
				continue
			}
			k := key{dep.Type, def.EnvVarName}
			if entry, exists := seen[k]; exists {
				entry.Services = append(entry.Services, dse.Name)
			} else {
				devURL := buildConnectionURL(chartName, dep.Type, helmSafe(dep.Type), def)
				entry := &prodCredEntry{
					DepType:    dep.Type,
					EnvVarName: def.EnvVarName,
					DevValue:   devURL,
					Services:   []string{dse.Name},
				}
				seen[k] = entry
				order = append(order, k)
			}
		}
	}

	var result []prodCredEntry
	for _, k := range order {
		result = append(result, *seen[k])
	}
	return result
}

// detectUserSecrets scans DSEs for env vars that are sourced from K8s
// secrets (secretKeyRef). These need production values because the dev
// cluster secrets won't exist in production.
func detectUserSecrets(dses []snapshotDSE) []prodCredEntry {
	type key struct {
		name string
	}
	seen := make(map[key]*prodCredEntry)
	var order []key

	for _, dse := range dses {
		for _, e := range dse.Env {
			if !e.IsSecret {
				continue
			}
			k := key{e.Name}
			if entry, exists := seen[k]; exists {
				entry.Services = append(entry.Services, dse.Name)
			} else {
				entry := &prodCredEntry{
					DepType:    "secret",
					EnvVarName: e.Name,
					DevValue:   e.Value,
					Services:   []string{dse.Name},
				}
				seen[k] = entry
				order = append(order, k)
			}
		}
	}

	var result []prodCredEntry
	for _, k := range order {
		result = append(result, *seen[k])
	}
	return result
}

// ── Encrypted cache ─────────────────────────────────────────────

func credsCacheDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".kindling", "prod-credentials")
}

func credsCacheFile(context string) string {
	h := sha256.Sum256([]byte(context))
	return filepath.Join(credsCacheDir(), fmt.Sprintf("%x.enc", h[:8]))
}

// deriveEncryptionKey generates a 256-bit key from machine identity.
// This is not HSM-grade security, but prevents casual reading of the
// cached credential file — appropriate for a localhost dev tool.
func deriveEncryptionKey() []byte {
	hostname, _ := os.Hostname()
	u, _ := user.Current()
	username := ""
	if u != nil {
		username = u.Username
	}
	h := sha256.Sum256([]byte(hostname + "|" + username + "|kindling-prod-creds-v1"))
	return h[:]
}

func encryptBytes(plaintext []byte) ([]byte, error) {
	block, err := aes.NewCipher(deriveEncryptionKey())
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	return gcm.Seal(nonce, nonce, plaintext, nil), nil
}

func decryptBytes(ciphertext []byte) ([]byte, error) {
	block, err := aes.NewCipher(deriveEncryptionKey())
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	if len(ciphertext) < gcm.NonceSize() {
		return nil, fmt.Errorf("credential cache corrupted")
	}
	return gcm.Open(nil, ciphertext[:gcm.NonceSize()], ciphertext[gcm.NonceSize():], nil)
}

func loadCredCache(context string) *prodCredCache {
	data, err := os.ReadFile(credsCacheFile(context))
	if err != nil {
		return nil
	}
	plain, err := decryptBytes(data)
	if err != nil {
		return nil // corrupted or key changed — prompt fresh
	}
	var cache prodCredCache
	if err := json.Unmarshal(plain, &cache); err != nil {
		return nil
	}
	return &cache
}

func saveCredCache(context, chart string, creds map[string]string) error {
	cache := prodCredCache{
		Context:   context,
		Chart:     chart,
		Creds:     creds,
		UpdatedAt: time.Now(),
	}
	data, err := json.Marshal(cache)
	if err != nil {
		return err
	}
	enc, err := encryptBytes(data)
	if err != nil {
		return err
	}
	dir := credsCacheDir()
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	return os.WriteFile(credsCacheFile(context), enc, 0600)
}

func clearCredCache(context string) {
	os.Remove(credsCacheFile(context))
}

// ── Interactive prompt ──────────────────────────────────────────

// resolveProductionCredentials is the main entry point for the credential
// flow. It detects dev credentials, checks the cache, prompts the user,
// and returns a map of helm values overrides to apply.
//
// Returns:
//   - overrides: map[valuesKey]map[envVarName]prodValue for helm --set
//   - error if the user cancels or something fails
//
// If the user chooses to skip, overrides is nil (deploy with dev creds).
func resolveProductionCredentials(chartName, context string, dses []snapshotDSE) (map[string]map[string]credOverride, error) {
	entries := detectDevCredentials(chartName, dses)
	secretEntries := detectUserSecrets(dses)
	entries = append(entries, secretEntries...)
	if len(entries) == 0 {
		return nil, nil
	}

	// Show what we found
	fmt.Fprintln(os.Stderr)
	depCount := len(entries) - len(secretEntries)
	if depCount > 0 {
		step("🔐", fmt.Sprintf("Found %d %s with dev credentials",
			depCount, pluralize(depCount, "dependency", "dependencies")))
	}
	if len(secretEntries) > 0 {
		step("🔑", fmt.Sprintf("Found %d %s from K8s secrets",
			len(secretEntries), pluralize(len(secretEntries), "env var", "env vars")))
	}
	for _, e := range entries {
		depLabel := strings.ToUpper(e.DepType[:1]) + e.DepType[1:]
		fmt.Fprintf(os.Stderr, "       %s%s%s → %s (used by %s)\n",
			colorCyan, depLabel, colorReset, e.EnvVarName, strings.Join(e.Services, ", "))
	}
	fmt.Fprintln(os.Stderr)

	// Check cache
	cached := loadCredCache(context)

	// Build choice options
	var choice string
	var options []huh.Option[string]

	if cached != nil {
		cachedCount := 0
		for _, e := range entries {
			if v, ok := cached.Creds[e.EnvVarName]; ok && v != "" {
				cachedCount++
			}
		}
		if cachedCount > 0 {
			options = append(options,
				huh.NewOption[string](
					fmt.Sprintf("Use cached credentials (%d saved, %s)",
						cachedCount, cached.UpdatedAt.Format("Jan 2 15:04")),
					"cached"),
			)
		}
	}
	options = append(options,
		huh.NewOption[string]("Configure production credentials", "configure"),
		huh.NewOption[string]("Deploy with dev credentials (insecure)", "skip"),
	)

	form := huh.NewForm(
		huh.NewGroup(
			huh.NewSelect[string]().
				Title("Production Credentials").
				Description("Dependencies have dev-default passwords (devuser/devpass).\nConfigure production connection strings for a secure deployment.").
				Options(options...).
				Value(&choice),
		),
	)
	if err := form.Run(); err != nil {
		return nil, fmt.Errorf("credential prompt cancelled: %w", err)
	}

	switch choice {
	case "skip":
		warn("Deploying with dev credentials — not recommended for production")
		return nil, nil

	case "cached":
		step("🔑", "Using cached production credentials")
		return buildOverrideMap(entries, dses, cached.Creds), nil

	case "configure":
		creds, err := promptCredentialValues(entries, cached)
		if err != nil {
			return nil, err
		}
		// Save to encrypted cache
		if len(creds) > 0 {
			if err := saveCredCache(context, chartName, creds); err != nil {
				warn(fmt.Sprintf("Could not cache credentials: %v", err))
			} else {
				step("💾", "Credentials cached (encrypted) for future deploys")
			}
		}
		return buildOverrideMap(entries, dses, creds), nil
	}

	return nil, nil
}

// promptCredentialValues shows input fields for each credential.
func promptCredentialValues(entries []prodCredEntry, cached *prodCredCache) (map[string]string, error) {
	// Prepare value pointers for the form
	type fieldRef struct {
		envVar string
		value  *string
	}
	var refs []fieldRef
	var fields []huh.Field

	for _, entry := range entries {
		val := new(string)
		// Pre-fill from cache if available
		if cached != nil {
			if v, ok := cached.Creds[entry.EnvVarName]; ok && v != "" {
				*val = v
			}
		}

		depLabel := strings.ToUpper(entry.DepType[:1]) + entry.DepType[1:]
		desc := fmt.Sprintf("Services: %s\nDev default: %s",
			strings.Join(entry.Services, ", "), truncateStr(entry.DevValue, 72))

		fields = append(fields, huh.NewInput().
			Title(fmt.Sprintf("%s — %s", depLabel, entry.EnvVarName)).
			Description(desc).
			Placeholder("leave blank to use dev default").
			Value(val))

		refs = append(refs, fieldRef{envVar: entry.EnvVarName, value: val})
	}

	form := huh.NewForm(huh.NewGroup(fields...))
	if err := form.Run(); err != nil {
		return nil, fmt.Errorf("credential input cancelled: %w", err)
	}

	// Collect entered values
	creds := make(map[string]string)
	for _, ref := range refs {
		if *ref.value != "" {
			creds[ref.envVar] = *ref.value
		}
	}

	if len(creds) > 0 {
		step("✓", fmt.Sprintf("Configured %d production %s",
			len(creds), pluralize(len(creds), "credential", "credentials")))
	}

	return creds, nil
}

// ── Override map builder ────────────────────────────────────────

// buildOverrideMap converts a flat credential map into the nested structure
// needed for Helm values overrides: valuesKey → envVarName → value.
// credOverride holds a production credential value and whether it's a
// secret (goes into secrets: section) or a plain env var (goes into env:).
type credOverride struct {
	Value    string
	IsSecret bool
}

// Each service that uses a given dependency gets the same credential.
func buildOverrideMap(entries []prodCredEntry, dses []snapshotDSE, creds map[string]string) map[string]map[string]credOverride {
	if len(creds) == 0 {
		return nil
	}

	// Map dep type → envVarName for lookup
	depEnvMap := make(map[string]string) // depType → envVarName
	for _, e := range entries {
		if e.DepType != "secret" {
			depEnvMap[e.DepType] = e.EnvVarName
		}
	}

	// Build a set of secret env var names for direct matching
	secretEnvVars := make(map[string]bool)
	for _, e := range entries {
		if e.DepType == "secret" {
			secretEnvVars[e.EnvVarName] = true
		}
	}

	result := make(map[string]map[string]credOverride)
	for _, dse := range dses {
		vk := helmValuesKey(dse.Name)
		// Dependency connection strings → env:
		for _, dep := range dse.Deps {
			envVar, ok := depEnvMap[dep.Type]
			if !ok {
				continue
			}
			prodVal, ok := creds[envVar]
			if !ok || prodVal == "" {
				continue
			}
			if result[vk] == nil {
				result[vk] = make(map[string]credOverride)
			}
			result[vk][envVar] = credOverride{Value: prodVal, IsSecret: false}
		}
		// User secrets → secrets:
		for _, e := range dse.Env {
			if !e.IsSecret {
				continue
			}
			if !secretEnvVars[e.Name] {
				continue
			}
			prodVal, ok := creds[e.Name]
			if !ok || prodVal == "" {
				continue
			}
			if result[vk] == nil {
				result[vk] = make(map[string]credOverride)
			}
			result[vk][e.Name] = credOverride{Value: prodVal, IsSecret: true}
		}
	}

	return result
}

// writeCredsOverrideFile writes a temporary Helm values file with the
// production credential overrides. Using a file avoids shell escaping
// issues with special characters in connection URLs (commas, @, etc.).
// Returns the path to the temp file (caller should defer os.Remove).
func writeCredsOverrideFile(overrides map[string]map[string]credOverride) (string, error) {
	var buf strings.Builder
	buf.WriteString("# Auto-generated production credential overrides\n")
	buf.WriteString("# This file is ephemeral and deleted after deploy.\n\n")

	for vk, creds := range overrides {
		// Split into env and secrets sections
		var envEntries, secretEntries []struct{ k, v string }
		for envVar, co := range creds {
			escaped := strings.ReplaceAll(co.Value, `\`, `\\`)
			escaped = strings.ReplaceAll(escaped, `"`, `\"`)
			entry := struct{ k, v string }{envVar, escaped}
			if co.IsSecret {
				secretEntries = append(secretEntries, entry)
			} else {
				envEntries = append(envEntries, entry)
			}
		}

		buf.WriteString(fmt.Sprintf("%s:\n", vk))
		if len(envEntries) > 0 {
			buf.WriteString("  env:\n")
			for _, e := range envEntries {
				buf.WriteString(fmt.Sprintf("    %s: \"%s\"\n", e.k, e.v))
			}
		}
		if len(secretEntries) > 0 {
			buf.WriteString("  secrets:\n")
			for _, e := range secretEntries {
				buf.WriteString(fmt.Sprintf("    %s: \"%s\"\n", e.k, e.v))
			}
		}
		buf.WriteString("\n")
	}

	tmpFile, err := os.CreateTemp("", "kindling-creds-*.yaml")
	if err != nil {
		return "", fmt.Errorf("cannot create temp credentials file: %w", err)
	}
	if _, err := tmpFile.WriteString(buf.String()); err != nil {
		tmpFile.Close()
		os.Remove(tmpFile.Name())
		return "", err
	}
	tmpFile.Close()
	// Set restrictive permissions
	os.Chmod(tmpFile.Name(), 0600)
	return tmpFile.Name(), nil
}

// ── Helpers ─────────────────────────────────────────────────────

func truncateStr(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-1] + "…"
}

func pluralize(n int, singular, plural string) string {
	if n == 1 {
		return singular
	}
	return plural
}
