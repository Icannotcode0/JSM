# JSM Backend — Design Guide & Learning Path

You know how to write code, and you understand cookies/sessions/middleware/handlers
in isolation. What's missing is the **big picture**: how all those pieces are supposed
to snap together into one coherent backend. This doc is that big picture, plus a
gradual path to build it so each step teaches you the next concept before you need it.

---

## Part 1 — The Big Picture: Layered Architecture

Almost every backend, no matter the language, is organized into layers. Each layer has
exactly one job and only talks to the layer directly below it. This is the single most
important idea in backend design — once it clicks, the folder structure stops being
arbitrary and starts being obvious.

```
   HTTP Request
        |
        v
+-------------------+
|   Middleware       |   cross-cutting concerns: logging, CORS, recover-from-panic,
|                     |   "is there a valid session cookie?", "does the CSRF token match?"
+-------------------+
        |
        v
+-------------------+
|   Handler          |   "translator" between HTTP and your app. Reads the request
|  (internal/http)   |   (JSON body, query params, cookies), calls a Service, writes
|                     |   back an HTTP response (status code + JSON). Contains NO
|                     |   business logic and NO database code.
+-------------------+
        |
        v
+-------------------+
|   Service           |   the actual business logic. "To log in: fetch the user by
| (internal/service)  |   email, compare password hash, create a session." Doesn't
|                     |   know anything about HTTP (no gin.Context here!). Doesn't
|                     |   know Mongo query syntax — it calls the Store.
+-------------------+
        |
        v
+-------------------+
|   Store / Repo      |   the ONLY layer that knows about Mongo/Redis query syntax.
| (internal/store)   |   "Insert this document," "find a user by email," "set this
|                     |   Redis key with a TTL." Pure data access, no business rules.
+-------------------+
        |
        v
   MongoDB / Redis
```

Two supporting pieces sit beside this stack, used by every layer:

- **`internal/domain`** — the shared vocabulary. Structs like `User` and
  `Application` that represent "what a thing IS," independent of how it's stored or
  transmitted. Every layer imports these.
- **`internal/config`** — how the app is configured (ports, connection strings,
  cookie settings). Should contain *only* configuration, not domain data.

### Why bother separating these?

- **You can test a Service without a real database** (swap the Store for a fake one).
- **You can change Mongo → Postgres later** by rewriting only the Store layer —
  handlers and services don't change.
- **A handler bug (wrong status code) can't hide a business-logic bug (wrong password
  check)** — they're physically in different functions, easier to reason about one
  at a time.
- When something breaks, the layering tells you where to look: wrong JSON shape in
  the response → handler. Wrong decision (e.g. let in a user with the wrong password) →
  service. Data corrupted/missing in Mongo → store.

This is why your project already has `handlers/`, `service/`, `store/` folders — the
shape is right, they just aren't fully wired together yet.

---

## Part 2 — What Each File/Folder Is For

```
backend/
  cmd/api/main.go            entry point ONLY: read config, construct every
                             layer (store -> service -> handler), wire routes,
                             start the HTTP server. No business logic here.

  internal/
    config/
      config.go              Config struct + a Load() function that reads env
                             vars / .env into it. Constants like cookie names.
                             (Currently also holds domain structs by mistake —
                             those should move to domain/.)

    domain/
      user.go                User struct: the canonical shape of a user.
      application.go         Application struct + (later) ResumeVersion struct.
                             These are what Services and Stores pass around.

    store/
      mongo/db.go            MongoClient wrapper + one file per collection's
                             queries later (users_store.go, applications_store.go).
                             Only place that writes bson.M{...} filters.
      redis/redis.go         Redis client constructor. Session storage
                             operations actually live in auth/session.go today
                             (fine for now — could move here later).

    auth/
      session.go             SessionManager: create/delete/read sessions in
                             Redis, issue/clear cookies. This is infrastructure,
                             used by the auth Service.
      password.go            Password hashing + verification (bcrypt). Pure
                             function, no HTTP, no DB.
      middleware.go           CSRF check. (Session-required check should also
                             live here as its own middleware function.)

    service/
      auth_service.go         Login/Signup/Logout business logic. Calls
                             store (fetch user), auth (check password,
                             create session).
      application_service.go  Create/list/update/delete an application, business
                             rules (e.g. "can't set NextFollowUpAt in the past").

    http/
      routers.go              One function that takes the Gin engine + all
                             services, and declares every route + which
                             middleware applies to it.
      handlers/
        auth_handler.go        HTTP glue for /signup, /login, /logout, /me.
        application_handler.go HTTP glue for /applications CRUD.
      middleware/
        cors.go, logging.go, recover.go   generic Gin middleware, no app
                             knowledge required.
```

