# JSM Frontend — Design & Motion System

Companion to `DESIGN_GUIDE.md` (architecture) and `API.md` (the wire contract).
This document covers how the UI looks, how it moves, and how it defends itself
against the backend's JSON.

No runtime dependencies. No webfonts, no animation library. That isn't
minimalism for its own sake — the landing page's central claim is that nothing
leaves your machine, and a page that opens a connection to Google Fonts on load
contradicts it.

---

## 1. Static design

**Swiss / International Typographic Style** (Müller-Brockmann) supplies the
structure: a fixed spacing and type scale, hierarchy built from size and weight
rather than ornament, generous negative space, and hairline rules where most UI
kits would reach for a bordered box. The feature grid is the clearest case — the
grid already groups the items, so a border around each card would say it twice.

**Dieter Rams** supplies the restraint. The rule the palette follows: colour
beyond the single accent must *encode* something. That's why there is exactly
one family of non-accent hues — the six pipeline statuses — and why they appear
in the same three places every time (column top rule, header dot, card edge on
hover). Nothing is coloured to be lively.

### Tokens (`src/style.css`, `:root`)

| Group | Notes |
|---|---|
| Colour | Warm paper (`--paper: #f7f6f3`) against cool ink. Warm stock is a Swiss printing convention and keeps large white areas from reading clinical. |
| Status | `--status-{applied,phone_screen,onsite,offer,rejected,withdrawn}` plus a `-tint` for each. |
| Space | 4px base: `--s-1` … `--s-10`. |
| Type | Major third (1.25) on a 16px base. `--text-display` is a `clamp()`. |
| Elevation | `--shadow-1` … `--shadow-4`, **two layers each** — a tight contact shadow plus a soft ambient one. Single-layer shadows are the main reason UI depth reads as fake. |

Numerals that change in place (stat values, column counts) get `.tabular` so
they don't reflow as they tick.

---

## 2. Motion

Derived from **Disney's twelve principles** (Thomas & Johnston) by way of
**Material's** motion spec, which is itself a restatement of them for screens.

### The four rules

1. **Asymmetric easing.** Entering elements use `--ease-out`
   (`cubic-bezier(0.22, 1, 0.36, 1)`, a quint-out): they cover most of the
   distance immediately, then settle. The eye reads the *start* of a motion, so
   a fast start feels responsive and a slow one feels broken.
2. **Duration scales with distance.** `--dur-1` (120ms) for a hover tint through
   `--dur-5` (760ms) for a full headline. A button that eases over 500ms feels
   mushy; a page section that snaps over 120ms feels violent.
3. **Stagger, don't batch.** Related items arrive in sequence (~60ms apart).
   This is follow-through and overlapping action: a group arriving in one frame
   reads as a slab, a group arriving in sequence reads as related-but-distinct
   items. The dashboard deliberately uses two different rates — columns sweep
   across at 70ms, cards drop within a column at 55ms — so the board reads as a
   wave rather than a grid switching on.
4. **Only `transform` and `opacity`.** Both are composited, so neither triggers
   layout or paint. Everything else is a frame-rate problem waiting to happen.

Rule 4 has exactly two deliberate exceptions, both documented at their call
site: the feature icons' `stroke-dashoffset` redraw (a 21px SVG on hover, where
no transform can express "redraw"), and the `pipeline-seg` widths, which are set
once via `flex-grow` and never animated — only the fill inside them scales.

### Where the layout itself has to move: FLIP

Moving a card between columns *is* a layout change. `flipMove()` in
`src/motion.ts` uses **FLIP** (Paul Lewis): measure First, move it in the DOM
and measure Last, Invert with a transform back to the start, then Play. The
element is laid out twice and the travel is a pure transform. This drives the
landing page's live board demo.

### Reduced motion

`prefers-reduced-motion: reduce` collapses everything to the end state — no
travel, no loops, no parallax. Elements still *appear*, they just don't animate
in. `prefersReducedMotion()` is checked live rather than cached, so toggling the
OS setting takes effect without a reload, and the JS-driven loops
(`loopWhileVisible`, the board demo, `countUp`) opt out at the source rather
than animating invisibly.

### Not animating is also a decision

Idle loops stop when off-screen or backgrounded (`loopWhileVisible`), reveals
fire once rather than replaying on every scroll, and `will-change` is dropped
after an element lands (`.is-settled`) instead of holding a compositor layer for
the life of the page.

### Failing safe

Every "start hidden" rule is scoped to `html.js`, a class an inline script in
each document head sets before first paint. With scripting off or a module that
fails to load, none of those rules match and the pages render as ordinary static
content rather than as a blank screen.

---

## 3. The API boundary

`src/types.ts` carries **two** layers, and the split is load-bearing:

- `Wire*` — exactly what the Go handlers put on the wire.
- plain (`Application`, `User`) — the normalized shape the UI consumes, where
  every collection is guaranteed to be an array and every optional field is
  explicitly `null`.

The reason is that Go has two different ways of saying "empty" and they don't
look the same in JSON:

```go
[]string                    nil  ->  null      // key present, value null
[]T   with `,omitempty`     nil  ->  <absent>  // key not emitted at all
*T    with `,omitempty`     nil  ->  <absent>
```

In `internal/domain/application.go`, `location` and `tags` have no `omitempty`,
so they arrive as `null`; `compensation`, `resumes`, and `next_follow_up_at` do
have it, so they arrive as `undefined`. Declaring all five as plain required
properties — which is what the frontend did before — means `app.tags.slice(0, 2)`
throws on the first application saved without tags.

`normalizeApplication()` in `src/api.ts` collapses both cases at the boundary,
so nothing downstream has to think about it. `mockData.ts` is typed as
`WireApplication[]` and its last row (Soylent Corp) is deliberately the sparsest
possible record — `null` slices, every `omitempty` field absent — so a
regression in the normalizer fails in development rather than in production.

Two related notes:

- **Paging is real, not stubbed.** `getApplications()` walks every page of the
  `{applications, page, page_size, total}` envelope. The dashboard's stat tiles
  and pipeline bar are aggregates, so a single page would silently report wrong
  totals past `page_size` with nothing on screen to indicate truncation.
- **Unknown statuses are surfaced, not rewritten.** `status` is an unvalidated
  `string` server-side. `normalizeStatus()` warns and passes the value through
  rather than coercing it to a real status, which would misreport the pipeline.

### Still to do before the mock comes out

Tracked as `TODO`s in `src/api.ts`:

- CSRF: every mutating request needs an `X-CSRF-TOKEN` header echoing the CSRF
  cookie, plus `credentials: "include"`. The cookie is correctly not `HttpOnly`,
  but its **name isn't configured server-side yet** — `NewSessionManager` takes a
  `csrfCookieName` and has no callers.
- `isAuthenticated()` must become async (`GET /me`, 401 = logged out). The
  session cookie is `HttpOnly`, so JS can never read it directly. Callers
  currently branch synchronously at module load; that shape has to change.
- `GET /me` returns a `{"user": {…}}` envelope to unwrap.
- `POST /login`: `API.md` documents `{"user": {…}}`; the implemented handler
  returns `{"status": "ok"}`. Worth reconciling — returning the user would let
  the dashboard skip a round trip.
