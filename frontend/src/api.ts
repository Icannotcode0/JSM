// API client — live against the Go backend.
//
// Everything the UI needs goes through request() below, which owns the three
// things every call has to get right: send the session cookie, attach a CSRF
// token to mutating requests, and turn the server's machine-readable error
// codes into prose. Nothing above this file touches fetch directly.
import type {
  Application,
  ApplicationInput,
  ApplicationPage,
  ApplicationQuery,
  User,
  WireApplication,
  WireApplicationPage,
} from "./types";
import { isApplicationStatus } from "./types";

/* -------------------------------------------------------------------------
   CSRF
   -------------------------------------------------------------------------
   Double-submit cookie (API.md "Conventions"): the server sets a CSRF cookie on
   any safe request, and every mutating request must echo that value back in an
   X-CSRF-TOKEN header. The cookie is deliberately not HttpOnly so this code can
   read it — that is what makes the pattern work, and it stays safe because a
   page on another origin cannot read cookies for this one.

   Server-side the token is signed and bound to the session, so it can become
   invalid without the client doing anything wrong (the session changed, or the
   server restarted with a new signing secret). ensureCsrfToken(true) re-fetches
   for exactly that case.

   Must match CSRF_COOKIE_NAME server-side (internal/config/config.go).
   ------------------------------------------------------------------------- */

const CSRF_COOKIE = "jsm_csrf";
const CSRF_HEADER = "X-CSRF-TOKEN";

function readCookie(name: string): string | null {
  for (const part of document.cookie.split("; ")) {
    const eq = part.indexOf("=");
    if (eq !== -1 && part.slice(0, eq) === name) {
      return decodeURIComponent(part.slice(eq + 1));
    }
  }
  return null;
}

async function ensureCsrfToken(force = false): Promise<string> {
  if (!force) {
    const existing = readCookie(CSRF_COOKIE);
    if (existing) return existing;
  }

  // Any GET issues the cookie; /health is the cheapest and needs no session.
  await fetch("/health", { credentials: "include" });

  const token = readCookie(CSRF_COOKIE);
  if (!token) {
    throw new ApiError(
      `no ${CSRF_COOKIE} cookie after GET /health — check the server is running`,
      0,
    );
  }
  return token;
}

/* -------------------------------------------------------------------------
   Errors
   ------------------------------------------------------------------------- */

export class ApiError extends Error {
  readonly status: number;
  constructor(message: string, status: number) {
    super(message);
    this.name = "ApiError";
    this.status = status;
  }
}

// The server's error envelope carries machine codes ("INCORRECT_CREDENTIALS"),
// not prose. Translating them here keeps the copy in one place instead of
// leaking SCREAMING_SNAKE_CASE into the UI. Validation messages from the
// service layer are already human-readable and fall through unchanged.
const ERROR_COPY: Record<string, string> = {
  INCORRECT_CREDENTIALS: "That email and password don't match.",
  BAD_REQUEST: "Please check the details you entered.",
  INTERNAL_SERVER_ERROR: "Something went wrong on the server.",
  SERVICE_UNAVAILABLE: "The server can't reach its database right now.",
  REQUEST_TOO_LARGE: "That request was too large.",
  NOT_FOUND: "That application no longer exists.",
  EMAIL_ALREADY_REGISTERED: "An account with that email already exists.",
  UNAUTHORIZED: "Please sign in again.",
  TOO_MANY_REQUESTS: "Too many attempts. Please wait a moment and try again.",
  unauthorized: "Please sign in again.",
  "csrf token missing": "Your session expired. Reload the page and try again.",
  "csrf token mismatch": "Your session expired. Reload the page and try again.",
  "csrf token invalid": "Your session expired. Reload the page and try again.",
};

async function toApiError(res: Response): Promise<ApiError> {
  let code = "";
  try {
    code = ((await res.json()) as { error?: string }).error ?? "";
  } catch {
    // Non-JSON body (a proxy error page, say) — fall through to the status.
  }

  // A 429 carries Retry-After. Folding it into the message turns "try again
  // later" into something the user can actually act on, and the server
  // deliberately says nothing else about why they were throttled.
  if (res.status === 429) {
    const seconds = Number(res.headers.get("Retry-After"));
    if (Number.isFinite(seconds) && seconds > 0) {
      return new ApiError(`Too many attempts. Try again in ${formatWait(seconds)}.`, 429);
    }
  }

  return new ApiError(ERROR_COPY[code] || code || `Request failed (${res.status}).`, res.status);
}

/** "45 seconds", "5 minutes", "1 hour" — whole units, because a live countdown
 *  would imply more precision than a fixed-window limiter actually offers.
 *
 *  Hours matter: the sustained rules run for an hour, so a tripped one yields
 *  values up to 3600 and "60 minutes" reads worse than "1 hour". */
