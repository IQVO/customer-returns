# ADR 0013: every problem type has a replay class; a retryable refusal is not stored

## Status

Accepted (2026-11-19). Adopts the fleet decision of 2026-11-18 (`.claude/rules/fleet/idempotency-and-outbox.md`;
standard and reference test in warehouse-harness-template, `docs/idempotency-replay-classes.md`). It supersedes only
the sentences that say every 4xx answer is stored and replayed: the Idempotency bullet of
[ADR 0010](./0010-fleet-rest-api-guideline.md) and the matching sentences of `apis/openapi.yaml`, the docs and the
rules. Their Decision text is untouched and carries a dated pointer here. The detail texts that say "retry later with
the same `Idempotency-Key`" (`group-not-shipped`, `no-return-destination`, `label-unavailable`;
[ADR 0003](./0003-consumed-contracts-and-local-read-models.md), [ADR 0005](./0005-the-reverse-label-route.md)) were
false until this change and are true with it. ADR 0007 is not reused (it is reserved for the web remote).

## Context

The middleware stored every answer below 500 under the key for 24 hours. A refusal that a retry could pass was
therefore pinned: `422 group-not-shipped` (the `OrderGroupView` lags customer-ordering's facts; its own detail says
"retry later with the same Idempotency-Key") was replayed for a day to the shopper who did exactly that, and so were a
`409 concurrent-update`, a `422 no-return-destination` before the node facts arrived and a `422 label-unavailable`
while the reverse-label route was not ready. Classifying by status cannot fix it (`422` is both
`group-not-shipped` and `authorization-expired`), and "store only 2xx" would lose the one refusal that commits state.

Phase-0 facts of this service (read from the code at develop `2d6f3ac`, 2026-11-19):

- **Store (design S1, a savepoint).** `RequireIdempotencyKey(pool, bind)`
  (`internal/adapters/inbound/http/idempotency.go`): `begin` (:88), the expired-key `DELETE ... created_at < now() -
  TTL` (:94-95, so a key older than 24 h is forgotten before it can replay), `INSERT INTO idempotency_keys ... ON
  CONFLICT (key) DO NOTHING` (:100-104) in the SAME transaction as the business unit of work (the use case's
  `UnitOfWork.Do` joins it through `TxBinder`, `unit_of_work.go:37`), the handler behind `SAVEPOINT handler_run` against
  a capture (`runFreshRequest` :232-314). A 5xx rolls the transaction back, key row included (:258); otherwise the
  savepoint is released (:265), healed when a statement failed on the server (:270), a 4xx KEPT (:275), then `UPDATE` +
  `COMMIT`. TTL 24 h (delete at claim, purge job `cmd/api/wire_background.go:56`). There was no per-response opt-out.
- **Catalogue.** `problemCatalogue` (`errors.go:44-58`), a slice of `{err, problem}`, 10 entries; `problemFor`
  (:62-69); three entries map to `validation-failed`. The middlewares answer three more problems of their own
  (`idempotency-key-*`), `/readyz` one (`dependency-unavailable`), `rejectNUL` one (`not-found` for a NUL in the path)
  and the writer a fallback (`internal-error`): 13 slugs.
- **Writers.** Every refusal on a keyed route goes through `writeError`/`writeProblem` (`response.go`). The only other
  response writers are `writeJSON` (2xx), the replay (`idempotency.go` `replayCachedResponse`) and `flush`.
- **Surfaces.** Three keyed `POST` routes (`server.go`): authorize, cancellations, labels. The MCP server and the A2A
  agent are read-only (ADRs 0006, 0009, 0011): no write tool, no store of their own, nothing to classify there.
- **Writes of a refusal.** In this design a joined unit of work shares the middleware's transaction and the handler
  savepoint is released on a 4xx, so what a use case wrote before it answered a refusal commits WITH the key row.
  OUTCOME therefore needs no new mechanism; only an OUTCOME whose savepoint cannot be released (a statement failed on
  the server, so its writes are gone) must not be stored.

## Decision

