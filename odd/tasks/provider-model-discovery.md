# Provider model discovery

Objective: Fetch actual models for each saved provider account, manually and after adding a key.
Branch: feat/provider-model-cache
Trigger: risk item 2 (server-side outbound requests carrying saved provider credentials).
Scope in: Account-card model discovery, automatic discovery after successful account creation, modal presentation, and persistent per-account model cache in the credential JSON.
Scope out: Changing inference routing, model registration contracts, or existing credential fields.
Rollback: Revert the feature work-unit commit.
Success: Each account can fetch and display its actual models; adding a key initiates that same fetch.

## Specs

### S1 — Manual model discovery

> "hay que agregar boton para obtener los modelos de cada provider agregado en su respectiva card"

- Each account card MUST offer a model-fetch action for its saved provider/account and display the resulting model identifiers.
- **S1.1** Given saved accounts, when fetching models from one account card, then a provider request uses that account and its returned models appear in that card.

### S2 — Automatic model discovery

> "y tambien hacer un fetch de los modelos cuandos se agrega una clave"

- After successfully adding a key the UI MUST initiate model discovery for the newly saved account.
- **S2.1** Given a successful key submission, when account creation completes, then discovery runs for the created account and its models appear without a manual click.

### S3 — Model list modal

> "vale al mostrar la lista de modelos debe ser en un modal"

- Model results MUST be displayed in a modal instead of expanded inline in an account card.
- **S3.1** Given an account, when opening its model list, then a modal identifies the account and displays its models; closing the modal returns to the unchanged cards.
- **S3.2** Given requests for different accounts, when responses arrive, then the visible modal MUST NOT show another account's models.

### S4 — Persistent credential-file cache

> "y se debe actualizar el archivo del auth.json donde se guardan las credenciales para poner la lista de los modelos y tenerlos como cache"

- Successful model discovery MUST save the model list in that account's existing credential JSON, preserving credentials and unrelated fields.
- Opening saved models MUST use persisted cache when available; an explicit refresh MUST update it from the provider.
- **S4.1** Given an account without cached models, when discovery succeeds, then its credential JSON contains the models and existing fields are preserved; a subsequent cached read returns the models without an upstream request.
- **S4.2** Given cached models, when refresh succeeds, then the persisted list updates; when provider response is invalid or refresh fails, then prior cache and credentials remain unchanged.

## Expected files

- T2: `main.go`, `main_test.go`, `model_discovery.go`, `model_discovery_test.go`, `ui_models_test.js`, `README.md`
- T1: `main.go`, `main_test.go`, `model_discovery.go`, `model_discovery_test.go`, `ui_models_test.js`, `README.md`

## Tasks

| ID | Specs | Route | Status | Evidence |
|---|---|---|---|---|
| T2 | S1, S2, S3, S4 | gentle-ai-worker | implemented; four blockers independently PASS; formal closure blocked | Correction worker reports Node8/8, Go26/26 GREEN; RED command evidence only, no new tests-only commit provided |
| T1 | S1, S2 | gentle-ai-worker | implemented; functional and independent review PASS; closure blocked | Base: 8581358f097be6343b94f13d7b6457a12fd7e5d8; RED: 481e063; implementation commit pending |

## Log

- **L4** delivery request: "crea commit, pr en mi fork, e issue en el upstream apuntando a mi pr"
- **L5** delivery correction: "crea una unica pr crea una rama nueva desde origin/main"
- **D7 — Delivery strategy.** Choice: one PR from new `feat/provider-model-cache` based on freshly fetched `origin/main` (`34adc2e8705e30905e791a981f45d3d8b5c6efc2`). Supersedes previously selected chain. Authenticated GitHub actor `gonzalez962` has ADMIN on fork; explicit single-PR request accepts oversized review scope. Preserve tests and source, no compression to meet budget. Prior fork PR1 merged. Native/gate/browser missing proof must be disclosed, not waived by delivery. Upstream `WiloxDev/omniauth-pro-plugin` has issues enabled but no `.github` forms; creation skill forbids fallback publication. No approved linked issue exists for PR skill prerequisite; blocked publication must be reported rather than inventing labels/authority.

