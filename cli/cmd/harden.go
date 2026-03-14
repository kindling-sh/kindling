package cmd

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

// ── Harden configuration ───────────────────────────────────────

// hardenSeverityLevel controls how aggressive the guardrails are.
type hardenSeverityLevel string

const (
	severityGentle   hardenSeverityLevel = "gentle"
	severityModerate hardenSeverityLevel = "moderate"
	severityStrict   hardenSeverityLevel = "strict"
)

// hardenConfig is the structure of .kindling/harden.yaml.
type hardenConfig struct {
	Severity   hardenSeverityLevel   `yaml:"severity"`
	GateDeploy bool                  `yaml:"gate-deploy"`
	Categories hardenCategoryConfig  `yaml:"categories"`
	Overrides  map[string]string     `yaml:"overrides"` // rule-id → "error"|"warning"|"info"|"off"
}

type hardenCategoryConfig struct {
	Security      bool `yaml:"security"`
	Scalability   bool `yaml:"scalability"`
	Performance   bool `yaml:"performance"`
	Containers    bool `yaml:"containers"`
	Observability bool `yaml:"observability"`
	CIHygiene     bool `yaml:"ci-hygiene"`
}

func defaultHardenConfig() *hardenConfig {
	return &hardenConfig{
		Severity:   severityModerate,
		GateDeploy: false,
		Categories: hardenCategoryConfig{
			Security:      true,
			Scalability:   true,
			Performance:   true,
			Containers:    true,
			Observability: true,
			CIHygiene:     true,
		},
		Overrides: make(map[string]string),
	}
}

// ── Rule types ──────────────────────────────────────────────────

type hardenRuleSeverity string

const (
	ruleSevError   hardenRuleSeverity = "error"
	ruleSevWarning hardenRuleSeverity = "warning"
	ruleSevInfo    hardenRuleSeverity = "info"
)

type hardenCategory string

const (
	catSecurity      hardenCategory = "security"
	catContainers    hardenCategory = "containers"
	catScalability   hardenCategory = "scalability"
	catPerformance   hardenCategory = "performance"
	catObservability hardenCategory = "observability"
	catCIHygiene     hardenCategory = "ci-hygiene"
)

type hardenFinding struct {
	RuleID   string
	Category hardenCategory
	Severity hardenRuleSeverity
	File     string
	Message  string
	Fix      string
}

// ── Command ─────────────────────────────────────────────────────

var hardenCmd = &cobra.Command{
	Use:   "harden",
	Short: "Check production readiness — security, scalability, containers",
	Long: `Scans your codebase for production-readiness issues across security,
container best practices, scalability, performance, and observability.

Guardrails are configurable via .kindling/harden.yaml. Three severity
levels control how aggressive the checks are:

  gentle    Info-only. Print suggestions, never flag errors.
  moderate  Warnings for important issues, errors for critical ones. (default)
  strict    Everything that isn't production-ready is an error.

Examples:
  kindling harden                     # scan with configured severity
  kindling harden --strict            # override to strict for this run
  kindling harden --category security # run only security checks
  kindling harden --init              # create default .kindling/harden.yaml
  kindling harden --fix               # show copy-pasteable fix commands`,
	RunE: runHarden,
}

var (
	hardenRepoPath     string
	hardenStrict       bool
	hardenGentle       bool
	hardenCategoryFlag string
	hardenInit         bool
	hardenFix          bool
)

func init() {
	hardenCmd.Flags().StringVarP(&hardenRepoPath, "repo-path", "r", ".", "Path to the repository to scan")
	hardenCmd.Flags().BoolVar(&hardenStrict, "strict", false, "Override severity to strict for this run")
	hardenCmd.Flags().BoolVar(&hardenGentle, "gentle", false, "Override severity to gentle for this run")
	hardenCmd.Flags().StringVar(&hardenCategoryFlag, "category", "", "Run only a specific category (security, containers, scalability, performance, observability, ci-hygiene)")
	hardenCmd.Flags().BoolVar(&hardenInit, "init", false, "Create a default .kindling/harden.yaml config file")
	hardenCmd.Flags().BoolVar(&hardenFix, "fix", false, "Show copy-pasteable fix commands for each finding")
	rootCmd.AddCommand(hardenCmd)
}