**Rule of thumb while coding:** if you're tempted to write `bson.M{...}` inside a
handler, stop — that query belongs in `store/mongo`. If you're tempted to write
`c.JSON(...)` inside a service, stop — that belongs in a handler. This one rule
prevents 90% of "spaghetti" backend code.

---

## Part 3 — How a Request Actually Flows (concrete example: Login)

Walking through one real request end-to-end is the fastest way to internalize the
layering.

1. Browser sends `POST /login` with JSON body `{"email": "...", "password": "..."}`.
2. **Middleware** runs first (in the order registered in `routers.go`): recover →
   logging → CORS → CSRF (login is a POST, so CSRF is checked). No session-required
   middleware here, since you're not logged in yet.
3. **Handler** (`auth_handler.go`) runs: parses the JSON body into a small
   `loginRequest` struct, calls `authService.Login(ctx, email, password)`.
4. **Service** (`auth_service.go`) runs:
   - Calls `store.FindUserByEmail(ctx, email)`.
   - If not found → return a "invalid credentials" error (never say "email not
     found" specifically — that leaks which emails are registered).
   - Calls `auth.VerifyPassword(user.PasswordHash, password)`.
   - If it matches, calls `sessionManager.CreateSession(ctx, c, user.ID, ...)`.
   - Returns the user (or a trimmed "safe" version of it) to the handler.
5. **Store** (`store/mongo`) only did one thing: ran `FindOne` with a `bson.M{"email":
   email}` filter and decoded into a `domain.User`.
6. **Handler** takes the service's result and writes the HTTP response:
   `c.JSON(http.StatusOK, gin.H{"user": safeUser})`. The session cookie was already
   set on `c.Writer` inside `CreateSession`.
7. Response goes back through the middleware chain (logging middleware records the
   status code and duration) and out to the browser.

Every future route (create application, upload resume, etc.) follows this same
shape: middleware → handler (parse + respond) → service (decide) → store (persist).
Once you've built login end-to-end, "add application" is the same pattern with
different fields.

---

## Part 4 — Data Model

### MongoDB — `users` collection

```go
package domain

import (
    "time"

    "go.mongodb.org/mongo-driver/v2/bson"
)

type User struct {
    ID           bson.ObjectID `bson:"_id,omitempty" json:"id"`
    Email        string        `bson:"email" json:"email"`
    PasswordHash string        `bson:"password_hash" json:"-"`        // never JSON
    Name         string        `bson:"name" json:"name"`
    CreatedAt    time.Time     `bson:"created_at" json:"created_at"`
    UpdatedAt    time.Time     `bson:"updated_at" json:"updated_at"`
}
```
Index: unique on `email`.

### MongoDB — `applications` collection

```go
package domain

import (
    "time"

    "go.mongodb.org/mongo-driver/v2/bson"
)

type Application struct {
    ID             bson.ObjectID     `bson:"_id,omitempty" json:"id"`
    UserID         bson.ObjectID     `bson:"user_id" json:"user_id"`
    CompanyName    string            `bson:"company_name" json:"company_name"`
    PositionTitle  string            `bson:"position_title" json:"position_title"`
    Status         string            `bson:"status" json:"status"` // "applied", "phone_screen", "onsite", "offer", "rejected", "withdrawn"
    Location       []string          `bson:"location" json:"location"`
    JobLink        string            `bson:"job_link" json:"job_link"`
    JobDescription string            `bson:"job_description" json:"job_description"`
    Tags           []string          `bson:"tags" json:"tags"`
    Notes          string            `bson:"notes" json:"notes"`
    Compensation   *CompensationInfo `bson:"compensation" json:"compensation"`
    Resumes        []ResumeVersion   `bson:"resumes" json:"resumes"`
    AppliedAt      time.Time         `bson:"applied_at" json:"applied_at"`
    NextFollowUpAt *time.Time        `bson:"next_follow_up_at,omitempty" json:"next_follow_up_at,omitempty"`
    CreatedAt      time.Time         `bson:"created_at" json:"created_at"`
    UpdatedAt      time.Time         `bson:"updated_at" json:"updated_at"`
}

type CompensationInfo struct {
    BaseSalary int64    `bson:"base_salary" json:"base_salary"`
    Bonus      int64    `bson:"bonus" json:"bonus"`
    Equity     string   `bson:"equity" json:"equity"` // free text, e.g. "$40k RSU over 4yr"
    Benefits   []string `bson:"benefits" json:"benefits"`
}

type ResumeVersion struct {
    ID         bson.ObjectID `bson:"_id,omitempty" json:"id"`
    Label      string        `bson:"label" json:"label"`   // "Backend-focused v2"
    StorageKey string        `bson:"storage_key" json:"-"` // path/S3 key, don't expose raw
    FileName   string        `bson:"file_name" json:"file_name"`
    UploadedAt time.Time     `bson:"uploaded_at" json:"uploaded_at"`
}
```
Indexes: compound on `(user_id, status)` for the dashboard list view; optionally a
text index on `notes` + `tags` if you want search later.

**Design choice worth knowing:** resumes are embedded as a sub-array here rather than
a separate collection, because they're always fetched together with the application
and rarely queried on their own. If resumes ever need independent listing (e.g. "all
resumes across all applications"), that's when you'd split them into their own
collection with an `application_id` foreign key.

### Redis — session storage

Key: `session:<random-session-id>`
Value: a Hash — `{user_id, email, created_at}`
TTL: matches cookie `MaxAge` (e.g. 7 days), refreshed on activity if you want
sliding sessions later.

---

## Part 5 — Gradual Build Path

Each milestone below is small enough to finish and *see working* (via `curl` or
Postman) before moving to the next. Don't skip ahead — each step teaches a concept
the next step assumes you already have.

### Milestone 0 — "Hello, server"
Get `main.go` to construct a Gin engine, register one route (`GET /health` returning
`{"status": "ok"}`), and call `.Run(":8080")`. Confirm with `curl localhost:8080/health`.
**Learning goal:** the server actually starts and responds. No DB yet.

### Milestone 1 — Config loading
Add `config.Load()` that reads env vars (use `os.Getenv`, or the `godotenv` package to
load `.env` in dev) into your `Config` struct. Print it (minus secrets) on startup.
**Learning goal:** config is a plain data-loading step, separate from everything else.

### Milestone 2 — Connect to Mongo & Redis
Wire `main.go` to call `mongo.NewClient(...)` and `redis.NewClient(...)` using the
loaded config, and fail fast (log + exit) if either ping fails.
**Learning goal:** infrastructure connections happen once, at startup, in `main.go` —
not inside handlers.

### Milestone 3 — Signup (no sessions yet)
Build `POST /signup`: handler parses email/password/name → service hashes the
password (bcrypt) and calls store to insert a `User` → handler returns 201.
Test with `curl -X POST -d '{"email":"a@b.com","password":"x","name":"A"}'`.
**Learning goal:** the full handler → service → store chain, for the simplest
possible write operation.

### Milestone 4 — Login + sessions
Build `POST /login`: service fetches user by email, verifies password with bcrypt,
calls `SessionManager.CreateSession` to set the cookie. Build `GET /me` protected by
a new "session required" middleware that reads the cookie, looks up Redis, and puts
the user ID on the Gin context for handlers to use.
**Learning goal:** this is where cookies + sessions + middleware — things you already
know — get connected to a real login flow for the first time.

### Milestone 5 — CSRF + logout
Fix and wire in the existing `CSRFMiddleWare` (apply the `return` fix from the review).
Build `POST /logout` (deletes the Redis session + clears cookie).
**Learning goal:** understand why CSRF matters specifically *because* you're using
cookie-based sessions (vs. header-based tokens, which don't need CSRF protection).

### Milestone 6 — Application CRUD
Build `POST /applications`, `GET /applications`, `GET /applications/:id`,
`PATCH /applications/:id`, `DELETE /applications/:id`, all behind the session
middleware, scoped to `user_id` so users can't see each other's data.
**Learning goal:** the same layered pattern, applied to your actual core feature —
proof the architecture generalizes beyond auth.

### Milestone 7 — Filtering, sorting, pagination
Add query params to `GET /applications` (`?status=applied&tag=remote&page=2`).
**Learning goal:** translating query params into Mongo filter/sort/skip/limit,
still entirely inside the store layer.

### Milestone 8 — Resume upload
Add `POST /applications/:id/resumes` (multipart file upload), store the file (start
with local disk under a gitignored `uploads/` folder — swap for S3 later without
changing any handler code), append a `ResumeVersion` to the application document.
Add `GET /applications/:id/resumes/:resumeId` to download.
**Learning goal:** file uploads in Gin (`c.FormFile`), and why storage-backend choice
is isolated to the store layer (swappable later).

### Milestone 9 — Middleware polish
Fill in `cors.go`, `logging.go`, `recover.go` for real (currently empty). Add them to
the engine in the right order in `main.go`.
**Learning goal:** middleware ordering matters (recover should wrap everything;
logging should run early to time the whole request).

### Milestone 10 — Tests + cleanup
Add a couple of table-driven tests for the auth service (using a fake store) and the
application service. Fix remaining review-list bugs if any weren't caught earlier.
**Learning goal:** because services don't know about Gin or Mongo directly, they're
the easiest layer to unit test — this is the payoff of the layering from Part 1.

---

## Part 6 — Local Web App + Browser Extension Architecture (decided 2026-07-13)

JSM is **local-only, by design** — Go backend + Mongo/Redis via Docker + a web
frontend, all running on your machine, never exposed to the internet. The frontend
is **Vite + vanilla TypeScript** (no framework), and there's a second, separate
piece: a **browser extension** that auto-captures job postings from sites like
LinkedIn/Greenhouse/Lever so you don't have to type everything by hand.

### Why this shape

Because the frontend is served **from the same Go binary, same origin** as the API
(via `go:embed` — see below), the browser's same-origin policy already protects the
main app. There is **no CORS configuration needed for the web UI at all**. The
*only* cross-origin surface in the whole system is the browser extension (running
as `chrome-extension://<id>`), which is why it gets its own dedicated, more
carefully hardened endpoint rather than reusing the normal `/applications` route.

### Updated project layout

```
JSM/
  backend/
    cmd/api/main.go              binds 127.0.0.1:<port> ONLY — never 0.0.0.0.
                                  Serves the API *and* the embedded frontend build.
    internal/                    same layered structure as Parts 1-2 above, plus:
      http/handlers/
        extension_handler.go     NEW — the one endpoint the browser extension calls.
                                  Deliberately kept separate from the other handlers
                                  so "this code path receives untrusted external
                                  input" is visible in the file layout, not buried.

  frontend/                      NEW — Vite + vanilla TypeScript
    src/
    index.html
    vite.config.ts                dev server proxies /api/* to the Go backend;
                                  `vite build` output gets embedded into the Go
                                  binary for normal (non-dev) runs.

  extension/                     NEW — the browser extension
    manifest.json                 Manifest V3. host_permissions limited to the
                                  specific job-board domains + the local API
                                  origin — never <all_urls>.
    content.ts                    Runs on job-board pages: looks for a
                                  schema.org/JobPosting <script type="application/
                                  ld+json"> block (JSON.parse only, never eval),
                                  falls back to OG tags. Injects a "Save to JSM"
                                  button using textContent/DOM APIs — never
                                  innerHTML on scraped content (self-XSS risk).
    background.ts                  Owns the actual fetch(POST) to the backend;
                                  content scripts should message this via
                                  chrome.runtime.sendMessage rather than
                                  fetching directly.

  docker-compose.yml             NEW — local Mongo + Redis for development.
```

### The extension endpoint's security model

This is the one place in JSM that must be written as if it were public-internet
facing, even though the app never leaves your machine — because any webpage open
in another tab can attempt a drive-by `fetch()` POST to `http://localhost:<port>`,
and the browser will still send it (it only blocks the *page* from reading a
cross-origin response, not from sending the request). Checklist for
`extension_handler.go` when we build it:

- Server binds `127.0.0.1` only (already called out above, worth repeating).
- CORS allowlist contains the **exact, specific** `chrome-extension://<id>` origin
  — no wildcards.
- Same session cookie + CSRF header requirement as every other mutating route —
  the extension isn't a trusted shortcut around that.
- Every scraped field (company, title, description, URL, tags) is treated as
  untrusted: length caps, HTML stripped/escaped before storage, `JobLink` validated
  as `http`/`https` only (reject `javascript:`, `data:`, `file:`).
- Request body size capped (`http.MaxBytesReader`) to block abuse.
- JSON-LD parsed with `encoding/json` only, both in the extension and the backend —
  never `eval`, never template-execute untrusted content.

### Build order changes

- **Before Milestone 2**, add `docker-compose.yml` (Mongo + Redis) and `docker
  compose up -d` — you need the containers running before "connect to Mongo &
  Redis" makes sense.
- **Milestone 11 — Frontend scaffold**: a single Vite+TS page that calls `GET
  /health` and renders the result, proving the `go:embed` serving path works
  end-to-end before building real screens.
- **Milestone 12 — Frontend CRUD screens**: application list + create form, wired
  to the Milestone 6/7 API.
- **Milestone 13 — Extension skeleton**: manifest + content script that detects a
  job posting page and shows a "Save to JSM" button (no backend call yet — log the
  extracted data to the console first, prove extraction works before wiring
  network calls).
- **Milestone 14 — Secure extension import endpoint**: `extension_handler.go` with
  the full checklist above, wired to the existing `ApplicationService`.

### Milestone 13 in detail — extension architecture

A browser extension has its own internal architecture, built around **contexts**
(isolated JS environments) rather than HTTP layers:

| File | What it is | What it does |
|---|---|---|
| `manifest.json` | Config/entry point | Declares permissions, which scripts run where, icons, toolbar button. Metadata only, no logic. |
| `content.js` | Content script | Injected into matching job-board pages. Reads the page's DOM/JSON-LD, injects the "Save to JSM" button. Runs in an **isolated world** — it can see the page's DOM, but the page's own JS can't see or tamper with the content script's variables. That isolation is a real security boundary. |
| `background.js` | Background service worker | The *only* piece that makes network calls (fetch to the local API). Receives messages from the content script rather than the content script fetching directly. |
| `popup.html` / `popup.js` (optional) | Toolbar popup | Small UI on icon click — last captured job, manual save, quick settings. |
| icons (16/32/48/128px) | Required assets | Shown in the toolbar and extensions page. |

**Why content and background are split:** this mirrors the handler/service split in
the backend, but for a security reason, not just organization — a content script
runs inside the context of whatever page injected it, which is adversarial input
(a malicious/compromised job-board page), so it must never be trusted with the
extension's network calls. The idiom:

```
job-board page
      |
content script  --DOM read-only-->  extracts data, injects button
      |
      | chrome.runtime.sendMessage(data)   <- the contract between the two contexts
      v
background worker  --fetch(POST)-->  local Go API
```

Content script does **extraction only**, never touches the network. Background
does **network only**, never touches page DOM. Same rule as "don't put `bson.M{}`
in a handler," just for a different reason (isolation/trust boundary, not layering
for its own sake).

**Build tooling:** write plain JavaScript for the extension (`content.js`,
`background.js`), no bundler, no TypeScript compile step — even though the
frontend is TS+Vite. MV3 already introduces several new concepts at once
(permissions, contexts, messaging); not stacking build tooling on top keeps this
milestone focused on those concepts. `chrome://extensions` → "Load unpacked" then
works immediately with zero build step. Migrate to a Vite-based extension bundler
later if/when TypeScript is worth the extra moving part.

**Folder structure:**

```
extension/
  manifest.json
  background.js
  content.js
  popup.html
  popup.js
  icons/
    16.png  32.png  48.png  128.png
```

---

## How to Use This Doc

Work through the milestones in order. For each one, tell me which milestone you're on
and I'll help you write just that piece — small, reviewable, and testable — rather
than generating the whole backend at once. That keeps you in control of the code and
means you actually understand every line, instead of inheriting a big pile you can't
maintain.
