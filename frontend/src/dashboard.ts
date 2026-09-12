import {
  changePassword,
  createApplication,
  deleteApplication,
  getMe,
  listAllApplications,
  logout,
  updateApplication,
} from "./api";
import type { Application, ApplicationInput, ApplicationStatus, User } from "./types";
import { APPLICATION_STATUSES } from "./types";
import { countUp, prefersReducedMotion, revealAllNow, revealNow, stagger } from "./motion";

const STATUS_LABELS: Record<ApplicationStatus, string> = {
  applied: "Applied",
  phone_screen: "Phone Screen",
  onsite: "Onsite",
  offer: "Offer",
  rejected: "Rejected",
  withdrawn: "Withdrawn",
};

/** Every status has a hue reserved for it in style.css; nothing else is coloured. */
function statusColor(status: ApplicationStatus): string {
  return `var(--status-${status})`;
}

const board = document.querySelector<HTMLElement>("#board")!;
const statsEl = document.querySelector<HTMLElement>("#stats")!;
const pipelineEl = document.querySelector<HTMLElement>("#pipeline")!;
const boardError = document.querySelector<HTMLElement>("#board-error")!;
const searchInput = document.querySelector<HTMLInputElement>("#search-input")!;
const statusFilter = document.querySelector<HTMLElement>("#status-filter")!;
const clearFilters = document.querySelector<HTMLButtonElement>("#clear-filters")!;
const resultCount = document.querySelector<HTMLElement>("#result-count")!;

/* -------------------------------------------------------------------------
   State
   -------------------------------------------------------------------------
   `all` is the unfiltered set fetched from the server; `filtered` is what the
   board shows. Filtering happens client-side because the board renders every
   column at once — a server round-trip per keystroke would fetch data we
   already hold, and the stat tiles must stay totals of everything regardless of
   the current filter.
   ------------------------------------------------------------------------- */

let all: Application[] = [];
let search = "";
let statusOf: ApplicationStatus | "" = "";

function initials(name: string): string {
  return name
    .split(" ")
    .map((part) => part[0])
    .join("")
    .slice(0, 2)
    .toUpperCase();
}

function formatDate(iso: string): string {
  return new Date(iso).toLocaleDateString(undefined, { month: "short", day: "numeric" });
}

/** yyyy-mm-dd for <input type="date">, in local time so the shown day matches. */
function toDateInput(iso: string | null): string {
  if (!iso) return "";
  const d = new Date(iso);
  const local = new Date(d.getTime() - d.getTimezoneOffset() * 60000);
  return local.toISOString().slice(0, 10);
}

function parseList(value: string): string[] {
  return value
    .split(",")
    .map((s) => s.trim())
    .filter(Boolean);
}

/* -------------------------------------------------------------------------
   Filtering
   ------------------------------------------------------------------------- */

function applyFilters(): Application[] {
  const q = search.trim().toLowerCase();

  return all.filter((app) => {
    if (statusOf && app.status !== statusOf) return false;
    if (!q) return true;

    // Same fields the server's ?q= searches, so switching to server-side
    // filtering later wouldn't change what matches.
    return (
      app.company_name.toLowerCase().includes(q) ||
      app.position_title.toLowerCase().includes(q) ||
      app.notes.toLowerCase().includes(q) ||
      app.tags.some((t) => t.toLowerCase().includes(q))
    );
  });
}

function renderFilterChips(): void {
  statusFilter.innerHTML = "";

  const options: { key: ApplicationStatus | ""; label: string }[] = [
    { key: "", label: "All" },
    ...APPLICATION_STATUSES.map((key) => ({ key, label: STATUS_LABELS[key] })),
  ];

  for (const option of options) {
    const chip = document.createElement("button");
    chip.type = "button";
    chip.className = "chip";
    chip.textContent = option.label;
    chip.setAttribute("aria-pressed", String(statusOf === option.key));
    if (option.key) chip.style.setProperty("--chip-color", statusColor(option.key));
    if (statusOf === option.key) chip.classList.add("is-active");

    chip.addEventListener("click", () => {
      statusOf = option.key;
      renderFilterChips();
      renderFiltered();
    });
    statusFilter.appendChild(chip);
  }
}

