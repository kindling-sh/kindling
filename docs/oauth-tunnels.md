# OAuth & Public HTTPS Tunnels

Some applications require a publicly accessible HTTPS URL for OAuth/OIDC
callbacks — for example, Auth0, Okta, Firebase Auth, or Google OAuth
need to redirect the browser back to your app after authentication.

Since kindling runs on `*.localhost`, these callbacks fail by default.
`kindling expose` solves this by creating a secure tunnel from a public
HTTPS URL to your local cluster.

---

## Quick start

```bash
# 1. Start the tunnel
kindling expose

# 2. Copy the public URL from the output
#    ✅ Public URL: https://random-name.trycloudflare.com

# 3. Configure your OAuth provider's callback URL:
#    https://random-name.trycloudflare.com/auth/callback

# 4. Store the URL as a secret
kindling secrets set PUBLIC_URL https://random-name.trycloudflare.com

# 5. Push code — the workflow wires PUBLIC_URL into your app
git push origin main
```

---

## How it works

```
Browser → OAuth Provider → redirect to callback URL
                                    ↓
Internet → Tunnel Provider (TLS termination) → localhost:80 → ingress-nginx → App Pod
```

The tunnel provider (Cloudflare or ngrok) handles TLS termination, so
your Kind cluster doesn't need certificates. The public HTTPS URL maps
directly to the ingress controller's port 80 on your machine.

---

## Supported providers

### cloudflared (recommended)

