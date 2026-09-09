# JSM Database Layer Design

Two databases, each doing the one thing it's actually good at — no data lives in
both, no overlap to keep in sync.

| Database | Holds | Why this one |
|---|---|---|
| **MongoDB** | Users, applications (durable, structured, user-owned data) | Documents map naturally onto "one application + its notes/comp/resume-metadata" without needing joins for the app's single most common read (fetch one application, get everything about it in one query). |
| **Redis** | Sessions only | Sessions are ephemeral, TTL'd, high-churn, and never need to survive a restart in a way that matters — exactly Redis's design center. Keeping them out of Mongo avoids write load and collection growth for data that's supposed to expire. |

Both run locally via `docker-compose.yml`, bound to `127.0.0.1` only (see
`DESIGN_GUIDE.md` Part 6 — JSM is local-only by design).

---

## MongoDB: 2 collections, deliberately not more

### `users`

```go
type User struct {
    ID           bson.ObjectID `bson:"_id,omitempty"`
    Email        string        `bson:"email"`         // unique index
    PasswordHash string        `bson:"password_hash"` // bcrypt, never leaves the store layer as JSON
    Name         string        `bson:"name"`
    CreatedAt    time.Time     `bson:"created_at"`
    UpdatedAt    time.Time     `bson:"updated_at"`
}
```
Already implemented as-is in `internal/domain/user.go` / `internal/store/mongo/db.go`.

### `applications`

```go
type Application struct {
    ID             bson.ObjectID     `bson:"_id,omitempty"`
    UserID         bson.ObjectID     `bson:"user_id"` // every query filters on this — see Multi-tenancy below
    CompanyName    string            `bson:"company_name"`
    PositionTitle  string            `bson:"position_title"`
    Status         string            `bson:"status"` // "applied" | "phone_screen" | "onsite" | "offer" | "rejected" | "withdrawn"
    Location       []string          `bson:"location"`
    JobLink        string            `bson:"job_link"`
    JobDescription string            `bson:"job_description"`
    Tags           []string          `bson:"tags"`
    Notes          string            `bson:"notes"`
    Compensation   *CompensationInfo `bson:"compensation,omitempty"`
    Resumes        []ResumeVersion   `bson:"resumes,omitempty"`
    AppliedAt      time.Time         `bson:"applied_at"`
    NextFollowUpAt *time.Time        `bson:"next_follow_up_at,omitempty"`
    CreatedAt      time.Time         `bson:"created_at"`
    UpdatedAt      time.Time         `bson:"updated_at"`
}

type CompensationInfo struct {
    BaseSalary int64
    Bonus      int64
    Equity     string
    Benefits   []string
}

type ResumeVersion struct {
    ID         bson.ObjectID
    Label      string
    StorageKey string // path/S3 key — the file itself is NOT in Mongo, see below
    FileName   string
    UploadedAt time.Time
}
```
Already implemented in `internal/domain/application.go`. One gap found while
writing this doc — see **Open item** at the bottom.

### Why only these two collections

Every feature that's actually been asked for maps onto a *field*, not a new
collection:

| Feature | Where it lives |
|---|---|
| Application status | `Application.Status` |
| Resume versions | `Application.Resumes` (embedded) |
| Notes | `Application.Notes` |
| Compensation / perks | `Application.Compensation` (embedded) |
| Job description | `Application.JobDescription` |
| Tags | `Application.Tags` |

Three things that *look* like they might need their own collection, and why
they don't (yet):

- **Resume versions aren't a separate collection.** This is a "one-to-few"
  relationship (a handful of resume versions per application, always fetched
  *with* the application, essentially never queried on their own) — MongoDB's
  own modeling guidance is to embed exactly this shape. A separate collection
  would only earn its keep if you needed to list/search resumes independently
  of their application, which isn't a feature that exists. The actual file
  bytes are **never** stored in Mongo either way — `StorageKey` points at a
  file on local disk (`uploads/`, per `DESIGN_GUIDE.md` Milestone 8); Mongo
  documents have a 16MB cap and are bad at holding binary blobs regardless of
  embedding vs. referencing.
