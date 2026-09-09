# JSM API Reference

Base URL: `http://127.0.0.1:<port>` (local-only — see `DESIGN_GUIDE.md` Part 6).

Status key: **✅ implemented** · **📝 designed, not yet built** (see `DESIGN_GUIDE.md`
for which Milestone builds each one).

---

## Conventions

**Auth model:** cookie-based sessions (`SessionRequired` middleware) + CSRF via the
double-submit-cookie pattern (`CSRFMiddleWare`). Every `GET`/`HEAD`/`OPTIONS`
request ensures a CSRF cookie is set (issuing one if missing); every mutating
request (`POST`/`PATCH`/`DELETE`) must echo that cookie's value back in an
`X-CSRF-TOKEN` header. **This applies even to `/signup` and `/login`** — the
client must make one `GET` request first (any route) to receive the CSRF
cookie before it can POST to them.

**Response envelope:**
- Success: `{"<resource>": {...}}` or `{"<resource>s": [...]}` for lists.
- Error: `{"error": "<message>"}`.

**Common errors** that can occur on *any* route below, not repeated per-endpoint:

| Status | Body | Cause |
|---|---|---|
| 403 | `{"error": "csrf token missing"}` | No CSRF cookie present on a mutating request |
| 403 | `{"error": "csrf token mismatch"}` | `X-CSRF-TOKEN` header doesn't match the cookie |
| 401 | `{"error": "unauthorized"}` | Missing/invalid/expired session on a route requiring one |
| 500 | `{"error": "internal server error"}` | Unhandled failure (DB down, etc.) |

---

## Auth

### `GET /health` — ✅
No auth. Health check.

**200 OK**
```json
{"status": "ok"}
```

---

### `POST /signup` — 📝 (Milestone 3)
Auth: CSRF only.

**Request**
```json
{"email": "a@b.com", "password": "at least 8 chars", "name": "Ada"}
```

**201 Created**
```json
{"user": {"id": "...", "email": "a@b.com", "name": "Ada", "created_at": "...", "updated_at": "..."}}
```
(`password_hash` is never included — `domain.User` tags it `json:"-"`.)

**Errors**
| Status | Body | Cause |
|---|---|---|
| 400 | `{"error": "invalid request"}` | Malformed JSON, missing fields, or password under 8 chars |
| 409 | `{"error": "email already registered"}` | Email already exists (unique index on `email`) |

---

### `POST /login` — 📝 (Milestone 4)
Auth: CSRF only.

**Request**
```json
{"email": "a@b.com", "password": "..."}
```

**200 OK** — sets the session cookie (`HttpOnly`) via `Set-Cookie`.
```json
{"user": {"id": "...", "email": "a@b.com", "name": "Ada", "created_at": "...", "updated_at": "..."}}
```

**Errors**
| Status | Body | Cause |
|---|---|---|
| 400 | `{"error": "invalid request"}` | Malformed JSON / missing fields |
| 401 | `{"error": "invalid email or password"}` | Wrong email or password — deliberately identical message for both, to avoid confirming which emails are registered |

---

### `POST /logout` — 📝 (Milestone 5)
Auth: session required.

**Request:** no body.

**200 OK** — clears the session cookie.
```json
{"status": "ok"}
```
Note: if no session cookie is present at all, this still returns `200 {"status": "ok"}` (already logged out) rather than an error.

---

### `GET /me` — 📝 (Milestone 4)
Auth: session required.

**200 OK**
```json
{"user": {"id": "...", "email": "a@b.com", "name": "Ada", "created_at": "...", "updated_at": "..."}}
```

**Errors:** 401 if the session is missing/invalid (see Common errors).

---

## Applications

All routes below are scoped to the authenticated user — you only ever see/modify
your own applications (filtered by `user_id` at the store layer).

### `POST /applications` — 📝 (Milestone 6)
Auth: session + CSRF.

**Request**
```json
{
  "company_name": "Acme Corp",
  "position_title": "Backend Engineer",
  "status": "applied",
  "location": ["Remote"],
  "job_link": "https://acme.example/jobs/123",
  "job_description": "...",
  "tags": ["backend", "go"],
  "notes": "Referred by...",
  "compensation": {"base_salary": 150000, "bonus": 10000, "equity": "$40k RSU/4yr", "benefits": ["health", "401k"]},
  "applied_at": "2026-07-01T00:00:00Z"
}
```
`compensation` may be `null`. `status` should be one of: `applied`, `phone_screen`,
`onsite`, `offer`, `rejected`, `withdrawn`.