func runHarden(cmd *cobra.Command, args []string) error {
	repoPath, err := filepath.Abs(hardenRepoPath)
	if err != nil {
		return fmt.Errorf("invalid repo path: %w", err)
	}

	if hardenInit {
		return writeDefaultHardenConfig(repoPath)
	}

	if info, err := os.Stat(repoPath); err != nil || !info.IsDir() {
		return fmt.Errorf("repo path does not exist or is not a directory: %s", repoPath)
	}

	// Load config
	cfg := loadHardenConfig(repoPath)

	// Apply CLI overrides
	if hardenStrict {
		cfg.Severity = severityStrict
	} else if hardenGentle {
		cfg.Severity = severityGentle
	}

	fmt.Fprintf(os.Stderr, "\n  %s%s kindling harden %s— %s %s(severity: %s)%s\n\n",
		colorBold, colorCyan, colorReset, repoPath, colorDim, cfg.Severity, colorReset)

	// Scan the repo
	repoCtx, err := scanRepo(repoPath)
	if err != nil {
		return fmt.Errorf("repo scan failed: %w", err)
	}

	// Run all enabled checks
	var findings []hardenFinding

	if shouldRunCategory(cfg, catSecurity) {
		findings = append(findings, hardenCheckSecurity(repoPath, repoCtx, cfg)...)
	}
	if shouldRunCategory(cfg, catContainers) {
		findings = append(findings, hardenCheckContainers(repoCtx, cfg)...)
	}
	if shouldRunCategory(cfg, catScalability) {
		findings = append(findings, hardenCheckScalability(repoCtx, cfg)...)
	}
	if shouldRunCategory(cfg, catPerformance) {
		findings = append(findings, hardenCheckPerformance(repoCtx, cfg)...)
	}
	if shouldRunCategory(cfg, catObservability) {
		findings = append(findings, hardenCheckObservability(repoCtx, cfg)...)
	}
	if shouldRunCategory(cfg, catCIHygiene) {
		findings = append(findings, hardenCheckCIHygiene(repoPath, repoCtx, cfg)...)
	}

	// Apply config overrides and severity level
	findings = applyOverrides(findings, cfg)

	// Print results
	printHardenResults(findings, cfg)

	return nil
}

// ── Config loading ──────────────────────────────────────────────

func loadHardenConfig(repoPath string) *hardenConfig {
	cfgPath := filepath.Join(repoPath, ".kindling", "harden.yaml")
	data, err := os.ReadFile(cfgPath)
	if err != nil {
		return defaultHardenConfig()
	}
	cfg := defaultHardenConfig()
	if err := yaml.Unmarshal(data, cfg); err != nil {
		warn(fmt.Sprintf("Invalid harden config at %s — using defaults", cfgPath))
		return defaultHardenConfig()
	}
	if cfg.Overrides == nil {
		cfg.Overrides = make(map[string]string)
	}
	return cfg
}

func writeDefaultHardenConfig(repoPath string) error {
	dir := filepath.Join(repoPath, ".kindling")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("cannot create .kindling directory: %w", err)
	}

	cfgPath := filepath.Join(dir, "harden.yaml")
	if _, err := os.Stat(cfgPath); err == nil {
		warn(fmt.Sprintf("%s already exists", cfgPath))
		return nil
	}

	content := `# kindling harden — production readiness configuration
#
# Severity levels:
#   gentle    — info-only, no errors or warnings
#   moderate  — warnings for important issues, errors for critical ones (default)
#   strict    — everything that isn't production-ready is an error
severity: moderate

# Block 'kindling deploy' when harden finds errors
gate-deploy: false

# Enable/disable check categories
categories:
  security: true
  scalability: true
  performance: true
  containers: true
  observability: true
  ci-hygiene: true

# Per-rule severity overrides (rule-id: error|warning|info|off)
# overrides:
#   no-root-container: error
#   pin-base-images: off
`

	if err := os.WriteFile(cfgPath, []byte(content), 0644); err != nil {
		return fmt.Errorf("cannot write config: %w", err)
	}

	success(fmt.Sprintf("Created %s", cfgPath))
	return nil
}

// ── Category gating ─────────────────────────────────────────────

func shouldRunCategory(cfg *hardenConfig, cat hardenCategory) bool {
	// CLI --category flag overrides config
	if hardenCategoryFlag != "" {
		return hardenCategoryFlag == string(cat)
	}

	switch cat {
	case catSecurity:
		return cfg.Categories.Security
	case catContainers:
		return cfg.Categories.Containers
	case catScalability:
		return cfg.Categories.Scalability
	case catPerformance:
		return cfg.Categories.Performance
	case catObservability:
		return cfg.Categories.Observability
	case catCIHygiene:
		return cfg.Categories.CIHygiene
	}
	return true
}

