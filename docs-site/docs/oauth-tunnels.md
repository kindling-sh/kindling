---
sidebar_position: 8
title: OAuth & Tunnels
description: Setting up public HTTPS tunnels for OAuth callbacks with kindling expose.
---

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
Internet → Tunnel Provider (TLS termination) → localhost:80 → Traefik → App Pod
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

### ngrok

[ngrok](https://ngrok.com/) provides stable tunnel URLs but requires a
free account and auth token.

**Install:**

```bash
# macOS
brew install ngrok/ngrok/ngrok
```

**Setup:**

```bash
ngrok config add-authtoken <your-token>
```

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

---

## End-to-end OAuth workflow

Here's a complete workflow for an app using Auth0:

```bash
# 1. Bootstrap cluster
kindling init

# 2. Register runner
kindling runners -u myuser -r myorg/myapp -t ghp_...

# 3. Generate workflow (OAuth patterns will be detected)
kindling generate -k sk-... -r /path/to/myapp

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

# 8. Push code
git push origin main

# 9. Access via the tunnel URL
open https://random-name.trycloudflare.com
```

---

## Multi-service tunnels

A single `kindling expose` tunnel routes to the ingress controller, which
means **all your services are exposed through one public URL** — the ingress
controller handles routing based on hostnames and paths.

### How it works

When you run `kindling expose`, the tunnel connects to `localhost:80`
(the ingress controller). Since all your services have their own ingress
rules (e.g. `alice-api.localhost`, `alice-ui.localhost`), the tunnel
automatically patches the active ingress with the tunnel's public hostname.

For **multi-service apps** with separate frontend and backend ingresses,
you can target a specific ingress with `--service`:

```bash
# Expose the frontend ingress (e.g. for OAuth callbacks)
kindling expose --service alice-ui
```

The tunnel patches that ingress's host to the public URL, while your
backend services remain accessible internally via cluster DNS
(`http://alice-api:8080`).

### Example: Frontend + API with OAuth

A common pattern is a React/Next.js frontend that handles OAuth callbacks,
talking to an API backend:

```
Internet → tunnel → Traefik → alice-ui (handles /auth/callback)
                                   ↘ alice-api (internal only, no tunnel needed)
```

```bash
# 1. Both services are deployed via git push
git push origin main

# 2. Expose just the frontend for OAuth callbacks
kindling expose --service alice-ui
#   ✅ Public URL: https://random-name.trycloudflare.com
#   ✅ Patched ingress alice-ui → random-name.trycloudflare.com

# 3. Configure your OAuth provider's callback:
#    https://random-name.trycloudflare.com/auth/callback

# 4. The frontend calls the API internally:
#    API_URL=http://alice-api:8080 (cluster DNS, no tunnel needed)
```

### When you need both services exposed

If your API also needs to be publicly reachable (e.g. for webhook
receivers or mobile app backends), you have two options:

**Option A: Path-based routing** — Route both through a single ingress
using path prefixes (`/api/*` → backend, everything else → frontend).
This works with a single tunnel.

**Option B: Multiple tunnels** — Run a second tunnel on a different port:

```bash
# Tunnel 1: frontend on port 80 (default ingress)
kindling expose --service alice-ui

# Tunnel 2: direct to the API service port
kindling expose --port 8080 --tunnel ngrok
```

:::tip
Most apps only need the frontend exposed for OAuth. Backend services
communicate over cluster DNS (`http://<name>:<port>`) and don't need
a public URL.
:::

---

## Limitations

- **cloudflared quick tunnels** generate a new random URL each time.
  You'll need to update your OAuth provider's callback URL after each
  restart. For stable URLs, use a named Cloudflare Tunnel (requires a
  free Cloudflare account).
- **ngrok free tier** also generates random URLs. Stable subdomains
  require a paid plan.
- The tunnel must remain running in a terminal while you're developing.
- TLS is handled entirely by the tunnel provider — the Kind cluster
  itself serves plain HTTP via Traefik.