[Cloudflare Tunnel](https://developers.cloudflare.com/cloudflare-one/connections/connect-networks/)
quick tunnels are **free and require no account**.

**Install:**

```bash
# macOS
brew install cloudflare/cloudflare/cloudflared

# Linux
curl -Lo cloudflared https://github.com/cloudflare/cloudflared/releases/latest/download/cloudflared-linux-amd64
chmod +x cloudflared && sudo mv cloudflared /usr/local/bin/
```

**How it works:** `kindling expose` runs `cloudflared tunnel --url http://localhost:80`
which creates a temporary tunnel with a random `*.trycloudflare.com` hostname.
The URL changes each time you restart the tunnel.

### ngrok

[ngrok](https://ngrok.com/) provides stable tunnel URLs but requires a
free account and auth token.

**Install:**

```bash
# macOS
brew install ngrok/ngrok/ngrok

# Linux
curl -s https://ngrok-agent.s3.amazonaws.com/ngrok.asc | sudo tee /etc/apt/trusted.gpg.d/ngrok.asc >/dev/null
echo "deb https://ngrok-agent.s3.amazonaws.com buster main" | sudo tee /etc/apt/sources.list.d/ngrok.list
sudo apt update && sudo apt install ngrok
```

**Setup:**

```bash
# Sign up at https://dashboard.ngrok.com/signup
ngrok config add-authtoken <your-token>
```

**How it works:** `kindling expose` runs `ngrok http 80` and polls the
local ngrok API (`http://localhost:4040/api/tunnels`) for the public URL.

---

## Auto-detection in `kindling generate`

During `kindling generate`, the repo scanner checks source files,
dependency manifests, and environment variables for 40+ OAuth/OIDC
patterns:

### Provider SDKs

| Pattern | Description |
|---|---|
| `auth0` | Auth0 SDK or configuration |
| `okta` | Okta SDK or configuration |
| `firebase/auth`, `firebase-admin` | Firebase Authentication |
| `next-auth`, `@nextauth` | NextAuth.js |
| `passport-oauth`, `passport-google` | Passport.js strategies |
| `clerk` | Clerk authentication |
| `supabase/auth` | Supabase Auth |
| `keycloak` | Keycloak integration |

### Protocol patterns

| Pattern | Description |
|---|---|
| `openid-connect`, `oidc` | OpenID Connect |
| `oauth2` | OAuth 2.0 flow |
| `authorization_code` | OAuth authorization code grant |
| `/callback`, `/auth/callback` | Callback route endpoints |
| `redirect_uri`, `REDIRECT_URI` | OAuth redirect configuration |
| `.well-known/openid-configuration` | OIDC discovery endpoint |

### Environment variables

| Variable | Description |
|---|---|
| `AUTH0_DOMAIN`, `AUTH0_CLIENT_ID` | Auth0 configuration |
| `OKTA_DOMAIN`, `OKTA_CLIENT_ID` | Okta configuration |
| `GOOGLE_CLIENT_ID` | Google OAuth |
| `GITHUB_CLIENT_ID` | GitHub OAuth |
| `NEXTAUTH_URL`, `NEXTAUTH_SECRET` | NextAuth.js |

### CLI output

When OAuth patterns are detected:

```
  🔐 Detected 3 OAuth/OIDC indicator(s) in source code:
       • Auth0 SDK or configuration
       • OAuth callback endpoint
       • Auth0 domain config

  💡 Run kindling expose to create a public HTTPS tunnel for OAuth callbacks
```

The AI also adds a comment to the generated workflow:

```yaml
# NOTE: OAuth detected — run 'kindling expose' for a public HTTPS URL
```

---

## Usage

### Flags

| Flag | Default | Description |
|---|---|---|
| `--tunnel` | auto-detect | `cloudflared` or `ngrok` |
| `--port` | `80` | Local port to expose |

### Auto-detection priority

If `--tunnel` is not specified, kindling checks for available binaries:
1. `cloudflared` (preferred — free, no account)
2. `ngrok`

If neither is found, the command prints install instructions and exits.

### Tunnel lifecycle

```bash
kindling expose
# Tunnel starts...
# Public URL printed and saved to .kindling/tunnel.yaml
# Press Ctrl+C to stop

# On Ctrl+C:
#   - Process receives SIGTERM
#   - 5-second grace period for cleanup
#   - .kindling/tunnel.yaml is deleted
#   - "Tunnel stopped" confirmation
```

### Tunnel info file

While the tunnel is running, `.kindling/tunnel.yaml` contains:

```yaml
# Generated by kindling expose — do not edit
provider: cloudflared
url: https://random-name.trycloudflare.com
created: 2026-02-17T10:30:00-07:00
```

This file is cleaned up when the tunnel stops.

---

## End-to-end OAuth workflow

Here's a complete workflow for an app using Auth0:

```bash
# 1. Bootstrap cluster
kindling init

# 2. Register runner
kindling quickstart -u myuser -r myorg/myapp -t ghp_...

# 3. Generate workflow (OAuth patterns will be detected)
kindling generate -k sk-... -r /path/to/myapp
#   🔐 Detected 4 OAuth/OIDC indicator(s)
#   💡 Run kindling expose to create a public HTTPS tunnel

# 4. Set Auth0 credentials
kindling secrets set AUTH0_DOMAIN myapp.us.auth0.com
kindling secrets set AUTH0_CLIENT_ID abc123
kindling secrets set AUTH0_CLIENT_SECRET def456

# 5. Start tunnel
kindling expose
#   ✅ Public URL: https://random-name.trycloudflare.com

# 6. Configure Auth0 dashboard:
#    Allowed Callback URLs: https://random-name.trycloudflare.com/auth/callback
#    Allowed Logout URLs:   https://random-name.trycloudflare.com
#    Allowed Web Origins:   https://random-name.trycloudflare.com

# 7. Store the public URL
kindling secrets set PUBLIC_URL https://random-name.trycloudflare.com

# 8. Push code — the workflow wires all secrets via secretKeyRef
git push origin main

# 9. Access via the tunnel URL
open https://random-name.trycloudflare.com
```

---

## Limitations

- **cloudflared quick tunnels** generate a new random URL each time.
  You'll need to update your OAuth provider's callback URL after each
  restart. For stable URLs, use a named Cloudflare Tunnel (requires a
  free Cloudflare account).
- **ngrok free tier** also generates random URLs. Stable subdomains
  require a paid plan.
- The tunnel must remain running in a terminal while you're developing.
  Run it in a separate terminal tab or use a terminal multiplexer.
- TLS is handled entirely by the tunnel provider — the Kind cluster
  itself serves plain HTTP via ingress-nginx.
