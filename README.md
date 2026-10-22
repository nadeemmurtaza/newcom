# newcom

Four services currently bundled in one repo:

- **`esp`** (Go, internally codenamed `nmail`) — the email engine: SMTP,
  DKIM, deliverability, journeys, A/B testing, billing. Already implements
  "Login with nID" correctly (`esp/nid_auth.go`) — this is the ecosystem's
  reference OAuth-client integration.
- **`collab-service`** (Node) — real-time collaborative documents (Yjs
  CRDT + Redis).
- **`web`** — the Vite/React frontend, intended to become the unified
  workspace shell.
- **`campaign-service`** (Java) — campaign sending, currently duplicated
  with similar logic in `erp-1` and `Communicator`.

## Part of the NEWAX ecosystem

See [`ECOSYSTEM.md`](ECOSYSTEM.md) for what each of these services owns and
the rules for talking to the rest of the ecosystem. **These four services
are slated to split into separate repos** — different languages, unrelated
products, no shared deploy lifecycle. See `ECOSYSTEM.md` and
`core-newax/docs/ecosystem/MODULE-AUDIT-AND-ROADMAP.md` for the reasoning
and the phased plan.