function renderFiltered(): void {
  const visible = applyFilters();
  renderBoard(visible);

  const filtering = search.trim() !== "" || statusOf !== "";
  clearFilters.hidden = !filtering;
  resultCount.textContent = filtering
    ? `${visible.length} of ${all.length} shown`
    : `${all.length} application${all.length === 1 ? "" : "s"}`;
}

/* -------------------------------------------------------------------------
   Stats
   ------------------------------------------------------------------------- */

interface Stat {
  label: string;
  value: number;
  hint: string;
  color: string;
}

function buildStats(apps: Application[]): Stat[] {
  const by = (...statuses: ApplicationStatus[]) =>
    apps.filter((a) => statuses.includes(a.status)).length;

  const total = apps.length;
  // "Heard back" = anything that moved past the initial application, however it
  // ended. It's the number that actually tells you if the resume is working.
  const heardBack = by("phone_screen", "onsite", "offer", "rejected");
  const responseRate = total === 0 ? 0 : Math.round((heardBack / total) * 100);

  return [
    { label: "Applications", value: total, hint: "tracked all time", color: "var(--accent)" },
    {
      label: "Active",
      value: by("applied", "phone_screen", "onsite"),
      hint: "still in play",
      color: statusColor("applied"),
    },
    {
      label: "Interviewing",
      value: by("phone_screen", "onsite"),
      hint: "screens and onsites",
      color: statusColor("onsite"),
    },
    {
      label: "Offers",
      value: by("offer"),
      hint: `${responseRate}% response rate`,
      color: statusColor("offer"),
    },
  ];
}

function renderStats(apps: Application[]): void {
  statsEl.innerHTML = "";

  for (const stat of buildStats(apps)) {
    const tile = document.createElement("article");
    tile.className = "stat";
    tile.setAttribute("data-reveal", "");
    tile.style.setProperty("--stat-color", stat.color);

    const accent = document.createElement("span");
    accent.className = "stat-accent";

    const label = document.createElement("p");
    label.className = "stat-label";
    label.textContent = stat.label;

    const value = document.createElement("p");
    value.className = "stat-value tabular";
    value.textContent = "0";

    const hint = document.createElement("p");
    hint.className = "stat-hint";
    hint.textContent = stat.hint;

    tile.append(accent, label, value, hint);
    statsEl.appendChild(tile);

    // The count-up starts once the tile's own reveal delay has elapsed, so the
    // number begins ticking as the tile arrives rather than before it.
    const delay = prefersReducedMotion() ? 0 : 120 + [...statsEl.children].indexOf(tile) * 60;
    window.setTimeout(() => countUp(value, stat.value, 900), delay);
  }

  stagger(statsEl.querySelectorAll<HTMLElement>(".stat"), 60, 120);
  revealAllNow(statsEl.querySelectorAll<HTMLElement>(".stat"));
}

/* -------------------------------------------------------------------------
   Pipeline bar
   ------------------------------------------------------------------------- */

