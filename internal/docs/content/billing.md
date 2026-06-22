<!-- title: Billing | order: 30 | category: Cloud | tier: Cloud | summary: Per-builder pricing, free stakeholders, and evidence invoices (EE). -->

# Billing

> [!IMPORTANT]
> Billing is an **Enterprise (EE)** feature. Real charging lives in `ee/` (built with `-tags ee`)
> and is also runtime-gated by `billing.enabled`. The OSS build links a no-op stub. Self-hosters can
> run gitstate fully without billing.

![pricing](/shots/pricing.png)

## Per-builder, free stakeholders

Billing is per **builder** — members with role `owner`, `admin`, or `member`. **Stakeholders are
always free** (`org_members.role = stakeholder` never counts toward a seat). This is structural: it's
the seat-tax-killer that per-seat incumbents can't match. See [Concepts → Free stakeholders](/docs/concepts).

## Plan ladder

gitstate bills **per builder** — stakeholders are always free. Managed AI is metered at the
model's **standard rate** (no per-seat AI fee); bring your own key (**BYOK**) for a lower base.

| Plan | Managed / builder | BYOK / builder | Included AI credit | Builders |
|---|---|---|---|---|
| Free | $0 | BYOK | — | ≤ 2 |
| Team | $6 | $3 | $3 / builder | unlimited |
| Business | $14 | $8 | $6 / builder | unlimited |
| Enterprise | custom | custom | custom | unlimited |

```http
GET /api/billing/plans
GET /api/billing/subscription
GET /api/billing/usage
```

## USD billed, ZAR charged

Invoices are computed in USD, then charged in ZAR using the **exchange rate captured at charge time**:

1. The invoice total is computed in USD cents.
2. `exchange.Convert(usdCents)` produces ZAR cents using the freshest cached rate (provider fallback;
   stale-beyond-TTL rates trigger a refresh rather than charging blind).
3. The ZAR amount, the FX rate, and the rate row id are **stamped onto the invoice** so the charge is
   fully auditable — the invoice shows both currencies and the exact rate used.

This protects margin against FX drift. See [Configuration](/docs/configuration) for `EXCHANGE_*` and
`BILLING_CURRENCY_*` settings.

## Evidence invoices with flagged gaps

Invoice lines fall into three kinds:

| Line | Backed by | `is_estimated` |
|---|---|---|
| **Builder seat** | git evidence for that builder in the period | `false` |
| **Usage / metered** | usage events (e.g. LLM cost) | `false` |
| **Unprovable seat** | a builder with **zero git activity** in the period | `true`, with `evidence.confirmation_required = true` |

When git can't prove a builder did billable work, gitstate does **not** silently invent a charge — it
emits an estimated line flagged for human confirmation. The discipline is *under-count rather than
fabricate*, which is what makes the invoice defensible to a client.

![billing & invoices](/shots/billing.png)

```http
GET /api/billing/invoices
GET /api/billing/invoices/{id}
```

## Paystack (EE)

Charging runs through **Paystack** in the EE build:

```http
POST /api/billing/checkout        # creates a draft invoice, stamps ZAR + FX, returns authorization_url
GET  /api/billing/verify/{ref}    # verify a transaction reference
POST /api/billing/webhook         # Paystack webhook (HMAC-SHA512 verified, idempotent)
```

Webhooks are verified with constant-time HMAC-SHA512 over the raw body against `X-Paystack-Signature`
and de-duplicated via `paystack_events` so a redelivery can't double-charge. See
[Security → Webhook verification](/docs/security).

## Viability simulator

`cmd/billsim` models profitability across the plan ladder and a 100 / 1 000 / 10 000-org scenario
sweep, accounting for conversion, churn, LLM COGS, and USD→ZAR FX. LLM cost is the dominant lever; the
tool flags any tier that runs underwater. See [CLI & tools](/docs/cli-and-tools).