**201 Created**
```json
{"application": {"id": "...", "user_id": "...", "company_name": "Acme Corp", "...": "...", "created_at": "...", "updated_at": "..."}}
```

**Errors:** 400 invalid input (bad `status` value, bad `job_link` scheme, missing required fields).

---

### `GET /applications` — 📝 (Milestones 6, 7)
Auth: session required.

**Query params** (all optional): `status`, `tag`, `page` (default 1), `page_size` (default 20).

**200 OK**
```json
{"applications": [{"id": "...", "company_name": "...", "...": "..."}], "page": 1, "page_size": 20, "total": 42}
```

**Errors:** 400 if a query param is malformed (e.g. `page=abc`).

---

### `GET /applications/:id` — 📝 (Milestone 6)
Auth: session required.

**200 OK**
```json
{"application": {"id": "...", "...": "..."}}
```

**Errors**
| Status | Body | Cause |
|---|---|---|
| 404 | `{"error": "application not found"}` | Doesn't exist, **or belongs to another user** — same response either way, so existence of other users' data is never confirmed |

---

### `PATCH /applications/:id` — 📝 (Milestone 6)
Auth: session + CSRF.

**Request:** any subset of the fields from `POST /applications`.
```json
{"status": "onsite", "notes": "Had the onsite, went well"}
```

**200 OK** — the full updated application.
```json
{"application": {"id": "...", "...": "..."}}
```

**Errors:** 400 invalid field value; 404 (see above, same not-found-vs-not-yours rule).

---

### `DELETE /applications/:id` — 📝 (Milestone 6)
Auth: session + CSRF.

**204 No Content** — no body.

**Errors:** 404 (see above).

---

## Resumes

### `POST /applications/:id/resumes` — 📝 (Milestone 8)
Auth: session + CSRF. `multipart/form-data`.

**Request fields:** `file` (the resume file), `label` (optional string, e.g. `"Backend-focused v2"`).

**201 Created**
```json
{"resume": {"id": "...", "label": "Backend-focused v2", "file_name": "resume.pdf", "uploaded_at": "..."}}
```
(`storage_key` is never included — `domain.ResumeVersion` tags it `json:"-"`.)

**Errors**
| Status | Body | Cause |
|---|---|---|
| 400 | `{"error": "invalid file"}` | Missing file, wrong type |
| 404 | `{"error": "application not found"}` | Parent application doesn't exist / isn't yours |
| 413 | `{"error": "file too large"}` | Exceeds the configured size cap |

---

### `GET /applications/:id/resumes/:resumeId` — 📝 (Milestone 8)
Auth: session required.

**200 OK** — the raw file, `Content-Disposition: attachment; filename="..."`.

**Errors:** 404 if the application or resume doesn't exist / isn't yours.

---

## Browser Extension

### `POST /api/extension/applications` — 📝 (Milestone 14)
Auth: session + CSRF, **plus** a CORS allowlist restricted to the exact
`chrome-extension://<id>` origin (no wildcards) — this is the one endpoint
that accepts data scraped from third-party pages, so every field below is
treated as untrusted input server-side (length caps, HTML stripped, `job_link`
scheme validated as `http`/`https` only). See `DESIGN_GUIDE.md` Part 6 for the
full security checklist.

**Request**
```json
{
  "company_name": "Acme Corp",
  "position_title": "Backend Engineer",
  "job_link": "https://acme.example/jobs/123",
  "job_description": "...",
  "location": ["Remote"],
  "source": "linkedin"
}
```

**201 Created** — same shape as `POST /applications`.
```json
{"application": {"id": "...", "...": "..."}}
```

**Errors**
| Status | Body | Cause |
|---|---|---|
| 400 | `{"error": "invalid input"}` | Field too long, `job_link` isn't `http`/`https`, malformed JSON-LD payload |
| 403 | `{"error": "origin not allowed"}` | Request's `Origin` isn't the configured extension ID |
| 413 | `{"error": "request too large"}` | Body exceeds `http.MaxBytesReader` cap |
