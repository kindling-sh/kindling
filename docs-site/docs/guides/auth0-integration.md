---
title: Auth0 / OAuth Provider Integration
description: Set up Auth0, Okta, or other OAuth providers with kindling's tunnel support.
---

# Auth0 / OAuth Provider Integration

OAuth and OIDC providers (Auth0, Okta, Firebase Auth, Google OAuth)
require a publicly accessible HTTPS callback URL. Since kindling runs
on `*.localhost`, callbacks fail by default. This guide shows how to
wire it up.

---

## 1. Start a tunnel

```bash
kindling expose
# ✅ Public URL: https://random-name.trycloudflare.com
```

This creates a public HTTPS tunnel from the internet to your local
cluster's ingress controller.

---

## 2. Configure your OAuth provider

### Auth0

In the [Auth0 Dashboard](https://manage.auth0.com):

1. Go to **Applications → Your App → Settings**
2. Set **Allowed Callback URLs**:
   `https://random-name.trycloudflare.com/auth/callback`
3. Set **Allowed Logout URLs**:
   `https://random-name.trycloudflare.com`
4. Set **Allowed Web Origins**:
   `https://random-name.trycloudflare.com`

### Okta

In the [Okta Admin Console](https://developer.okta.com):

1. Go to **Applications → Your App → General**
2. Set **Sign-in redirect URIs**:
   `https://random-name.trycloudflare.com/auth/callback`
3. Set **Sign-out redirect URIs**:
   `https://random-name.trycloudflare.com`

### Google OAuth

In the [Google Cloud Console](https://console.cloud.google.com/apis/credentials):

1. Edit your OAuth 2.0 Client
2. Add to **Authorized redirect URIs**:
   `https://random-name.trycloudflare.com/auth/callback`

---

## 3. Store credentials

```bash
kindling secrets set AUTH0_CLIENT_ID xxxxx
kindling secrets set AUTH0_CLIENT_SECRET xxxxx
kindling secrets set AUTH0_DOMAIN your-tenant.auth0.com
```

---

## 4. Set the callback URL as an env var

Your app needs to know its own public URL for constructing callback
URLs:

```bash
kindling env set myapp-dev \
  AUTH0_CALLBACK_URL=https://random-name.trycloudflare.com/auth/callback \
  APP_URL=https://random-name.trycloudflare.com
```

---

## 5. Verify the flow

1. Open `https://random-name.trycloudflare.com` in your browser
2. Click your login button — you should be redirected to Auth0/Okta
3. After authentication, you should land back at your app via the callback URL

---

## Auto-detection with `kindling generate`

When you run `kindling generate`, it scans your code for OAuth patterns:

- `AUTH0_CLIENT_ID`, `OKTA_CLIENT_ID` in env vars
- `passport`, `next-auth`, `@auth0/nextjs-auth0` in package.json
- `firebase-admin` auth imports

If detected, the generated workflow includes the relevant secrets and
the output suggests running `kindling expose` for callback support.

---

## Tunnel lifecycle

```bash
# Start
kindling expose

# Stop and restore original ingress config
kindling expose --stop
```

The tunnel URL changes each time you restart it. Update your provider's
callback URLs accordingly, or use ngrok with a reserved subdomain for a
stable URL:

```bash
kindling expose --provider ngrok
```