function formatWait(seconds: number): string {
  const plural = (n: number, unit: string) => `${n} ${unit}${n === 1 ? "" : "s"}`;

  if (seconds < 60) return plural(Math.ceil(seconds), "second");
  if (seconds < 3600) return plural(Math.ceil(seconds / 60), "minute");
  return plural(Math.ceil(seconds / 3600), "hour");
}

/* -------------------------------------------------------------------------
   Request
   ------------------------------------------------------------------------- */

const MUTATING = new Set(["POST", "PATCH", "PUT", "DELETE"]);

interface RequestOptions {
  method?: string;
  body?: unknown;
  /** Treat 401 as a value rather than an error — used by getMe(). */
  allowUnauthorized?: boolean;
}

async function request<T>(path: string, options: RequestOptions = {}): Promise<T | null> {
  const method = options.method ?? "GET";
  const needsToken = MUTATING.has(method);

  const send = async (token?: string): Promise<Response> => {
    const headers: Record<string, string> = {};
    if (options.body !== undefined) headers["Content-Type"] = "application/json";
    if (token) headers[CSRF_HEADER] = token;

    return fetch(path, {
      method,
      // Same-origin through the Vite proxy in dev, and same-origin for real
      // once Go serves the pages — but stated explicitly so a future split
      // origin doesn't silently stop sending the session cookie.
      credentials: "include",
      headers,
      body: options.body === undefined ? undefined : JSON.stringify(options.body),
    });
  };

  let res: Response;
  try {
    res = await send(needsToken ? await ensureCsrfToken() : undefined);

    // A 403 on a mutating call is a CSRF rejection, not a permissions failure.
    // Retry once with a freshly minted token, which recovers from a stale
    // binding or a restarted server. A second 403 is a real error.
    //
    // Note this deliberately does not retry a 429: the whole point of being
    // throttled is that retrying immediately is what got you here.
    if (needsToken && res.status === 403) {
      res = await send(await ensureCsrfToken(true));
    }
  } catch (err) {
    if (err instanceof ApiError) throw err;
    throw new ApiError("Can't reach the server.", 0);
  }

  if (res.status === 401 && options.allowUnauthorized) return null;
  if (!res.ok) throw await toApiError(res);
  if (res.status === 204) return null;

  return (await res.json()) as T;
}

/* -------------------------------------------------------------------------
   Normalization
   -------------------------------------------------------------------------
   Go has two ways of writing "empty" and they don't look the same in JSON: a
   nil slice without omitempty becomes null, and anything with omitempty is
   absent entirely. Collapsing both here means nothing downstream has to guard
   before calling .length or .slice(). See types.ts for the full mapping.
   ------------------------------------------------------------------------- */

function normalizeApplication(wire: WireApplication): Application {
  const status = isApplicationStatus(wire.status) ? wire.status : undefined;
  if (!status) {
    // The backend validates status on write, so this means data predating that
    // validation. Surface it rather than silently remapping it to a real
    // status, which would misreport the pipeline.
    console.warn(`[api] unknown application status: ${JSON.stringify(wire.status)}`);
  }

  return {
    ...wire,
    status: (status ?? wire.status) as Application["status"],
    location: wire.location ?? [],
    tags: wire.tags ?? [],
    resumes: wire.resumes ?? [],
    compensation: wire.compensation ?? null,
    next_follow_up_at: wire.next_follow_up_at ?? null,
  };
}

/* -------------------------------------------------------------------------
   Auth
   ------------------------------------------------------------------------- */

export type LoginResult = { ok: true } | { ok: false; error: string };

export async function login(email: string, password: string): Promise<LoginResult> {
  if (!email || !password) {
    return { ok: false, error: "Email and password are required." };
  }
  try {
    await request("/login", { method: "POST", body: { email, password } });
    return { ok: true };
  } catch (err) {
    return { ok: false, error: err instanceof Error ? err.message : "Login failed." };
  }
}

export type SignUpResult = { ok: true; user: User } | { ok: false; error: string };

/**
 * Create an account.
 *
 * Deliberately does not sign the new user in: the server answers 201 with the
 * user and no session cookie, so the caller sends them to the login page. That
 * keeps one code path for "become authenticated" rather than two, and it is the
 * path that has to keep working once email verification stands between signing
 * up and being allowed in.
 *
 * The server owns every rule — address shape, password policy, whether the
 * address is taken — and its messages are already prose, so they surface
 * unchanged rather than being second-guessed here.
 */
