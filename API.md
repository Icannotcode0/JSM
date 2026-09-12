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

**Rate limiting:** `POST /signup`, `POST /login`, and `POST /reset-password` are
throttled and may answer `429`. Every other route is unlimited — including
`GET /health`, deliberately, because it is how a client obtains its CSRF token
and throttling it would break sign-in for whoever retries most.

```
429 Too Many Requests
Retry-After: 41
{"error": "TOO_MANY_REQUESTS"}
```

`Retry-After` is the whole seconds until the exhausted budget refills; honour it
rather than retrying blind. The body is **identical regardless of which limit was
hit** — naming the rule would confirm that an account exists at a given address,
so it deliberately says nothing beyond "too many".

Login is limited both per client address and per email address; signup per client
address; reset-password per user. A successful login clears its counters, so
mistyping a password a few times before getting it right costs nothing.

**Common errors** that can occur on *any* route below, not repeated per-endpoint:

| Status | Body | Cause |
|---|---|---|
| 403 | `{"error": "csrf token missing"}` | No CSRF cookie present on a mutating request |
| 403 | `{"error": "csrf token mismatch"}` | `X-CSRF-TOKEN` header doesn't match the cookie |
| 403 | `{"error": "csrf token invalid"}` | Token wasn't signed by this server, or isn't bound to the session presenting it |
| 401 | `{"error": "unauthorized"}` | Missing/invalid/expired session on a route requiring one |
| 500 | `{"error": "INTERNAL_SERVER_ERROR"}` | Unhandled failure (DB down, etc.) |

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
| 429 | `{"error": "TOO_MANY_REQUESTS"}` | Too many signups from this client address — see Rate limiting above |

---

### `POST /login` — ✅
Auth: CSRF only.

**Request**
```json
{"email": "a@b.com", "password": "..."}
```

**200 OK** — sets the session cookie (`HttpOnly`) plus a CSRF cookie rebound to
the new session, via `Set-Cookie`.
```json
{"status": "ok"}
```
The user is deliberately *not* returned: `GET /me` is the single source of truth
for user info, so there is only one shape to keep in sync.

Both cookies matter. CSRF tokens are bound to a session, so the token that
authorised this login stops being valid the instant the session exists — the
response reissues one bound to it.

**Errors**
| Status | Body | Cause |
|---|---|---|
| 400 | `{"error": "invalid request"}` | Malformed JSON / missing fields |
| 401 | `{"error": "invalid email or password"}` | Wrong email or password — deliberately identical message for both, to avoid confirming which emails are registered |
| 429 | `{"error": "TOO_MANY_REQUESTS"}` | Too many attempts from this client address, or against this email — see Rate limiting above |

---

### `POST /logout` — ✅
Auth: session required.

**Request:** no body.

**200 OK** — clears the session cookie.
```json
{"status": "ok"}
```
Note: if no session cookie is present at all, this still returns `200 {"status": "ok"}` (already logged out) rather than an error.

---

### `POST /reset-password` — ✅

Auth: session + CSRF.

Changes the password of the **currently signed-in user**. Despite the name this
is a *change-password*, not a forgot-password reset: it requires an active
session and proof of the current password. There is no unauthenticated
password-recovery flow — that needs a mail transport JSM doesn't have.

The account is taken from the session. There is deliberately no `email` or
`user_id` field: accepting one would make this "change any user's password",
gated only by whatever check the handler remembered to perform.

**Request**
```json
{"current_password": "…", "new_password": "…"}
```

`new_password` must be at least 8 characters, at most 72 **bytes**, and contain
at least one uppercase letter and one special character (Unicode punctuation or
symbol — so `!` and `$` both count). The byte cap is bcrypt's: it truncates at
72 bytes, so anything longer would be silently ignored rather than hashed.

**200 OK**
```json
{"status": "ok"}
```

> **The session is destroyed on success.** The response clears the session
> cookie and returns a CSRF cookie rebound to the anonymous state, so the client
> must send the user back to sign in with the new password. Keeping the session
> alive would mean a stolen token still worked after a rotation performed
> because it was stolen.

**Errors**
| Status | Body | Cause |
|---|---|---|
| 400 | `{"error": "password must be at least 8 characters"}` | Too short |
| 400 | `{"error": "password must be at most 72 bytes"}` | Exceeds bcrypt's limit |
| 400 | `{"error": "password must contain an uppercase letter"}` | Missing uppercase |
| 400 | `{"error": "password must contain a special character"}` | Missing punctuation/symbol |
| 400 | `{"error": "new password must differ from the current one"}` | `new_password` equals `current_password` |
| 401 | `{"error": "INCORRECT_CREDENTIALS"}` | `current_password` is wrong |
| 401 | `{"error": "UNAUTHORIZED"}` | Session names a user that no longer exists |
| 413 | `{"error": "REQUEST_TOO_LARGE"}` | Body exceeds the 1 MiB cap |
| 429 | `{"error": "TOO_MANY_REQUESTS"}` | Too many attempts for this user — see Rate limiting above |

---

### `GET /me` — ✅
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

### `POST /applications` — ✅
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

### `GET /applications` — ✅
Auth: session required.

**Query params** (all optional): `status`, `tag`, `q`, `page` (default 1),
`page_size` (default 20, max 200).

`q` is a case-insensitive substring match over `company_name`, `position_title`,
`tags`, and `notes`. The term is regex-escaped server-side, so metacharacters
are matched literally rather than interpreted.

**200 OK**
```json
{"applications": [{"id": "...", "company_name": "...", "...": "..."}], "page": 1, "page_size": 20, "total": 42}
```

**Errors:** 400 if a query param is malformed (e.g. `page=abc`).

---

### `GET /applications/:id` — ✅
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

### `PATCH /applications/:id` — ✅
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

### `DELETE /applications/:id` — ✅
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
