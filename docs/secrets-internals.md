# Secrets — Internal Reference

This document describes the complete secret flow: CLI commands,
K8s Secret creation, naming conventions, local persistence, dual-name
resolution, and the push pre-flight system.

Source: `cli/cmd/secrets.go`, `cli/core/secrets.go`, `cli/cmd/push.go`

---

## Secret categories

kindling manages two distinct categories of secrets:

### 1. User-managed secrets (external)

Secrets the user provides — API keys, OAuth tokens, database
credentials for external services.

```bash
kindling secrets set OPENAI_API_KEY sk-abc123
kindling secrets set GITHUB_TOKEN ghp_xyz789
```

These are stored as K8s Secrets and referenced in CI workflows via
`secretKeyRef`:

```yaml
env:
  - name: OPENAI_API_KEY
    valueFrom:
      secretKeyRef:
        name: openai-api-key
        key: OPENAI_API_KEY
```

### 2. Dependency credentials (auto-managed)

Secrets the operator creates for dependency connections — database
passwords, cache credentials. These are **never** managed by the
user directly. See [Dependencies — Credential management](dependencies.md#credential-management).

---

## CLI flow

### `kindling secrets set KEY VALUE`

```
1. Sanitize key → K8s secret name
   "OPENAI_API_KEY" → "openai-api-key"

2. Create K8s Secret
   kubectl create secret generic openai-api-key \
     --from-literal=OPENAI_API_KEY=sk-abc123 \
     --dry-run=client -o yaml | kubectl apply -f -

3. Label the secret
   kubectl label secret openai-api-key \
     app.kubernetes.io/managed-by=kindling

4. Persist to local store
   Write to .kindling/secrets.yaml

5. Print confirmation
   ✅ Secret OPENAI_API_KEY set
```

### `kindling secrets list`

```
1. kubectl get secrets -l app.kubernetes.io/managed-by=kindling
2. Print table:
   NAME              KEY              AGE
   openai-api-key    OPENAI_API_KEY   2h
   github-token      GITHUB_TOKEN     1d
```

Values are **never** displayed in the list output.

### `kindling secrets delete KEY`

```
1. Resolve name (dual-name check)
2. kubectl delete secret <name>
3. Remove from .kindling/secrets.yaml
4. Print confirmation
```

### `kindling secrets export`

```
1. Read all kindling-managed secrets from cluster
2. Decode values
3. Write to specified file (default: .kindling/secrets-export.yaml)
```

**Warning:** Export produces plaintext values. The file should be
`.gitignore`d and handled carefully.

### `kindling secrets import`

```
1. Read YAML file
2. For each entry: kindling secrets set KEY VALUE
3. Print summary
```

---

## Naming convention

### Key → Secret name

```go
func secretName(key string) string {
    name := strings.ToLower(key)
    name = strings.ReplaceAll(name, "_", "-")
    // K8s DNS-1123 label rules
    name = regexp.MustCompile(`[^a-z0-9-]`).ReplaceAllString(name, "-")
    name = strings.Trim(name, "-")
    if len(name) > 253 {
        name = name[:253]
    }
    return name
}
```

Examples:
| Key | Secret name |
|---|---|
| `DATABASE_URL` | `database-url` |
| `OPENAI_API_KEY` | `openai-api-key` |
| `S3_ACCESS_KEY_ID` | `s3-access-key-id` |
| `MyCustomKey` | `mycustomkey` |

### Data key

The Secret's `.data` map uses the **original key name** (not sanitized):

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: openai-api-key          # sanitized
data:
  OPENAI_API_KEY: c2stYWJj...   # original key name
```

This allows `secretKeyRef` to reference the human-readable key name.

---

## Dual-name resolution

Historically, some secrets may have been created with the original
(unsanitized) key as the secret name. The lookup system checks both:

```go
func getSecret(key string) (*v1.Secret, error) {
    // Try sanitized name first (current convention)
    if s, err := kubectl("get", "secret", secretName(key)); err == nil {
        return s, nil
    }
    // Fall back to original key name (legacy)
    if s, err := kubectl("get", "secret", key); err == nil {
        return s, nil
    }
    return nil, fmt.Errorf("secret %q not found", key)
}
```

This ensures backward compatibility while migrating to the sanitized
naming convention.

---

## Local persistence

### `.kindling/secrets.yaml`

All secrets are also saved locally for recovery after cluster teardown:

```yaml
# .kindling/secrets.yaml
# DO NOT COMMIT — contains sensitive values
secrets:
  OPENAI_API_KEY: sk-abc123
  GITHUB_TOKEN: ghp_xyz789
  DATABASE_URL: postgresql://external-host:5432/prod
```

### Why local persistence?

`kindling destroy` deletes the Kind cluster and all K8s resources,
including Secrets. Without local persistence, users would have to
re-enter every secret after `kindling init`.

The flow after cluster recreation:

```
kindling destroy
kindling init
kindling secrets import    ← reads .kindling/secrets.yaml
```

Or, if auto-restore is enabled:

```
kindling destroy
kindling init             ← auto-detects .kindling/secrets.yaml, restores
```

### `.gitignore` handling

`kindling init` adds `.kindling/secrets.yaml` and
`.kindling/secrets-export.yaml` to `.gitignore` if not already present.

---

## Push pre-flight — secret checking

`kindling push` checks that all secrets referenced in the CI workflow
exist in the cluster before pushing:

### How it works

```go
func checkSecretsExist(workflowFile string) error {
    content, _ := os.ReadFile(workflowFile)

    // Extract all secretKeyRef names from the workflow
    re := regexp.MustCompile(`secretKeyRef:\s*\n\s*name:\s*(\S+)`)
    matches := re.FindAllStringSubmatch(string(content), -1)

    var missing []string
    seen := make(map[string]bool)

    for _, match := range matches {
        name := match[1]
        if seen[name] {
            continue
        }
        seen[name] = true

        // Check if secret exists in cluster
        _, err := core.RunKubectlSilent("get", "secret", name)
        if err != nil {
            missing = append(missing, name)
        }
    }

    if len(missing) > 0 {
        return fmt.Errorf(
            "missing secrets in cluster:\n%s\n\nRun:\n%s",
            formatMissing(missing),
            formatSetCommands(missing),
        )
    }
    return nil
}
```

### Why pre-flight?

Without this check, the user would:
1. `git push`
2. Wait for CI to start (~10s)
3. Wait for build to complete (~30-60s)
4. See the deploy step fail because a secret doesn't exist
5. Set the secret
6. Push again

With pre-flight, the error appears immediately:

```
❌ Missing secrets in cluster:

  SECRET NAME        WORKFLOW REFERENCE
  openai-api-key     deploy step, line 45
  stripe-api-key     deploy step, line 52

Run:
  kindling secrets set OPENAI_API_KEY <value>
  kindling secrets set STRIPE_API_KEY <value>
```

---

## Secret labels

All kindling-managed secrets are labeled:

```yaml
metadata:
  labels:
    app.kubernetes.io/managed-by: kindling
    app.kubernetes.io/part-of: kindling-secrets
```

This allows:
- `kindling secrets list` to filter efficiently
- Dashboard to display only kindling secrets
- Cleanup during `kindling reset`