export async function signUp(
  email: string,
  password: string,
  name: string,
): Promise<SignUpResult> {
  try {
    const body = await request<{ user: User }>("/signup", {
      method: "POST",
      body: { email, password, name },
    });
    return { ok: true, user: body!.user };
  } catch (err) {
    return { ok: false, error: err instanceof Error ? err.message : "Could not create the account." };
  }
}

/**
 * The signed-in user, or null when there's no valid session.
 *
 * This is the only real authentication check available to the client: the
 * session cookie is HttpOnly, so JS can never read it, and a 401 from /me is
 * the single source of truth for "logged out".
 */
export async function getMe(): Promise<User | null> {
  const body = await request<{ user: User }>("/me", { allowUnauthorized: true });
  return body?.user ?? null;
}

/**
 * End the session. The server destroys it in Redis and returns cookies that
 * clear the session and rebind CSRF to the anonymous state.
 *
 * Errors are swallowed on purpose: the caller navigates to /login either way,
 * and refusing to move because the sign-out request failed would strand the
 * user on a page they've already asked to leave. The server treats logout as
 * idempotent, so a retry is always safe.
 */
export async function logout(): Promise<void> {
  try {
    await request("/logout", { method: "POST" });
  } catch (err) {
    console.warn("[api] logout request failed", err);
  }
}

/**
 * Change the signed-in user's password.
 *
 * On success the server **destroys the current session** — it clears the
 * session cookie and rebinds CSRF to the anonymous state — so the caller must
 * send the user back to sign in with the new password. Continuing to render the
 * dashboard afterwards would show a UI whose every subsequent request 401s.
 *
 * Rejections come back as ApiError with the server's own wording (which rule
 * the new password missed, or that the current one was wrong), so callers can
 * surface `err.message` directly rather than re-implementing the policy here
 * and drifting from it.
 */
export async function changePassword(
  currentPassword: string,
  newPassword: string,
): Promise<void> {
  try {
    await request("/reset-password", {
      method: "POST",
      body: { current_password: currentPassword, new_password: newPassword },
    });
  } catch (err) {
    // The server reuses INCORRECT_CREDENTIALS here, which ERROR_COPY words for
    // the login screen ("that email and password don't match"). No email is
    // involved in this form, so re-word it rather than confusing the user about
    // which field is wrong.
    if (err instanceof ApiError && err.status === 401) {
      throw new ApiError("Your current password is incorrect.", 401);
    }
    throw err;
  }
}

/* -------------------------------------------------------------------------
   Applications
   ------------------------------------------------------------------------- */

export async function listApplications(query: ApplicationQuery = {}): Promise<ApplicationPage> {
  const params = new URLSearchParams();
  if (query.status) params.set("status", query.status);
  if (query.tag) params.set("tag", query.tag);
  if (query.search) params.set("q", query.search);
  if (query.page) params.set("page", String(query.page));
  if (query.pageSize) params.set("page_size", String(query.pageSize));

  const qs = params.toString();
  const body = await request<WireApplicationPage>(`/applications${qs ? `?${qs}` : ""}`);

  return {
    applications: (body?.applications ?? []).map(normalizeApplication),
    page: body?.page ?? 1,
    pageSize: body?.page_size ?? 0,
    total: body?.total ?? 0,
  };
}

/**
 * Every application matching `query`, across all pages.
 *
 * The board renders every column at once and the stat tiles are aggregates, so
 * a single page isn't enough: past page_size the totals would be confidently
 * wrong with nothing on screen to indicate truncation.
 */
export async function listAllApplications(query: ApplicationQuery = {}): Promise<Application[]> {
  const PAGE_SIZE = 200; // the server's maximum
  const MAX_PAGES = 50; // stops a bad `total` becoming an infinite loop
  const all: Application[] = [];

  for (let page = 1; page <= MAX_PAGES; page += 1) {
    const result = await listApplications({ ...query, page, pageSize: PAGE_SIZE });
    all.push(...result.applications);

    // An empty page means we've run off the end regardless of what `total`
    // claims, so check it before trusting the count.
    if (result.applications.length === 0) break;
    if (all.length >= result.total) break;
  }
  return all;
}

export async function createApplication(input: ApplicationInput): Promise<Application> {
  const body = await request<{ application: WireApplication }>("/applications", {
    method: "POST",
    body: input,
  });
  return normalizeApplication(body!.application);
}

export async function updateApplication(
  id: string,
  patch: Partial<ApplicationInput>,
): Promise<Application> {
  const body = await request<{ application: WireApplication }>(`/applications/${id}`, {
    method: "PATCH",
    body: patch,
  });
  return normalizeApplication(body!.application);
}

export async function deleteApplication(id: string): Promise<void> {
  await request(`/applications/${id}`, { method: "DELETE" });
}
