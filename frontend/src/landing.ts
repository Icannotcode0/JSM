import { getMe } from "./api";
import {
  EASE_OUT,
  flipMove,
  loopWhileVisible,
  observeReveals,
  prefersReducedMotion,
  revealAllNow,
  revealNow,
} from "./motion";

/* -------------------------------------------------------------------------
   Signed-in state
   ------------------------------------------------------------------------- */

// If already logged in, point every CTA at the dashboard instead of login —
// matches how most real product marketing pages behave for signed-in users.
//
// Deliberately not awaited: the session cookie is HttpOnly, so answering this
// needs a round-trip to /me, and blocking the hero entrance on a network call
// would be a visible stall for the common case (a logged-out visitor). The
// buttons work as "Sign in" until the answer arrives, then relabel.
void getMe().then((user) => {
  if (!user) return;

  // An authenticated visitor has no use for "create an account".
  const signupCta = document.querySelector<HTMLElement>("#hero-signup");
  if (signupCta) signupCta.hidden = true;

  const ctas: [string, string][] = [
    ["#nav-cta", "Dashboard"],
    ["#hero-cta", "Go to dashboard"],
    ["#closer-cta", "Go to dashboard"],
  ];
  for (const [selector, label] of ctas) {
    const el = document.querySelector<HTMLAnchorElement>(selector);
    if (!el) continue;
    el.href = "/dashboard";
    // Keep the arrow, replace only the label text node.
    const arrow = el.querySelector(".btn-arrow");
    el.textContent = label;
    if (arrow) el.append(arrow);
  }
});

/* -------------------------------------------------------------------------
   Hero entrance
   -------------------------------------------------------------------------
   Hand-choreographed rather than left to the scroll observer, because the hero
   is above the fold: it should perform on load, in a deliberate order, instead
   of all arriving in the same frame. Delays climb in rough 80-100ms steps —
   close enough to read as one gesture, far enough apart to read as sequence.
   ------------------------------------------------------------------------- */

const heroTitle = document.querySelector<HTMLElement>("#hero-title");
const heroVisual = document.querySelector<HTMLElement>(".hero-visual");

// Delays are assigned synchronously, at module-evaluation time. observeReveals()
// at the bottom of this file also watches these elements, and its first callback
// can land before a requestAnimationFrame does — if the delays weren't already
// in place the whole hero would arrive in one frame.
const heroStaged: HTMLElement[] = [];

for (const [selector, delay] of [
  [".hero-eyebrow", 0],
  [".hero-subtitle", 320],
  [".hero-actions", 400],
  // The board arrives just behind the headline — the copy establishes what the
  // product is, the visual confirms it.
  [".hero-visual", 240],
] as [string, number][]) {
  const el = document.querySelector<HTMLElement>(selector);
  if (!el) continue;
  el.style.setProperty("--reveal-delay", `${delay}ms`);
  heroStaged.push(el);
}

if (heroTitle) {
  // Each line rises out of its own mask, one after the other.
  const lines = heroTitle.querySelectorAll<HTMLElement>(".line-mask > span");
  lines.forEach((line, i) => line.style.setProperty("--reveal-delay", `${80 + i * 110}ms`));
}

// Wait one frame so the browser has painted the "before" state; otherwise the
// class lands in the same frame the element first renders and no transition runs.
requestAnimationFrame(() => {
  if (heroTitle) revealNow(heroTitle);
  revealAllNow(heroStaged);
});

/* -------------------------------------------------------------------------
   Nav: transparent over the hero, surfaced once it's covering content
   ------------------------------------------------------------------------- */

const nav = document.querySelector<HTMLElement>("#site-nav");
if (nav) {
  let ticking = false;
  const sync = () => {
    nav.classList.toggle("is-stuck", window.scrollY > 16);
    ticking = false;
  };
  window.addEventListener(
    "scroll",
    () => {
      if (ticking) return;
      ticking = true;
      requestAnimationFrame(sync);
    },
    { passive: true },
  );
  sync();
}

/* -------------------------------------------------------------------------
   Pointer tilt on the product preview
   -------------------------------------------------------------------------
   Low amplitude on purpose (7deg of yaw, 5deg of pitch). Enough parallax that
   the window reads as an object sitting in space rather than a flat image;
   not so much that it becomes the thing you're looking at.
   ------------------------------------------------------------------------- */