function renderPipeline(apps: Application[]): void {
  pipelineEl.innerHTML = "";
  if (apps.length === 0) return;

  const present = APPLICATION_STATUSES.map((key) => ({
    key,
    label: STATUS_LABELS[key],
    count: apps.filter((a) => a.status === key).length,
  })).filter((c) => c.count > 0);

  const bar = document.createElement("div");
  bar.className = "pipeline-bar";

  const legend = document.createElement("div");
  legend.className = "pipeline-legend";

  present.forEach((column, i) => {
    const color = statusColor(column.key);
    const delay = `${140 + i * 70}ms`;

    const seg = document.createElement("div");
    seg.className = "pipeline-seg";
    seg.style.setProperty("--w", String(column.count));

    const fill = document.createElement("div");
    fill.className = "pipeline-fill";
    fill.style.setProperty("--seg-color", color);
    fill.style.setProperty("--reveal-delay", delay);
    seg.appendChild(fill);
    bar.appendChild(seg);

    const item = document.createElement("span");
    item.style.setProperty("--seg-color", color);
    item.innerHTML = `<span class="legend-dot"></span>`;
    item.append(`${column.label} · ${column.count}`);
    legend.appendChild(item);
  });

  pipelineEl.append(bar, legend);
  revealNow(pipelineEl);
}

/* -------------------------------------------------------------------------
   Board
   ------------------------------------------------------------------------- */

function renderSkeleton(): void {
  board.innerHTML = "";
  const shape = [2, 2, 1, 1, 1, 1];

  APPLICATION_STATUSES.forEach((status, i) => {
    const section = document.createElement("section");
    section.className = "column";
    section.style.setProperty("--status-color", statusColor(status));

    const header = document.createElement("div");
    header.className = "column-header";
    header.innerHTML = `<span class="status-dot"></span><span>${STATUS_LABELS[status]}</span>`;

    const list = document.createElement("div");
    list.className = "column-list";
    for (let n = 0; n < shape[i]; n += 1) {
      const card = document.createElement("div");
      card.className = "skeleton";
      card.innerHTML =
        '<div class="skeleton-line w-70"></div>' +
        '<div class="skeleton-line w-90"></div>' +
        '<div class="skeleton-line w-45"></div>';
      list.appendChild(card);
    }

    section.append(header, list);
    board.appendChild(section);
  });
}

function renderCard(app: Application): HTMLElement {
  // A button, not a div: the card opens an editor, so it needs to be reachable
  // and activatable from the keyboard without re-implementing that by hand.
  const card = document.createElement("button");
  card.type = "button";
  card.className = "card";
  card.setAttribute("data-reveal", "");
  card.setAttribute("aria-label", `Edit ${app.company_name} — ${app.position_title}`);

  const company = document.createElement("h3");
  company.className = "card-company";
  company.textContent = app.company_name;

  const title = document.createElement("p");
  title.className = "card-title";
  title.textContent = app.position_title;

  const meta = document.createElement("div");
  meta.className = "card-meta";

  if (app.location.length > 0) {
    const loc = document.createElement("span");
    loc.className = "pill";
    loc.textContent = app.location[0];
    meta.appendChild(loc);
  }
  for (const tag of app.tags.slice(0, 2)) {
    const tagEl = document.createElement("span");
    tagEl.className = "pill pill-tag";
    tagEl.textContent = tag;
    meta.appendChild(tagEl);
  }

  const footer = document.createElement("div");
  footer.className = "card-footer";

  const applied = document.createElement("span");
  applied.textContent = `Applied ${formatDate(app.applied_at)}`;
  footer.appendChild(applied);

  // The one thing on a card you might need to act on, so it gets the only
  // accent-coloured text on the board.
  if (app.next_follow_up_at) {
    const flag = document.createElement("span");
    flag.className = "card-flag";
    flag.textContent = `Follow up ${formatDate(app.next_follow_up_at)}`;
    footer.appendChild(flag);
  }

  card.append(company, title, meta, footer);
  card.addEventListener("click", () => openEditor(app));
  return card;
}