1. **Three classes, declared on the catalogue entry.** `problem` (`errors.go`) gains `class` (`replayClass`:
   `FINAL`, `OUTCOME`, `RETRYABLE`, `replay_class.go`). Every `problem` literal of the package lives in `errors.go` and
   declares one (`TestEveryProblemLiteralIsInTheCatalogueAndClassified`); a problem type without a class fails
   `TestEveryProblemTypeDeclaresAReplayClass`; the table below is held equal to the catalogue by
   `TestTheADRClassTableMatchesTheCatalogue` and each class is pinned by `TestEveryProblemTypeHasTheClassTheADRDecided`.
   There is no runtime default.
2. **The writer marks, the middleware reads.** `writeProblem` (so `writeError`) marks the middleware's `capture` with
   the class of the problem it answers. `runFreshRequest` stores a 2xx, never stores a 5xx or a panic, and stores any
   other answer only if its class is FINAL or OUTCOME. It holds no slug and no status range beyond 2xx and 5xx.
3. **RETRYABLE is released exactly as the 5xx branch.** `tx.Rollback` (the key row with it), the answer reaches the
   client without `Idempotency-Replayed`, and the same key executes again. A runtime refusal that declares no class is
   released the same way (a safe fallback).
4. **OUTCOME commits with the key row.** The handler savepoint is released, the answer is stored and the transaction
   commits: the use case's writes and the stored answer are one commit. If the release fails (the writes are gone) the
   OUTCOME is a 500 and the key is released, like a 2xx behind a failed release.
5. **2xx, 5xx and FINAL are byte-identical to before.** The status codes, headers and bodies of every answer are
   unchanged; only the storing of a 4xx became a decision of the class.
6. **Nothing consumer-side or client-side changes.** RELEASED means the same key may be tried again by whoever sent
   it, nothing more. No retry behaviour is added anywhere in this repository.

What a **concurrent duplicate** sees: the second request blocks on the unique index until the first transaction
resolves. When the first ends RETRYABLE (rolled back) the second's insert succeeds and it EXECUTES the command itself
and answers its own result (which may be a success, or the same refusal, released too). When the first ends FINAL,
OUTCOME or 2xx the second replays it. Releasing a RETRYABLE key also means a different body under the same key is a
new request afterwards, not `idempotency-key-reused`: nothing is left to compare against.

### Replay class of every problem type

`FINAL`: a function of the request alone. `OUTCOME`: the command committed state and then refused. `RETRYABLE`: nothing
committed and the answer depends on state that can change (or a 5xx). Line numbers are those of develop `2d6f3ac`.

| Problem type | Status | Class | Why |
|---|---|---|---|
| `idempotency-key-required` | 400 | FINAL | Answered by the middleware before the store is touched (`idempotency.go:126`); a function of the request. Never stored under a key. |
| `idempotency-key-invalid` | 400 | FINAL | Same (`idempotency.go:130`). |
| `idempotency-key-reused` | 409 | FINAL | The store's own answer to a key seen with another method, path or body (`idempotency.go:169`): no handler ran and no key row is written. |
| `validation-failed` | 422 | RETRYABLE | One slug, mixed causes: request-only (`errMalformedRequest` bad JSON, unknown field; `labels.go:23` packageNo) and state-dependent (`commands.go:111` the tracking number the label owner returned; `commands.go:119` the aggregate already holds five tracking numbers; `ports.ErrUnknownLine` against the group view). Mixed slug: RETRYABLE (judgement call 1). |
| `group-not-shipped` | 422 | RETRYABLE | `authorize.go:23-30`: the `OrderGroupView` has no row yet or the group is still PROMISED; customer-ordering's fact arrives later. Nothing was written. The detail says "retry later with the same Idempotency-Key". |
| `no-return-destination` | 422 | RETRYABLE | `decide.go:34`: no ACTIVE returns centre in the region and no origin node known; node-topology and order-sourcing facts arrive later. Raised before any write (`authorize.go:37-43`). |
| `label-unavailable` | 422 | RETRYABLE | `labels.go:35-41`, `labels/client.go:66-120`: the reverse-label route is not configured (the client is OFF in every environment today), not live, or refused (a node it does not know yet). The call is made before the commit; nothing is written. |
| `authorization-expired` | 422 | OUTCOME | `labels.go:29-30`: `expireAndReject` -> `expireLazily` (`queries.go:110-122`) saves the CANCELLED authorization and appends its `ReturnCancelled` outbox row on the request's transaction, THEN the use case answers the refusal. The cancellation commits with the key row. Proven by `TestReplayClass_AuthorizationExpiredCommitsTheCancellationAndIsReplayed` and `TestIssueLabelOnAnExpiredAuthorizationCommitsTheCancellationThenRefuses`. |
| `not-found` | 404 | RETRYABLE | `ports.ErrNotFound` on a keyed route is an authorization id that is unknown (`cancellations`, `labels`: `Authorizations.Get`). Ids are minted here (`authorize.go:46`) and never deleted, so FINAL is arguable; the slug is shared with the NUL-path 404 and the owner rule applies (judgement call 2). |
| `illegal-transition` | 409 | RETRYABLE | `commands.go` `Cancel`/`LinkLabel`: depends on the authorization's state (cancelled, in transit, KEEP, label already linked) which events and other requests change. Raised before any write. |
| `concurrent-update` | 409 | RETRYABLE | `authorization_repository.go:30-37`: a version-guarded save found zero rows (or a lost create race, 23505) after the use case's own one retry (`service.go` `atomically`); another writer committed. Nothing of this request was written. |
| `internal-error` | 500 | RETRYABLE | A 5xx is never stored. |
| `dependency-unavailable` | 503 | RETRYABLE | A 5xx; answered only by `GET /readyz`, which is not a keyed route. Classified so no problem type is unclassified. |