const tilt = document.querySelector<HTMLElement>("#preview-tilt");
const finePointer = window.matchMedia("(hover: hover) and (pointer: fine)").matches;

if (tilt && heroVisual && finePointer && !prefersReducedMotion()) {
  const MAX_YAW = 7;
  const MAX_PITCH = 5;
  let frame = 0;

  heroVisual.addEventListener("pointermove", (e) => {
    if (frame) return;
    frame = requestAnimationFrame(() => {
      frame = 0;
      const box = heroVisual.getBoundingClientRect();
      // -0.5 .. 0.5 from the centre of the visual.
      const px = (e.clientX - box.left) / box.width - 0.5;
      const py = (e.clientY - box.top) / box.height - 0.5;
      tilt.style.setProperty("--tilt-y", `${px * MAX_YAW * 2}deg`);
      tilt.style.setProperty("--tilt-x", `${-py * MAX_PITCH * 2}deg`);
      // Track the pointer tightly while it's moving...
      tilt.style.transitionDuration = "120ms";
    });
  });

  heroVisual.addEventListener("pointerleave", () => {
    // ...but return to rest slowly, so the release reads as the object
    // settling rather than snapping back.
    tilt.style.transitionDuration = "";
    tilt.style.setProperty("--tilt-y", "0deg");
    tilt.style.setProperty("--tilt-x", "0deg");
  });
}

/* -------------------------------------------------------------------------
   Live board demo
   -------------------------------------------------------------------------
   The hero claims JSM tracks a pipeline; this shows one moving. Cards are
   promoted between columns with FLIP, so the travel is a pure transform even
   though the underlying change is a DOM move that re-lays-out both columns.

   The loop is three scripted steps and then a soft reset — a card flying
   *backwards* to an earlier stage would read as a bug, so the board fades
   through the reset instead of animating it.
   ------------------------------------------------------------------------- */

const previewBoard = document.querySelector<HTMLElement>("#preview-board");

function initBoardDemo(board: HTMLElement): void {
  const lists = [...board.querySelectorAll<HTMLElement>("[data-list]")];
  const counts = [...board.querySelectorAll<HTMLElement>("[data-count]")];
  if (lists.length < 3) return;

  const acme = lists[0].children[0] as HTMLElement | undefined;
  const initech = lists[1].children[0] as HTMLElement | undefined;
  if (!acme || !initech) return;

  function syncCounts(): void {
    lists.forEach((list, i) => {
      const badge = counts[i];
      if (!badge) return;
      const next = String(list.children.length);
      if (badge.textContent === next) return;
      badge.textContent = next;
      // Re-trigger the pop keyframe: remove, flush, re-add.
      badge.classList.remove("is-bumped");
      void badge.offsetWidth;
      badge.classList.add("is-bumped");
    });
  }

  async function softReset(): Promise<void> {
    const out = board.animate([{ opacity: 1 }, { opacity: 0.12 }], {
      duration: 240,
      easing: "cubic-bezier(0.65, 0, 0.35, 1)",
      fill: "forwards",
    });
    await out.finished;

    lists[0].prepend(acme!);
    lists[1].prepend(initech!);
    syncCounts();

    // Start the fade back in *before* cancelling `out`, so there's no frame
    // where the board snaps to full opacity between the two animations.
    board.animate(
      [
        { opacity: 0.12, transform: "translate3d(0, 8px, 0)" },
        { opacity: 1, transform: "none" },
      ],
      { duration: 460, easing: EASE_OUT },
    );
    out.cancel();
  }

  const steps: (() => void | Promise<void>)[] = [
    () => flipMove(initech, lists[2]), // Initech: screen -> offer
    () => flipMove(acme, lists[1]), //    Acme: applied -> screen
    () => softReset(),
  ];

  let step = 0;
  let busy = false;

  loopWhileVisible(board, 2600, async () => {
    if (busy) return;
    busy = true;
    await steps[step % steps.length]();
    step += 1;
    syncCounts();
    busy = false;
  });
}

if (previewBoard && !prefersReducedMotion()) {
  initBoardDemo(previewBoard);
}

/* -------------------------------------------------------------------------
   Everything below the fold reveals on scroll
   ------------------------------------------------------------------------- */

observeReveals();