// ── Override application ────────────────────────────────────────

func applyOverrides(findings []hardenFinding, cfg *hardenConfig) []hardenFinding {
	var result []hardenFinding
	for _, f := range findings {
		// Check per-rule override
		if override, ok := cfg.Overrides[f.RuleID]; ok {
			if override == "off" {
				continue
			}
			f.Severity = hardenRuleSeverity(override)
		}

		// Apply severity level: gentle demotes everything to info
		if cfg.Severity == severityGentle {
			f.Severity = ruleSevInfo
		}

		// Strict promotes warnings to errors
		if cfg.Severity == severityStrict && f.Severity == ruleSevWarning {
			f.Severity = ruleSevError
		}

		result = append(result, f)
	}
	return result
}

// ── Output formatting ───────────────────────────────────────────

func printHardenResults(findings []hardenFinding, cfg *hardenConfig) {
	if len(findings) == 0 {
		fmt.Fprintf(os.Stderr, "  %s✅ No production readiness issues found — nice work%s\n\n", colorGreen, colorReset)
		return
	}

	// Group by category
	categoryOrder := []hardenCategory{catSecurity, catContainers, catScalability, catPerformance, catObservability, catCIHygiene}
	categoryNames := map[hardenCategory]string{
		catSecurity:      "Security",
		catContainers:    "Container Best Practices",
		catScalability:   "Scalability",
		catPerformance:   "Performance",
		catObservability: "Observability",
		catCIHygiene:     "CI Hygiene",
	}

	errorCount, warnCount, infoCount := 0, 0, 0

	for _, cat := range categoryOrder {
		var catFindings []hardenFinding
		for _, f := range findings {
			if f.Category == cat {
				catFindings = append(catFindings, f)
			}
		}
		if len(catFindings) == 0 {
			continue
		}

		fmt.Fprintf(os.Stderr, "  %s%s%s%s\n", colorBold, colorCyan, categoryNames[cat], colorReset)

		for _, f := range catFindings {
			var prefix string
			switch f.Severity {
			case ruleSevError:
				prefix = fmt.Sprintf("  %s❌%s", colorRed, colorReset)
				errorCount++
			case ruleSevWarning:
				prefix = fmt.Sprintf("  %s⚠️ %s", colorYellow, colorReset)
				warnCount++
			case ruleSevInfo:
				prefix = fmt.Sprintf("  %sℹ️ %s", colorCyan, colorReset)
				infoCount++
			}

			msg := f.Message
			if f.File != "" {
				msg = fmt.Sprintf("%s — %s", f.File, f.Message)
			}
			fmt.Fprintf(os.Stderr, "%s %s\n", prefix, msg)

			if hardenFix && f.Fix != "" {
				fmt.Fprintf(os.Stderr, "     %s→ %s%s\n", colorDim, f.Fix, colorReset)
			}
		}
		fmt.Fprintln(os.Stderr)
	}

	// Summary
	if errorCount > 0 {
		fmt.Fprintf(os.Stderr, "  %s%d error(s)%s", colorRed, errorCount, colorReset)
	}
	if warnCount > 0 {
		if errorCount > 0 {
			fmt.Fprint(os.Stderr, ", ")
		} else {
			fmt.Fprint(os.Stderr, "  ")
		}
		fmt.Fprintf(os.Stderr, "%s%d warning(s)%s", colorYellow, warnCount, colorReset)
	}
	if infoCount > 0 {
		if errorCount > 0 || warnCount > 0 {
			fmt.Fprint(os.Stderr, ", ")
		} else {
			fmt.Fprint(os.Stderr, "  ")
		}
		fmt.Fprintf(os.Stderr, "%s%d suggestion(s)%s", colorCyan, infoCount, colorReset)
	}
	fmt.Fprintln(os.Stderr)

	if errorCount > 0 && cfg.GateDeploy {
		fmt.Fprintf(os.Stderr, "\n  %s❌ Deploy would be blocked — %d error(s). Fix and retry, or set gate-deploy: false%s\n", colorRed, errorCount, colorReset)
	}

	fmt.Fprintln(os.Stderr)
}