function renderBoard(applications: Application[]): void {
  board.innerHTML = "";

  APPLICATION_STATUSES.forEach((status, columnIndex) => {
    const items = applications.filter((app) => app.status === status);

    const section = document.createElement("section");
    section.className = "column";
    section.setAttribute("data-reveal", "");
    section.style.setProperty("--status-color", statusColor(status));
    section.style.setProperty("--reveal-delay", `${columnIndex * 70}ms`);

    const header = document.createElement("div");
    header.className = "column-header";

    const dot = document.createElement("span");
    dot.className = "status-dot";

    const headerLabel = document.createElement("span");
    headerLabel.textContent = STATUS_LABELS[status];

    const headerCount = document.createElement("span");
    headerCount.className = "column-count tabular";
    headerCount.textContent = String(items.length);

    header.append(dot, headerLabel, headerCount);

    const list = document.createElement("div");
    list.className = "column-list";

    if (items.length === 0) {
      const empty = document.createElement("p");
      empty.className = "column-empty";
      empty.textContent = "Nothing here yet";
      list.appendChild(empty);
    } else {
      items.forEach((app, cardIndex) => {
        const card = renderCard(app);
        // Columns sweep across at 70ms, cards drop within a column at 55ms, so
        // the board reads as a wave rather than a grid switching on.
        card.style.setProperty("--reveal-delay", `${columnIndex * 70 + 140 + cardIndex * 55}ms`);
        list.appendChild(card);
      });
    }

    section.append(header, list);
    board.appendChild(section);
  });

  revealAllNow(board.querySelectorAll<HTMLElement>("[data-reveal]"));
}

/* -------------------------------------------------------------------------
   Editor dialog
   ------------------------------------------------------------------------- */

const dialog = document.querySelector<HTMLDialogElement>("#editor")!;
const form = document.querySelector<HTMLFormElement>("#editor-form")!;
const dialogTitle = document.querySelector<HTMLElement>("#editor-title")!;
const editorError = document.querySelector<HTMLElement>("#editor-error")!;
const saveBtn = document.querySelector<HTMLButtonElement>("#editor-save")!;
const deleteBtn = document.querySelector<HTMLButtonElement>("#editor-delete")!;
const statusSelect = document.querySelector<HTMLSelectElement>("#f-status")!;

for (const status of APPLICATION_STATUSES) {
  const option = document.createElement("option");
  option.value = status;
  option.textContent = STATUS_LABELS[status];
  statusSelect.appendChild(option);
}

/** null = creating a new application; otherwise the one being edited. */
let editing: Application | null = null;

function field<T extends HTMLElement>(id: string): T {
  return document.querySelector<T>(id)!;
}

function openEditor(app: Application | null): void {
  editing = app;
  editorError.hidden = true;
  form.reset();

  dialogTitle.textContent = app ? "Edit application" : "New application";
  saveBtn.textContent = app ? "Save changes" : "Add application";
  deleteBtn.hidden = app === null;

  field<HTMLInputElement>("#f-company").value = app?.company_name ?? "";
  field<HTMLInputElement>("#f-title").value = app?.position_title ?? "";
  statusSelect.value = app?.status ?? "applied";
  field<HTMLInputElement>("#f-applied").value = toDateInput(app?.applied_at ?? new Date().toISOString());
  field<HTMLInputElement>("#f-location").value = (app?.location ?? []).join(", ");
  field<HTMLInputElement>("#f-tags").value = (app?.tags ?? []).join(", ");
  field<HTMLInputElement>("#f-link").value = app?.job_link ?? "";
  field<HTMLInputElement>("#f-followup").value = toDateInput(app?.next_follow_up_at ?? null);
  field<HTMLTextAreaElement>("#f-notes").value = app?.notes ?? "";

  dialog.showModal();
  field<HTMLInputElement>("#f-company").focus();
}