### Judgement calls

1. **`validation-failed` is RETRYABLE.** The brief left the slug to the rule "a slug that mixes request-only and
   state-dependent causes is RETRYABLE". It mixes them (see the row), so a malformed body, which a retry can never fix, is
   released too and re-executes once per retry (it costs one more parse and no write). Splitting the slug so the
   request-only causes become FINAL changes a wire slug and is a recorded follow-up, not part of this change.
2. **`not-found` is RETRYABLE although FINAL is arguable.** Authorization ids are `ret-<uuid>` minted by this service
   and no code deletes a row, which is the case where the fleet standard allows FINAL. It is RETRYABLE because the slug is
   shared (the NUL-path 404 is request-only, the other is a lookup) and "when unsure between FINAL and RETRYABLE choose
   RETRYABLE". The cost is one extra execution of a lookup that fails the same way.
3. **`authorization-expired` is the only OUTCOME**, and only because the use case commits the cancellation before it
   refuses. To make that true in every path `IssueLabel` now answers the SAVE'S OWN error when the lazy expiry cannot be
   saved (`labels.go`): it used to return `errors.Join(ErrAuthorizationExpired, err)`, which the catalogue order mapped
   to `422 authorization-expired` even for a lost race (`concurrent-update`) or a database error, so an answer nothing
   backed would have been stored. Now a lost race is a RETRYABLE 409 and a database error a 500
   (`TestIssueLabelAnswersTheSaveErrorWhenTheLazyExpiryCannotBeSaved`).
4. **The three `idempotency-key-*` problems are FINAL** although nothing is stored under their key: they are written
   before the store is touched and are a function of the request alone.
5. **`label-unavailable` is RETRYABLE although its detail text is imprecise.** The title still says "route is not
   available yet" for a 409/422 refusal of the provider; the text is reported, not changed here.

## Consequences

- A retry under the same key of any RETRYABLE refusal executes again: `group-not-shipped`, `no-return-destination`
  and `label-unavailable` now do what their detail says. `Idempotency-Replayed: true` appears only on a replayed 2xx or a
  replayed `authorization-expired` (no FINAL answer is ever stored: the three FINAL problem types are written before the
  store is touched).
- Per-key storage and the 24-hour window are unchanged (delete at claim, purge job). A released refusal leaves no row.
- Callers: the storefront is the only caller of these routes and a person drives it; whether to retry a released
  refusal stays its decision (`group-not-shipped` and `no-return-destination` say "retry later"). The only outbound
  caller, the reverse-label client, sends the deterministic key `<authorizationId>:<packageNo>` to the shipping
  context; nothing about that changes here, and no retry is added.
- Follow-ups recorded, not done: split `validation-failed`; correct the `label-unavailable` title for a provider
  refusal; the MCP/A2A surfaces get no write tool in this change.
