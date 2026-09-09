# JSM — Job Search Manager

  A local-first job application tracker. Log every application, the resume version you sent, compensation notes, and where each one stands — on a pipeline board that runs entirely on your own machine.

  No cloud, no accounts, no telemetry. Mongo and Redis run in Docker on loopback, the Go server binds `127.0.0.1`, and nothing leaves your computer.

  ---

  ## Why local-only

  This is a single-user personal tool holding a fairly sensitive dataset: where you're applying, what you're paid, and what you said about it. The simplest way to keep that private is not to host it.

  That constraint shapes the design throughout — including the security model, which deliberately does *not* assume "local means trusted". Any page in another browser tab can attempt a drive-by `fetch()` to `localhost`, and cookies aren't port-scoped, so a page on any other localhost port shares this one's cookie jar. The backend is written as if it were internet-facing.

  ---

  ## Stack

  | Layer | Choice |
  |---|---|
  | Backend | Go 1.24, `net/http` (stdlib `ServeMux`, method + wildcard patterns) |
  | Database | MongoDB 7 — applications and users |
  | Sessions | Redis 7 — session hashes with a TTL |
  | Frontend | TypeScript + Vite, zero runtime dependencies |
  | Extension | Chrome MV3 (auto-capture from job boards — in progress) |

  No web framework, no frontend framework, no CSS library. A page advertising that nothing leaves your machine shouldn't open a connection to a font CDN on load.

  ---

  ## Getting started

  **Requires:** Go 1.24+, Node 18+, Docker.

  ```bash
  git clone git@github.com:Icannotcode0/JSM.git
  cd JSM

  # 1. Databases (both bind to 127.0.0.1 only)
  docker compose up -d

  # 2. Config
  cp .env.example .env
  #    Optional but recommended:
  #      echo "SESSION_SECRET=$(openssl rand -base64 32)" >> .env

  # 3. Backend  — http://127.0.0.1:8080
  cd backend && go run ./cmd/api

  # 4. Frontend — http://localhost:5173  (separate terminal)
  cd frontend && npm install && npm run dev
  ```

  > **Note:** `config.Load()` reads `.env` relative to the process working directory. Running the server from `backend/` means it won't be found and every value silently falls back to its default. Run from the repo root, or export the variables.

  ### Creating an account

  `POST /signup` isn't built yet, so seed a user directly:

  ```bash
  cd backend
  cat > /tmp/seed.go <<'EOF'
  package main

  import (
        "context"
        "fmt"
        "time"

        "go.mongodb.org/mongo-driver/v2/bson"
        "go.mongodb.org/mongo-driver/v2/mongo"
        "go.mongodb.org/mongo-driver/v2/mongo/options"
        "golang.org/x/crypto/bcrypt"
  )

  func main() {
        ctx := context.Background()
        c, err := mongo.Connect(options.Client().ApplyURI("mongodb://127.0.0.1:27017"))
        if err != nil {
                panic(err)
        }
        defer c.Disconnect(ctx)

        hash, err := bcrypt.GenerateFromPassword([]byte("change-this-password"), bcrypt.DefaultCost)
        if err != nil {
                panic(err)
        }

        res, err := c.Database("jobtracker").Collection("users").InsertOne(ctx, bson.M{
                "email":         "you@example.com",
                "password_hash": string(hash),
                "name":          "Your Name",
                "created_at":    time.Now(),
                "updated_at":    time.Now(),
        })
        if err != nil {
                panic(err)
        }
        fmt.Println("created user:", res.InsertedID)
  }
  EOF
  go run /tmp/seed.go && rm /tmp/seed.go
  ```

  Then sign in at `http://localhost:5173/login`.

  ---

  ## API

  Base URL `http://127.0.0.1:8080`. Full reference in [`API.md`](API.md).

  | Method | Path | Auth | Status |
  |---|---|---|---|
  | `GET` | `/health` | — | ✅ |
  | `POST` | `/login` | CSRF | ✅ |
  | `POST` | `/logout` | CSRF | ✅ |
  | `GET` | `/me` | session | ✅ |
  | `GET` | `/applications` | session | ✅ |
  | `POST` | `/applications` | session + CSRF | ✅ |
  | `GET` | `/applications/{id}` | session | ✅ |
  | `PATCH` | `/applications/{id}` | session + CSRF | ✅ |
  | `DELETE` | `/applications/{id}` | session + CSRF | ✅ |
  | `POST` | `/signup` | CSRF | 📝 planned |
  | `POST` | `/applications/{id}/resumes` | session + CSRF | 📝 planned |
  | `GET` | `/applications/{id}/resumes/{resumeId}` | session | 📝 planned |
  | `POST` | `/api/extension/applications` | session + CSRF + origin | 📝 planned |

  `GET /applications` accepts `?status=`, `?tag=`, `?q=` (substring search over company, role, tags, and notes), `?page=`, and `?page_size=`, returning `{applications, page, page_size, total}`.

  ### Conventions

  Success is `{"<resource>": {...}}`; errors are `{"error": "..."}`.

  Every mutating request needs a session cookie **and** an `X-CSRF-TOKEN` header echoing the `jsm_csrf` cookie. That cookie is issued by any safe request, so a cold client calls `GET /health` first to obtain one.

  ---

  ## Security model

  Notes on the decisions that aren't obvious from the code:

  **CSRF tokens are signed and session-bound.** A plain double-submit cookie assumes an attacker can't write cookies into your browser. That assumption is weak here: cookies ignore ports, so a page on any other `localhost` origin shares this one's jar and could choose *both* halves of the pair. `SameSite` doesn't help either, since "site" also ignores the port. Tokens are therefore `<nonce>!<sessionID>.<HMAC>` — valid only if this server minted them *and* they're bound to the session presenting them. Binding also gives rotation for free: logging in changes the session ID, retroactively invalidating every token issued before the privilege change.

  **Login is constant-time across both failure modes.** Returning the same error message for "no such user" and "wrong password" is pointless if only one of them runs bcrypt — the ~30× timing gap enumerates accounts just as well. The unknown-user path burns an equivalent bcrypt comparison against a throwaway hash.

  **Ownership lives in the query filter, not a post-read check.** Every application query is scoped by `user_id` inside the filter itself, so there's no code path that can read or write another user's document. A record belonging to someone else returns the same `404` as one that doesn't exist.

  **Routing is protected by default.** Authenticated routes sit on their own mux wrapped once in `SessionRequired`; the public surface is a three-line list, and everything else falls through to the protected group. Forgetting to guard a new route is therefore impossible — the failure mode is a `401` you notice immediately, not a silently public endpoint.

  **All input is treated as untrusted,** including on the endpoints only the UI talks to. Length caps, HTML escaping at write time, `job_link` restricted to `http`/`https` (`javascript:`, `data:`, and `file:` are rejected), `regexp.QuoteMeta` on search terms before they reach Mongo, and a 1 MiB body cap. The browser extension will eventually POST scraped page content into the same models, and one validation path is safer than a "trusted" and an "untrusted" one that drift apart.

  ### Known gaps

  - No rate limiting on `/login` — bcrypt gives ~60 ms of natural throttling, nothing more.
  - The CSRF cookie hardcodes `Secure: true`, which Safari rejects over `http://localhost`. Chrome and Firefox accept it.
  - `SESSION_SECRET` unset means a random per-boot signing key: safe, but outstanding CSRF tokens don't survive a restart.

  ---

  ## Layout

  ```
  backend/
    cmd/api/            entrypoint — wiring, lifecycle, graceful shutdown
    internal/
      http/             router (public vs. authenticated) + handlers
      service/          business logic and all validation
      store/            persistence interfaces + Mongo implementations
      authentication/   sessions, CSRF, password hashing
      common/           mongoWrap, redisWrap, jsmHttp, logbuilder, metrics
      domain/           wire and storage models
  frontend/
    src/                api client, dashboard, motion system
  extension/            Chrome MV3 auto-capture (in progress)
  ```

  Dependencies point inward: `http` → `service` → `store`. Handlers depend on a single-capability interface rather than the whole service aggregate, so each is testable with a one-method fake.

  ---

  ## Documentation

  | File | Contents |
## Documentation

| File | Contents |
|---|---|
| [`API.md`](API.md) | Endpoint reference, auth model, error codes |
| [`DATABASE.md`](DATABASE.md) | Schema, embed-vs-reference reasoning, indexes, multi-tenancy rule |
| [`DESIGN_GUIDE.md`](DESIGN_GUIDE.md) | Architecture and build order by milestone |
| [`frontend/DESIGN_SYSTEM.md`](frontend/DESIGN_SYSTEM.md) | Visual language, motion principles, the API boundary |

---

## Roadmap

- [x] Auth: sessions, CSRF, login/logout
- [x] Applications: full CRUD, filter, search, pagination
- [x] Dashboard: pipeline board, stats, inline editor
- [ ] `POST /signup`
- [ ] Resume uploads (Milestone 8)
- [ ] Browser extension auto-capture (Milestone 14)
- [ ] Rate limiting on auth endpoints
Tip: Use /btw to ask a quick side question without interrupting Claude's current work