function readForm(): ApplicationInput {
  const appliedRaw = field<HTMLInputElement>("#f-applied").value;
  const followUpRaw = field<HTMLInputElement>("#f-followup").value;

  return {
    company_name: field<HTMLInputElement>("#f-company").value.trim(),
    position_title: field<HTMLInputElement>("#f-title").value.trim(),
    status: statusSelect.value as ApplicationStatus,
    location: parseList(field<HTMLInputElement>("#f-location").value),
    tags: parseList(field<HTMLInputElement>("#f-tags").value),
    job_link: field<HTMLInputElement>("#f-link").value.trim(),
    job_description: editing?.job_description ?? "",
    notes: field<HTMLTextAreaElement>("#f-notes").value.trim(),
    compensation: editing?.compensation ?? null,
    applied_at: appliedRaw ? new Date(appliedRaw).toISOString() : new Date().toISOString(),
    // An empty date input clears the follow-up. Sending null (rather than
    // omitting the key) is what tells the server to unset it — see
    // UpdateApplicationRequest.
    next_follow_up_at: followUpRaw ? new Date(followUpRaw).toISOString() : null,
  };
}

function showEditorError(message: string): void {
  editorError.textContent = message;
  editorError.hidden = false;
}

form.addEventListener("submit", async (e) => {
  e.preventDefault();
  editorError.hidden = true;

  const input = readForm();
  if (!input.company_name || !input.position_title) {
    showEditorError("Company and role are both required.");
    return;
  }

  saveBtn.disabled = true;
  const original = saveBtn.textContent;
  saveBtn.textContent = "Saving…";

  try {
    if (editing) {
      const updated = await updateApplication(editing.id, input);
      all = all.map((a) => (a.id === updated.id ? updated : a));
    } else {
      all = [await createApplication(input), ...all];
    }
    dialog.close();
    refresh();
  } catch (err) {
    showEditorError(err instanceof Error ? err.message : "Could not save.");
  } finally {
    saveBtn.disabled = false;
    saveBtn.textContent = original;
  }
});

deleteBtn.addEventListener("click", async () => {
  if (!editing) return;
  if (!window.confirm(`Delete the ${editing.company_name} application? This can't be undone.`)) {
    return;
  }

  deleteBtn.disabled = true;
  try {
    await deleteApplication(editing.id);
    all = all.filter((a) => a.id !== editing!.id);
    dialog.close();
    refresh();
  } catch (err) {
    showEditorError(err instanceof Error ? err.message : "Could not delete.");
  } finally {
    deleteBtn.disabled = false;
  }
});

for (const id of ["#editor-close", "#editor-cancel"]) {
  document.querySelector<HTMLButtonElement>(id)!.addEventListener("click", () => dialog.close());
}

// Clicking the backdrop (outside the form) closes, matching what the visual
// dimming implies. The check works because the dialog's own box is the
// backdrop's hit area, and the form fills the dialog.
dialog.addEventListener("click", (e) => {
  if (e.target === dialog) dialog.close();
});

/* -------------------------------------------------------------------------
   Wiring
   ------------------------------------------------------------------------- */

document.querySelector<HTMLButtonElement>("#new-application-btn")!.addEventListener("click", () => {
  openEditor(null);
});

/* -------------------------------------------------------------------------
   Change-password dialog
   ------------------------------------------------------------------------- */

const passwordDialog = document.querySelector<HTMLDialogElement>("#password-dialog")!;
const passwordForm = document.querySelector<HTMLFormElement>("#password-form")!;
const passwordError = document.querySelector<HTMLElement>("#password-error")!;
const passwordSave = document.querySelector<HTMLButtonElement>("#password-save")!;

function showPasswordError(message: string): void {
  passwordError.textContent = message;
  passwordError.hidden = false;
}

document.querySelector<HTMLButtonElement>("#change-password-btn")!.addEventListener("click", () => {
  passwordForm.reset();
  passwordError.hidden = true;
  passwordDialog.showModal();
  field<HTMLInputElement>("#p-current").focus();
});

for (const id of ["#password-close", "#password-cancel"]) {
  document.querySelector<HTMLButtonElement>(id)!.addEventListener("click", () => passwordDialog.close());
}

passwordDialog.addEventListener("click", (e) => {
  if (e.target === passwordDialog) passwordDialog.close();
});

