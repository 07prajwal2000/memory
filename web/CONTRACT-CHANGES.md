# REST API Contract Feedback & Proposals

This document records observations, suggestions, and proposals for the REST API contract (`.prompts/_api.md`) made during the implementation of the dashboard frontend (`web/`).

---

### Proposal 1: Clarify `PUT /memory` response schema for `index_warning`
- **Endpoint**: `PUT /memory`
- **Current description**: "`200 Memory` (updated) or `201 Memory` (created); optional `index_warning:string` when indexing failed but the write succeeded"
- **Proposal**: Specify that `index_warning?: string` is a top-level field on the returned JSON object alongside the `Memory` fields (e.g. `{ ...Memory, index_warning?: string }`), rather than a response header.
- **Reason**: The dashboard UI inspects `response.index_warning` to display a non-blocking warning banner with a "Sync now" action. Explicitly typing this as a field in the response JSON avoids confusion between body fields and HTTP headers.

---

### Proposal 2: Clarify `id`-only lookup in `GET /memory`
- **Endpoint**: `GET /memory`
- **Current description**: "query `context?` (default global) and exactly one of `id`, `name`"
- **Proposal**: When `id` is provided, allow `context` to be omitted entirely, with the backend resolving the memory by its unique `id` across contexts.
- **Reason**: In search results and deep links, a memory `doc_id` is often known before the context or when jumping between views. If `context` defaults to `global` when omitted, a lookup for `GET /memory?id=xyz` (where `xyz` belongs to another context) might fail with `not_found` unless `context` is passed. Making `context` truly optional when `id` is present simplifies client routing.

---

### Proposal 3: Batch deletion or bulk actions (Future feature)
- **Endpoint**: `DELETE /memory`
- **Current description**: query `id`
- **Proposal**: Consider supporting comma-separated `ids` or a batch endpoint `POST /memories/batch-delete` `{ ids: string[] }`.
- **Reason**: Useful if users want to clean up multiple memories at once from the tree/list view.
