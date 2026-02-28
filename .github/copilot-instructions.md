# Copilot Instructions

## Active Feature Branches

### Provider Abstraction (`provider-abstraction` branch)

All work on the CI provider abstraction feature **must** be done on the
`provider-abstraction` branch. This includes:

- Changes to `pkg/ci/` (interfaces, registry, provider implementations)
- Refactoring operator controller or CLI code to use `ci.Default()`
- Adding new provider implementations (e.g. GitLab CI)
- Updating docs related to the provider abstraction

Do **not** merge provider abstraction changes into `main` or `fuzz`
until the feature is complete and tested.

### Intel Auto-Lifecycle (`feat/intel-auto-lifecycle` branch)

All work on the `kindling intel` automatic lifecycle feature **must** be
done on this branch. The feature gives coding agents (Copilot, Claude Code,
Cursor, Windsurf) full kindling context automatically.

**How it works:**

- `ensureIntel()` runs via `PersistentPreRun` on `rootCmd` before every
  CLI command (skips `intel on/off/status`, `version`, `help`, `completion`).
- First kindling command in a repo silently backs up existing agent config
  files to `.kindling/intel-backups/` and replaces them with a focused
  kindling context document.
- Each command touches a `last_interaction` timestamp in
  `.kindling/intel-state.json`.
- After 1 hour of inactivity (`intelSessionTimeout`), the next command
  restores originals, then re-activates with a fresh backup.
- `kindling intel off` restores originals and writes `.kindling/intel-disabled`
  to prevent auto-reactivation. `kindling intel on` clears that flag.

**Key files:**

| File | Role |
|---|---|
| `cli/cmd/intel.go` | All intel logic: commands, lifecycle, context doc builder, agent formatting |
| `cli/cmd/intel_test.go` | 14 tests covering timestamp, staleness, restore, disabled flag, backup dedup, command filtering |
| `cli/cmd/root.go` | `PersistentPreRun` hook calling `ensureIntel(cmd)` |
| `cli/cmd/status.go` | Agent Intel section in `kindling status` output |
| `cli/cmd/generate.go` | Writes `.kindling/context.md` after workflow generation |

**Key functions in `intel.go`:**

- `ensureIntel(cmd)` — auto-lifecycle hook (disable check → stale check → activate/touch)
- `activateIntel(repoRoot, verbose)` — backup + write context to all agent files
- `restoreIntel(repoRoot, state)` — restore backups, remove created files, clean up
- `buildContextDocument(repoRoot)` — generates the 4-section context markdown
- `shouldSkipIntel(cmd)` — filters commands that shouldn't trigger auto-intel

**Remaining work:**

- Wire `kindling destroy` to call `restoreIntel()` during cluster teardown
- Add `.kindling/intel-*` patterns to `.gitignore` generation
- Consider surfacing intel activation as a one-line notice on first silent activation
- Update docs-site with intel documentation page
