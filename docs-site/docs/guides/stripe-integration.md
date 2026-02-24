---
title: Stripe Integration
description: Configure Stripe API keys in your kindling environment.
---

# Stripe Integration

This guide walks through setting up Stripe API keys so your app can
process payments in your local kindling environment.

---

## 1. Store your Stripe keys

```bash
kindling secrets set STRIPE_KEY sk_test_xxxxx
kindling secrets set STRIPE_WEBHOOK_SECRET whsec_xxxxx
```

These are stored as Kubernetes Secrets and backed up locally so they
survive cluster rebuilds.

:::tip
Use your **test mode** keys for local development. Never put live keys
in a dev environment.
:::

---

## 2. Reference in your DSE manifest

If you're writing workflows by hand, add the secrets to your deploy step:

```yaml
- name: Deploy
  uses: kindling-sh/kindling/.github/actions/kindling-deploy@main
  with:
    name: "${{ github.actor }}-my-app"
    image: "registry:5000/my-app:${{ env.TAG }}"
    port: "8080"
    secrets: |
      - STRIPE_KEY
      - STRIPE_WEBHOOK_SECRET
```

If you're using `kindling generate`, it detects `STRIPE_KEY` and
`STRIPE_SECRET_KEY` patterns automatically and includes them in the
generated workflow.

---

## 3. Test webhooks locally

Stripe webhooks need a public URL. Use `kindling expose` to create one:

```bash
kindling expose
# ✅ Public URL: https://random-name.trycloudflare.com
```

Then in the [Stripe Dashboard → Webhooks](https://dashboard.stripe.com/test/webhooks):

1. Click **Add endpoint**
2. Paste your tunnel URL + your webhook path:
   `https://random-name.trycloudflare.com/api/webhooks/stripe`
3. Select the events you want to receive
4. Copy the signing secret and store it:

```bash
kindling secrets set STRIPE_WEBHOOK_SECRET whsec_xxxxx
```

---

## 4. Verify

```bash
# Check secrets are set
kindling secrets list

# Check your app is running
kindling status

# Test the webhook endpoint
curl -X POST https://random-name.trycloudflare.com/api/webhooks/stripe \
  -H "Content-Type: application/json" \
  -d '{"type": "checkout.session.completed"}'
```

---

## After a cluster rebuild

```bash
kindling init
kindling secrets restore   # ← restores STRIPE_KEY + STRIPE_WEBHOOK_SECRET
```

Your Stripe keys are restored automatically from the local backup.
