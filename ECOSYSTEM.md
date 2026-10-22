# NEWAX Ecosystem

This repo is one of nine that make up one connected NEWAX ecosystem. This
file exists so anyone working in any one of them knows what the others are,
what belongs here vs. there, and the rules that keep them decoupled. It is
copied verbatim into every repo listed below — if you're editing it, edit it
everywhere, or better, edit it in `core-newax` and re-sync.

Full audit and roadmap this file is built from:
`core-newax/docs/ecosystem/MODULE-AUDIT-AND-ROADMAP.md` (see that doc for the
evidence behind every claim here, and the phased plan to get from today's
state to this target state).

## The one rule

**No repo calls another repo directly.** All inter-module communication goes
through the Module Gateway (synchronous calls) or the event bus
(asynchronous, fire-and-forget) — both live in `core-newax`. If you're about
to add an HTTP call from this repo straight to another repo's API, stop:
register the call through the gateway instead, or publish/subscribe an event.

## Identity: nID is the one login

**nID is the single identity provider for the whole ecosystem** — login,
MFA/2FA, OAuth sessions, and the secrets vault (every service's API keys and
env-level secrets, scoped per-service, fetched at boot, never committed to a
repo). `newcom/esp` (internally `nmail`) already implements this correctly
(`esp/nid_auth.go` — the authorization-code exchange, one nID Account to one
local `users` row via `nid_account_id`). **That file is the reference
implementation.** Every other repo wires in the same way, not a reinvention.

`core-newax` does **not** run a competing identity system. Its own
`packages/auth`, `users`, `organizations`, `memberships`, `roles`,
`permissions`, `tenants` packages are being deprecated/reduced to a thin
client library (`@newax/nid-client`) that wraps nID's OAuth and session
validation — see Phase 0 of the roadmap.

## core-newax: what actually goes there

Two tests, both must be true, not just one:

1. **Shared or public, not private.** Data or a module goes in `core-newax`
   only if it's genuinely shared across domains, or public (not scoped to
   one organization, tenant, or individual). A tenant's private order, a
   contact's private conversation — those stay in the domain that owns
   them, no matter how generic the table looks.
2. **Build once, reuse everywhere.** If two or more domains would otherwise
   each build the same capability, it's built once in `core-newax` and
   everyone calls it. "Might be useful later" doesn't qualify — the bar is
   an actual second domain that needs it now.

Everything else — even things that feel foundational — stays in the domain
that owns it.

## The repos and what each one owns

| Repo | Owns | Must never own |
|---|---|---|
| **nID** | Login, 2FA, OAuth sessions, secrets vault | Any business data |
| **core-newax** | Module registry, Module Gateway, event bus, shared/public data, build-once-reuse capabilities, cross-domain read-model for BI | Identity (→ nID), any domain's private business data |
| **erp-1 (Yaseen)** | Catalog, orders, fulfillment, inventory, finance/accounting, HR | CRM/support/conversations, marketing campaigns, identity verification, its own user model, AI/RAG |
| **Communicator** | Conversations, messages, drafts, automation rules, knowledge base, broadcasts | RBAC/SSO, billing, generic ticketing, a generic webhooks engine, its own email-sending infra |
| **newcom/esp (nmail)** | All outbound email: sending, deliverability, DKIM, journeys, A/B testing | Campaign strategy/segmentation (that's the calling domain's job) |
| **newcom/collab-service** | Real-time collaborative documents | Customer-facing messaging |
| **newcom/web** | The unified workspace shell: login, module launcher | Any domain's business logic |
| **harvester** | Lead sourcing and qualification | Outreach/messaging |
| **AegisDesk** | On-device offline automation | Any networked ecosystem integration, unless its own "no Internet" design changes |

## Known misplacements being migrated (see roadmap for phases)

- `erp-1`: `customers/crm,support,notify,onsitemessages` → Communicator's
  territory. `merchandising/marketing` → resolves a 3-way duplicate with
  `newcom/campaign-service` and Communicator's broadcasts. `system/identity-
  verification` and `system/users` → nID. `system/rag` → likely
  Communicator (tied to its conversational AI).
- `Communicator`: strip RBAC, SSO/SAML, Stripe billing, generic ticketing/
  SLA, and the generic outgoing-webhooks engine it grew outside this
  ecosystem's boundaries — these belong to nID, erp-1, and core-newax's
  Module Gateway respectively.
- `newcom`: split `esp`, `collab-service`, and `web` into separate repos —
  different languages, unrelated products, no shared deploy lifecycle.
  `campaign-service` folds into whichever repo wins the campaign-engine
  duplicate above.

## Governing documents

- `core-newax/docs/architecture/core-architecture.md` — the four-layer model
  (Foundation → Platform Services → Business Modules → Client Solutions).
- `core-newax/docs/architecture/central-registry-architecture.md` — what
  belongs in the shared identity/org foundation.
- `core-newax/docs/decisions/0001`–`0034` — the ADRs. 0001 (modular monolith
  first) governs when to split a repo into more than one. 0005 (event-driven
  module communication) is what the Module Gateway implements for real.
- `core-newax/registry/module-registry.json` — the live list of every
  module, its events, dependencies, and database ownership.