passwordForm.addEventListener("submit", async (e) => {
  e.preventDefault();
  passwordError.hidden = true;

  const current = field<HTMLInputElement>("#p-current").value;
  const next = field<HTMLInputElement>("#p-new").value;
  const confirm = field<HTMLInputElement>("#p-confirm").value;

  // The confirmation field is the one rule the server can't check — it only
  // ever receives one new password. Everything else (length, uppercase,
  // special character) is left to the server so there is a single authority,
  // and its message is shown verbatim.
  if (next !== confirm) {
    showPasswordError("The new passwords don't match.");
    return;
  }
  if (!current || !next) {
    showPasswordError("Both fields are required.");
    return;
  }

  passwordSave.disabled = true;
  const original = passwordSave.textContent;
  passwordSave.textContent = "Changing…";

  try {
    await changePassword(current, next);
  } catch (err) {
    showPasswordError(err instanceof Error ? err.message : "Could not change the password.");
    passwordSave.disabled = false;
    passwordSave.textContent = original;
    return;
  }

  // The server destroys the session on success, so every subsequent request
  // would 401. Leave for the login page rather than rendering a dashboard that
  // is already logged out. No logout() call: the session is gone already.
  passwordDialog.close();
  window.location.href = "/login?password-changed=1";
});

document.querySelector<HTMLButtonElement>("#logout-btn")!.addEventListener("click", async () => {
  await logout();
  // The landing page, not the login form. Signing out is a deliberate "I'm
  // done" — answering it with a login form assumes the opposite. The two
  // redirects that *do* go to /login are the ones where the user hasn't asked
  // to leave and has to get back in: a password change and a new account.
  window.location.href = "/";
});

let searchDebounce = 0;
searchInput.addEventListener("input", () => {
  window.clearTimeout(searchDebounce);
  // Filtering is local, so this only smooths rendering — 120ms is under the
  // threshold where typing feels laggy but well above per-keystroke rerenders.
  searchDebounce = window.setTimeout(() => {
    search = searchInput.value;
    renderFiltered();
  }, 120);
});

clearFilters.addEventListener("click", () => {
  search = "";
  statusOf = "";
  searchInput.value = "";
  renderFilterChips();
  renderFiltered();
});

/** Re-render everything that depends on `all`. */
function refresh(): void {
  renderStats(all);
  renderPipeline(all);
  renderFiltered();
}

/* -------------------------------------------------------------------------
   Boot
   ------------------------------------------------------------------------- */

function wait(ms: number): Promise<void> {
  return new Promise((resolve) => setTimeout(resolve, ms));
}

function showUser(user: User): void {
  document.querySelector<HTMLElement>("#user-avatar")!.textContent = initials(user.name);
  document.querySelector<HTMLElement>("#user-name")!.textContent = user.name;
}

async function init(): Promise<void> {
  renderSkeleton();
  requestAnimationFrame(() => revealNow(document.querySelector<HTMLElement>(".topbar")!));

  // The session cookie is HttpOnly, so GET /me is the only real way to ask
  // "am I signed in?" — hence the guard is here rather than at module load.
  const user = await getMe();
  if (!user) {
    // An expired or missing session, not a deliberate exit — the user was
    // trying to reach the dashboard, so send them where they can get back to
    // it rather than to the marketing page.
    window.location.href = "/login?session-expired=1";
    return;
  }
  showUser(user);

  try {
    all = await listAllApplications();
  } catch (err) {
    boardError.textContent = err instanceof Error ? err.message : "Could not load applications.";
    boardError.hidden = false;
    board.innerHTML = "";
    return;
  }

  if (!prefersReducedMotion()) {
    // Shrink the skeleton away before replacing it, so the swap reads as one
    // continuous event rather than a hard cut between two layouts.
    board.classList.add("fade-out");
    await wait(200);
    board.classList.remove("fade-out");
  }

  renderFilterChips();
  refresh();
}

init();
