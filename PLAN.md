# JSM Backend Plan

Job Search Manager backend: Go + Gin, session auth via Redis, data in MongoDB.
Core features: job application tracking, resume version uploads per application,
notes, compensation/benefits, tags.

## Review Findings (as of 2026-07-01)

### Won't compile
- `internal/auth/password.go` — `func LoginCredentialChecker()` has no body/signature.
- `internal/service/auth_service.go` — `Login` declares `(string, error)` return but has no `return` statement.
- `internal/store/mongo/db.go` — `FindByEmailAddrAndValidateUser(...) (userObj config.UserDbObj, error)` mixes named/unnamed return values.
- `cmd/api/main.go` — calls `auth.LoginAuthHandler()`, which doesn't exist (real handler is `handlers.AuthHandler.LoginHandler`).
- `internal/service/application_service.go` — byte-for-byte duplicate of `auth_service.go`. No real `ApplicationService` exists yet.

### Logic bugs
- `internal/auth/middleware.go` (`CSRFMiddleWare`) — safe-method branch calls `c.Next()` without `return`, falls through and still runs the CSRF check.
- `internal/auth/session.go` (`PopRedirectCookie`) — condition inverted (`if err != nil { val = sanitizeReturnTo(redirectURL) }`); can never recover a redirect cookie.
- `internal/http/handlers/auth_handler.go` — session-check condition is backwards (`err != nil && userId != ""` instead of `err == nil && userId != ""`).
- `internal/http/handlers/auth_handler.go` — none of the `AbortWithStatusJSON` calls are followed by `return` (Gin footgun: execution continues after abort).
- `internal/store/mongo/db.go` — Mongo filter uses `bson.M{"Email": email}` but `UserDbObj` tags the field `bson:"email"` (lowercase) — query never matches.
- Parameter mixup through the login chain: `AuthService.Login(ctx, username, password)` calls `FindByEmailAddrAndValidateUser(ctx, username, password)`, whose signature names params `(username, email)` and uses `email` as the Mongo filter — so password value gets used as the email lookup. No actual password/hash comparison exists anywhere.

### Structural issues
- Domain models (`UserDbObj`, `ApplicationObj`) live in `internal/config/config.go` instead of `internal/domain/` (currently empty stub files).
- No `json` tags on domain structs — `UserDbObj.PasswordHash` would leak if ever serialized directly in a response.
- `config.go` has no `Load()` — nothing reads `.env` yet.
- `routers.go`, `middleware/cors.go`, `middleware/logging.go`, `middleware/recover.go` are empty; nothing is wired together; `main.go` never calls `.Run()`.
- No `.git` repo or `.gitignore` yet — `.env` (has Mongo/Redis creds) is unprotected once a repo is initialized.
- Schema has no field(s) for resume file versions yet (storage key/path, filename, uploaded_at, link to application).
- No Mongo indexes planned (unique on `email`, compound on `UserId`+`Status`, maybe text index over notes/tags).

## Build Plan

1. **Foundation**
   - Add `config.Load()` to parse `.env` / process env into `Config`.
   - Move `UserDbObj` / `ApplicationObj` into `internal/domain`, add proper `json`/`bson` tags (hide `PasswordHash` from JSON).
   - Wire `main.go`: construct Mongo client, Redis client, session manager, services, handlers, router; call `.Run()`.
   - Init git repo + `.gitignore` (exclude `.env`, `.idea/`) before anything else touches version control.

2. **Auth correctness**
   - Fix all compile errors above.
   - Implement real password hashing/verification (bcrypt) in `password.go`.
   - Fix the Mongo field-case bug and the password/email parameter mixup.
   - Fix CSRF middleware early-return and session redirect-cookie bug.
   - Add a `Signup`/`Register` handler (only login exists today).
   - Add unique index on `email` in Mongo.

3. **Application CRUD**
   - Build a real `ApplicationService` (create/list/get/update/delete/status-change).
   - Wire routes in `routers.go` behind session + CSRF middleware.
   - Add compound index on `UserId` + `Status`; consider text index over `Notes`/`JobTags`.
   - Support filtering/sorting (by status, tag, applied date) and pagination for the list endpoint.

4. **Resume upload feature**
   - Decide storage backend (local disk vs. S3/MinIO vs. Mongo GridFS).
   - Add a `ResumeVersion` sub-document or separate collection linked to an application (filename, storage key, uploaded_at, optional label/notes).
   - Add upload/download/delete endpoints, with size/type validation.

5. **Middleware**
   - Fill in CORS, request logging, and panic recovery (currently empty stubs); wire into the Gin engine.

6. **Polish**
   - Request validation via Gin binding + `validator` tags.
   - Consistent error response shape across handlers.
   - Basic tests for auth flow and application CRUD.

## Status
Not started — plan only. Next step to decide: begin with step 1 (compile fixes + config + main.go wiring), or nail down the Mongo domain schema first since it touches everything downstream.
