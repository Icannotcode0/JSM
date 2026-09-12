import { login } from "./api";
import { revealAllNow, revealNow, stagger } from "./motion";

const form = document.querySelector<HTMLFormElement>("#login-form")!;
const errorEl = document.querySelector<HTMLParagraphElement>("#form-error")!;
const submitBtn = document.querySelector<HTMLButtonElement>("#submit-btn")!;
const submitLabel = document.querySelector<HTMLElement>("#submit-label")!;
const card = document.querySelector<HTMLElement>(".auth-card")!;

// The card scales up as it rises (see .auth-card[data-reveal] in style.css), so
// it reads as coming toward you rather than being pushed up from below. Its
// contents follow once it has landed — the container arrives, then fills.
const fields = form.querySelectorAll<HTMLElement>("[data-reveal]");
stagger(fields, 55, 180);

requestAnimationFrame(() => {
  revealNow(card);
  revealAllNow(fields);
});

function setBusy(busy: boolean): void {
  submitBtn.disabled = busy;
  submitLabel.textContent = busy ? "Logging in…" : "Log in";

  const existing = submitBtn.querySelector(".btn-spinner");
  if (busy && !existing) {
    const spinner = document.createElement("span");
    spinner.className = "btn-spinner";
    submitBtn.prepend(spinner);
  } else if (!busy && existing) {
    existing.remove();
  }
}

// Both routes into this page are redirects the user didn't explicitly ask for,
// so each says why. Without it, a password change reads as being logged out at
// random, and a successful signup reads as having failed.
const NOTICES: Record<string, string> = {
  "password-changed": "Password changed. Sign in with your new password.",
  "account-created": "Account created. Sign in to get started.",
  "session-expired": "Your session expired. Sign in again to continue.",
};

const params = new URLSearchParams(window.location.search);
for (const [param, message] of Object.entries(NOTICES)) {
  if (!params.has(param)) continue;

  const notice = document.querySelector<HTMLElement>("#login-notice");
  if (notice) {
    notice.textContent = message;
    notice.hidden = false;
  }
  // Drop the query string so a refresh doesn't repeat the message.
  window.history.replaceState({}, "", window.location.pathname);
  break;
}

form.addEventListener("submit", async (e) => {
  e.preventDefault();

  // Fully remove the error before re-showing it, so the shake keyframe replays
  // on a second failed attempt instead of staying finished from the first.
  errorEl.hidden = true;

  const email = (form.elements.namedItem("email") as HTMLInputElement).value.trim();
  const password = (form.elements.namedItem("password") as HTMLInputElement).value;

  setBusy(true);
  const result = await login(email, password);

  if (!result.ok) {
    errorEl.textContent = result.error;
    errorEl.hidden = false;
    setBusy(false);
    return;
  }

  window.location.href = "/dashboard";
});