- **No `companies` collection.** Normalizing `company_name` into its own
  collection (with a `company_id` reference) would only pay off if you wanted
  cross-application company insight ("show me every time I've applied to
  Google"). Nobody's asked for that. Keeping `company_name` as a plain string
  is simpler today, and — importantly — adding a `companies` collection later
  is a **non-breaking additive change**: you'd add the new collection,
  backfill it, add `company_id`, and can even keep `company_name` around as a
  denormalized cache field to avoid a join on every read. Not building it now
  doesn't box us in later.
- **No `tags` collection.** Same reasoning — freeform `[]string` on
  `Application` is sufficient; if tag autocomplete becomes a real feature,
  `db.applications.distinct("tags")` answers it without a dedicated collection.

**Status history was considered and deliberately deferred.** A timeline view
("applied → phone screen → onsite") would need either a `status_history`
array embedded on `Application` (still one-to-few, still fine to embed) or,
if it ever needs to be its own audit trail, a separate collection. Neither is
built now — `Status` + `UpdatedAt` is enough for the currently-scoped
feature set, and adding `status_history: []StatusChange{...}` later is an
additive field, not a migration. Flagging this here so it's a documented,
intentional choice, not a gap someone finds later and wonders about.

**No Mongo-side JSON Schema validators.** Mongo supports collection-level
schema validation, but that would mean maintaining validation rules in two
places (Go structs + Mongo validator) that can drift out of sync. Per
`DESIGN_GUIDE.md`'s layering rules, validation belongs in the service layer,
in one place, in Go. Simpler, one source of truth.

---

## Multi-tenancy: every query filters by `user_id`

Even though JSM is a personal, single-user-per-install tool, the schema
already supports multiple `User` documents — so every `applications` query
**must** filter by `user_id`, not just at "list applications" time but on
every single-document fetch too (`GET/PATCH/DELETE /applications/:id`), per
`API.md`'s existing rule: a document that exists but belongs to someone else
returns the same `404` as a document that doesn't exist at all. This needs to
be enforced at the store layer (the filter itself, e.g.
`bson.M{"_id": objID, "user_id": userID}`), not left to the handler to check
after the fact — that way it's structurally impossible to leak another user's
document through a missing check.

---

## Indexes

| Collection | Index | Purpose |
|---|---|---|
| `users` | `{email: 1}` unique | Enforce one account per email; also the lookup path for login. **Already implemented** in `EnsureIndexes`. |
| `applications` | `{user_id: 1}` | Base filter for every query — every list/get/patch/delete goes through this. **Not yet implemented — add this.** |
| `applications` | `{user_id: 1, status: 1}` | Serves the documented `GET /applications?status=` filter directly. **Not yet implemented — add this.** |

Deliberately **not** adding yet:
- A dedicated `applied_at` sort index — at personal-tool scale (tens to low
  thousands of documents per user), sorting the result of an indexed
  `user_id` lookup in memory is fast enough. Add it only if it's ever
  actually observed to be slow — indexes aren't free, they cost write
  throughput and disk on every insert/update.
- A text index on `notes`/`tags`/`company_name` for search — deferred until
  search is a real feature being built, not speculatively.

`EnsureIndexes` in `internal/store/mongo/db.go` currently only creates the
`users.email` index and needs extending to add the two `applications`
indexes above.

---

## Redis keyspace

| Key pattern | Type | Purpose |
|---|---|---|
| `session:<sid>` | Hash: `{user_id, email, user_name, lastLoginAttempt}` | Session state. TTL = `SESSION_TTL_HOURS` (config, default 168h). **Already implemented** in `internal/store/redis/session_store.go`. |

CSRF tokens are **not** in Redis — the double-submit-cookie pattern
(`CSRFMiddleWare`) only compares a cookie to a header; there's no server-side
CSRF state to store. Correctly minimal.

Not built now, worth knowing as a future Redis use case if login abuse ever
becomes a real concern: `login_attempts:<email>` counter with a short TTL,
to rate-limit brute-force login attempts. Not implemented — noted as an
option, not a plan.

---

## Robustness: resume upload write order

When `POST /applications/:id/resumes` is implemented (Milestone 8), the two
writes involved — the file landing on disk, and the `ResumeVersion` metadata
being appended to the `Application` document — are **not** atomic with each
other (different systems). The order matters:

**Write the file to disk first, generate its `StorageKey`, then update the
Mongo document to reference it.** If the Mongo update then fails, you're left
with an orphaned file on disk — harmless, just reclaimable wasted space,
cleanable later. If the order were reversed (metadata written first), a
failure after that point leaves a `ResumeVersion` pointing at a file that was
never written, which breaks the download endpoint the next time someone
clicks it. Prefer the failure mode that degrades silently over the one that
breaks a feature.

---

## Open item found while writing this doc

`API.md`'s extension-import endpoint (`POST /api/extension/applications`)
already documents a `"source": "linkedin"` field in its request body, but
`domain.Application` has no `Source` field to store it in — a small drift
between the two design docs. Recommend adding:

```go
Source string `bson:"source" json:"source"` // "manual" (default) | "extension:linkedin" | "extension:greenhouse" | ...
```

so it's always known whether an application was typed in by hand or imported
by the extension. Small, additive, no migration concerns since existing
documents would just decode with a zero-value (empty) `Source`. Not applied
to the code yet — flagging for you to make the call on the exact value
format and whether to backfill a default like `"manual"` for existing rows.