- **E8 — Final bounded recheck.** Verifier `mvklclg8-7-fcbf` independently ran Node8/8 exit0 and Go regression3/3 exit0. All four E6 blockers PASS: exact big-number preservation, stale identity rejection, synchronized in-process delete/commit, modal card-button cleanup. Parent final `git diff --check` exit0; `git status` still contains uncommitted feature files (verifier's 'clean' refers at most to no new edits by verifier, not a clean Git worktree). Mechanical gate/JUnit and real-browser proof unavailable; native assess previously blocked by facade schema. Formal closure/implementation commit remain blocked pending human decision on missing proof. In-process mutex does not coordinate external host credential-file writers; no cross-process safety claimed. Binary not rebuilt.
- **E7 — Correction returned.** Worker `mvkl1xjd-6-b19j` reports all four corrections implemented, RED observed for big-number preservation, stale identity and late button reset; GREEN Node8/8, Go regression3/3 and Go26/26, diff-check exit0. Independent recheck `mvklclg8-7-fcbf` launched, limited to four blockers. `accountStorageMu` is in-process only; fresh checks do not provide atomic coordination against unrelated external-process mutations after re-read. No test-only commit, gate, native review or browser proof inferred from worker summary.
- **E6 — Independent blockers / correction batch 1.** Verifier `mvkknfg6-5-l1zg` found: (1) map[string]any roundtrip corrupts >2^53 unknown numeric fields, reproduced RED; (2) stale response cached after key/base URL changes, reproduced RED; (3) unsynchronized stat/read/rename can resurrect deleted accounts or overwrite updates, static evidence; (4) modal close/switch late response leaves per-card button disabled, reproduced RED. S1/S3.1/S4.1/S4.2 unmet. One consolidated correction batch authorized by original scope; independent recheck limited to these blockers afterwards. Verifier created unapproved external scratch; parent will neither access nor delete it. No clean-worktree claim accepted: parent observed uncommitted feature files. Tests-only checkpoint remains absent; gate unavailable, not waived.
- **E5 — T2 worker result.** Worker `mvkjz2wl-4-p6x0` returned modal/cache implementation and RED exit1 / GREEN exit0 evidence, Node7/7 and Docker Go23/23. No tests-only checkpoint query or RED commit was provided; mechanical gate evidence remains missing. Parent diff check exit0. Independent verifier `mvkknfg6-5-l1zg` checking safety: JSON value preservation, stale cache identity, delete/write race, and modal per-card loading reset. Browser verification not performed by worker; existing .so unchanged.
- **L3** user correction: "vale al mostrar la lista de modelos debe ser en un modal, y se debe actualizar el archivo del auth.json donde se guardan las credenciales para poner la lista de los modelos y tenerlos como cache"
- **D4 — Cache storage.** Choice: additive namespaced `model_cache` object with `models` string array and `updated_at` in the existing per-account JSON, not a separate shared auth.json. Alternatives: overwrite host-consumed top-level model configuration or use browser storage. Rationale: current files are one JSON per account; preserve inference configuration and credentials. Read legacy files without cache; honor explicit refresh and serve cache on normal viewing. Automatic fetch after creation remains. No TTL is introduced.
- **D5 — Safe persistence.** Choice: validate a recognized provider payload before persistence, preserve unknown JSON values and file permissions, use atomic replacement with current-account recheck to avoid restoring deleted/changed credentials after network I/O. Alternatives: direct overwrite from stale account map. Rationale: updating credential files triggers risk items 1/2/4. A failed fetch/save must not destroy existing cache or credentials; return honest errors.
- **D6 — Modal behavior.** Choice: account-specific modal with close and refresh actions; cache visible without network when available. Alternatives: inline model box (superseded by L3). Rationale: requested functional presentation change includes opening/closing and result ownership, not merely styling. Retain retry/errors safely and guard late responses from other accounts.
- **E4 — Scope reconciliation.** T2 extends the same feature and overlaps T1 intentionally; no parallel writer on these files. Prior functional/static proof applies only to pre-modal/cache implementation. Prior native/gate limitations remain recorded, not waived. Existing Linux .so predates T2 and is not rebuilt unless requested.

- **L1** original request: "hay que agregar boton para obtener los modelos de cada provider agregado en su respectiva card y tambien hacer un fetch de los modelos cuandos se agrega una clave"
- **D1 — Server-side discovery.** Choice: fetch via an additive management endpoint referencing an existing account ID; browser never receives the saved key. Alternatives: direct browser requests. Rationale: main.go handleAccountsAPI already owns credentials and account IDs; UI apiFetch already uses management authorization. Preserve existing account routes, default provider URLs, and saved credential data. Sanitize provider errors so keys are not returned; reject unsafe IDs, do not forward credentials across redirects, use bounded timeouts and response sizes. These are implementation safety invariants, not extra product scope.
- **D2 — Discovery failure.** Choice: retain the newly added account if discovery fails and show a per-card retryable error. Alternatives: roll back account creation. Rationale: key creation currently succeeds independently of provider network availability; preserve it.
- **E3 — Independent verification.** Verifier `mvkjedpx-3-982n` returned PASS for S1/S1.1/S2/S2.1, no concrete blockers; independently ran Node 4/4 PASS and reviewed credential isolation, redirects, safe rendering and account creation flow. Parent Docker scoped checks exited 0 (`ok omniauth-pro-plugin 0.094s`). This is not the mechanical spec-guard gate matrix: verifier did not supply gate execution or RED/GREEN JUnit evidence. Its 'ready for merge' statement grants no delivery authority. Native assess still rejects `intendedUntracked` before assessment, so native risk/reviewDue are unavailable. Functional and independent static review are complete; gate/native tooling proof remains unavailable. Implementation commit remains pending; no merge/push authorized.
- **L2** user continuation: "verifica". Explicitly authorizes renewed verification after unavailable proof. Parent observed `node --test ui_models_test.js` exit 0 (4/4), `git diff --check` exit 0. Scoped Docker Go job `job-1` and independent read-only verifier `mvkjedpx-3-982n` started; final proof pending.
- **E2 — GREEN evidence.** Worker completed T1: Node UI 4/4 and Docker Go 18/18 pass; host Go unavailable due to missing GCC. Independent verifier `mvkiozbw-2-r4lj` failed: assistant returned no final report. Independent proof and scenario gate are unavailable; no verdict inferred. Closing and implementation commit await a human decision on the missing verification. Native assess unavailable: direct facade rejects required schema field `intendedUntracked` for assess; no review authority created. Parent diff check passes. Security classification remains risk item 2, contrary to worker's risk-none summary.
- **E1 — RED evidence.** Tests-only commit: `481e063`. Worker observed exit 1 for `node --test ui_models_test.js` (S1.1/S2.1 failures) and Docker Go tests (missing registration/discovery). Fixtures now use local httptest providers. RED commit is 764 lines, larger than the ~400-line target due to standalone browser-mock tests and public-interface Go fixtures. Keep implementation as a separate work unit; do not accumulate further source into the test slice. Delivery/publishing remains unrequested.
- **D3 — Verification.** Scenario IDs describe behavior (not a purely visual requirement). Use public management requests with isolated auth fixtures and executable UI behavior tests. Existing account list/add/delete behaviors must remain covered. Parent owns commits; worker returns RED/GREEN evidence and does not commit.
