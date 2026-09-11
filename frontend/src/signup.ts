import { signUp } from "./api";
import { revealAllNow, revealNow, stagger } from "./motion";

const form = document.querySelector<HTMLFormElement>("#signup-form")!;
const errorEl = document.querySelector<HTMLParagraphElement>("#form-error")!;
const submitBtn = document.querySelector<HTMLButtonElement>("#submit-btn")!;
const submitLabel = document.querySelector<HTMLElement>("#submit-label")!;
const card = document.querySelector<HTMLElement>(".auth-card")!;

// Mirrors login: the card rises and scales, then its contents follow once it
// has landed — the container arrives, then fills.
const fields = form.querySelectorAll<HTMLElement>("[data-reveal]");
stagger(fields, 55, 180);

requestAnimationFrame(() => {
  revealNow(card);
  revealAllNow(fields);
});

function setBusy(busy: boolean): void {
  submitBtn.disabled = busy;
  submitLabel.textContent = busy ? "Creating account…" : "Create account";

  const existing = submitBtn.querySelector(".btn-spinner");
  if (busy && !existing) {
    const spinner = document.createElement("span");
    spinner.className = "btn-spinner";
    submitBtn.prepend(spinner);
  } else if (!busy && existing) {
    existing.remove();
  }
}

function showError(message: string): void {
  errorEl.textContent = message;
  errorEl.hidden = false;
}

form.addEventListener("submit", async (e) => {
  e.preventDefault();

  // Fully remove the error before re-showing it, so the shake keyframe replays
  // on a second failed attempt instead of staying finished from the first.
  errorEl.hidden = true;

  const name = (form.elements.namedItem("name") as HTMLInputElement).value.trim();
  const email = (form.elements.namedItem("email") as HTMLInputElement).value.trim();
  const password = (form.elements.namedItem("password") as HTMLInputElement).value;

  // Only presence is checked here. Address shape, password policy, and whether
  // the address is taken are all the server's calls — duplicating them would
  // give two sources of truth that drift, and the server's messages are already
  // written for a person to read.
  if (!name || !email || !password) {
    showError("Name, email, and password are all required.");
    return;
  }

  setBusy(true);
  const result = await signUp(email, password, name);

  if (!result.ok) {
    showError(result.error);
    setBusy(false);
    return;
  }

  // Signup does not sign you in — the server returns no session cookie — so the
  // next step is the login page, told why it is showing.
  window.location.href = "/login?account-created=1";
});
