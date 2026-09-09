// Shared motion primitives.
//
// Two rules everything here obeys:
//
//  1. Only `transform` and `opacity` are animated. Both are handled by the
//     compositor, so animating them never triggers layout or paint. Animating
//     `width`, `top`, `height` etc. does, which is what makes hand-rolled web
//     animation janky. Where a layout change genuinely has to be animated
//     (a card moving between columns) we use FLIP — see flipMove below.
//  2. Every helper degrades to "jump straight to the end state" when the user
//     has asked for reduced motion. That's checked live rather than cached,
//     so toggling the OS setting takes effect without a reload.

/** Easing curves, mirrored from the --ease-* custom properties in style.css. */
export const EASE_OUT = "cubic-bezier(0.22, 1, 0.36, 1)"; // quint-out: entering
export const EASE_SPRING = "cubic-bezier(0.34, 1.56, 0.64, 1)"; // slight overshoot

/** Delay between siblings in a staggered group, in ms. */
export const STAGGER_MS = 60;

export function prefersReducedMotion(): boolean {
  return window.matchMedia("(prefers-reduced-motion: reduce)").matches;
}

/**
 * Assign staggered reveal delays to a group of elements. Later siblings wait
 * longer, which is Disney's "follow-through / overlapping action": a group
 * that arrives together reads as a slab, a group that arrives in sequence
 * reads as related-but-distinct items.
 */
export function stagger(elements: Iterable<HTMLElement>, stepMs = STAGGER_MS, baseMs = 0): void {
  let i = 0;
  for (const el of elements) {
    el.style.setProperty("--reveal-delay", `${baseMs + i * stepMs}ms`);
    i += 1;
  }
}

/**
 * Reveals are one-shot, so the `will-change` hint that buys them a compositor
 * layer is only useful until they land. Holding it on every revealed node for
 * the life of the page is exactly the misuse the property warns about, so we
 * drop it once the transition ends.
 */
function settle(el: HTMLElement): void {
  el.addEventListener(
    "transitionend",
    () => el.classList.add("is-settled"),
    { once: true },
  );
}

/** Reveal an element (and honour its --reveal-delay) right now, no scrolling required. */
export function revealNow(el: HTMLElement): void {
  // Force a style flush first, otherwise adding the class in the same frame the
  // element was inserted means the browser never sees the "before" state and
  // the transition is skipped entirely.
  void el.offsetHeight;
  settle(el);
  el.classList.add("is-revealed");
}

export function revealAllNow(elements: Iterable<HTMLElement>): void {
  const list = [...elements];
  if (list.length > 0) void list[0].offsetHeight;
  for (const el of list) {
    settle(el);
    el.classList.add("is-revealed");
  }
}

/**
 * Reveal `[data-reveal]` elements as they scroll into view.
 *
 * Elements inside a `[data-reveal-group]` are auto-staggered in DOM order and
 * revealed together when the group enters, so a row of feature cards cascades
 * rather than each card firing on its own scroll position.
 */
export function observeReveals(root: ParentNode = document): void {
  const groups = [...root.querySelectorAll<HTMLElement>("[data-reveal-group]")];
  for (const group of groups) {
    stagger(group.querySelectorAll<HTMLElement>("[data-reveal]"), STAGGER_MS);
  }

  const targets = [
    ...groups,
    ...[...root.querySelectorAll<HTMLElement>("[data-reveal]")].filter(
      (el) => !el.closest("[data-reveal-group]"),
    ),
  ];

  if (prefersReducedMotion()) {
    for (const el of targets) {
      el.classList.add("is-revealed");
      for (const child of el.querySelectorAll<HTMLElement>("[data-reveal]")) {
        child.classList.add("is-revealed");
      }
    }
    return;
  }

  const observer = new IntersectionObserver(
    (entries) => {
      for (const entry of entries) {
        if (!entry.isIntersecting) continue;
        const el = entry.target as HTMLElement;
        settle(el);
        el.classList.add("is-revealed");
        for (const child of el.querySelectorAll<HTMLElement>("[data-reveal]")) {
          settle(child);
          child.classList.add("is-revealed");
        }
        observer.unobserve(el); // reveal once; re-animating on every scroll is noise
      }
    },
    // Fire slightly before the element is fully on screen, and treat anything
    // already past the bottom of the viewport on load as visible.
    { threshold: 0.15, rootMargin: "0px 0px -8% 0px" },
  );

  for (const el of targets) observer.observe(el);
}

/**
 * Count a number up to its final value. Used for the dashboard stat tiles:
 * a number that lands on 12 reads as "12", a number that counts to 12 reads
 * as "we tallied this for you", which is the point of the strip.
 */
export function countUp(el: HTMLElement, to: number, durationMs = 900, suffix = ""): void {
  if (prefersReducedMotion() || to === 0) {
    el.textContent = `${to}${suffix}`;
    return;
  }

  const start = performance.now();
  const tick = (now: number) => {
    const t = Math.min(1, (now - start) / durationMs);
    // Quint ease-out — matches EASE_OUT's shape, so the numbers decelerate on
    // the same curve as the tiles they sit in.
    const eased = 1 - Math.pow(1 - t, 5);
    el.textContent = `${Math.round(to * eased)}${suffix}`;
    if (t < 1) requestAnimationFrame(tick);
  };
  requestAnimationFrame(tick);
}

/**
 * Move `el` into `target` and animate the move with FLIP (Paul Lewis):
 *
 *   First  — measure where it is now.
 *   Last   — move it in the DOM, measure where it landed.
 *   Invert — transform it back to where it started.
 *   Play   — release the transform and let it animate to its real position.
 *
 * The element is only ever laid out twice; the travel itself is a pure
 * transform. The mid-flight scale bump is secondary action — it sells the
 * card as a physical thing being picked up and put down.
 */
export function flipMove(el: HTMLElement, target: HTMLElement, durationMs = 620): void {
  if (prefersReducedMotion()) {
    target.appendChild(el);
    return;
  }

  const first = el.getBoundingClientRect();
  target.appendChild(el);
  const last = el.getBoundingClientRect();

  const dx = first.left - last.left;
  const dy = first.top - last.top;
  if (dx === 0 && dy === 0) return;

  el.animate(
    [
      { transform: `translate3d(${dx}px, ${dy}px, 0) scale(1)`, zIndex: "5" },
      { transform: `translate3d(${dx * 0.5}px, ${dy * 0.5}px, 0) scale(1.05)`, offset: 0.5, zIndex: "5" },
      { transform: "translate3d(0, 0, 0) scale(1)", zIndex: "5" },
    ],
    { duration: durationMs, easing: EASE_OUT, fill: "backwards" },
  );
}

/**
 * Run `fn` on an interval, but only while the element is on screen and the tab
 * is in the foreground. Returns a stop function.
 *
 * An idle loop that keeps ticking in a background tab burns battery for an
 * animation nobody is looking at.
 */
export function loopWhileVisible(el: Element, intervalMs: number, fn: () => void): () => void {
  let timer: number | undefined;
  let onScreen = false;

  const shouldRun = () => onScreen && document.visibilityState === "visible";

  const sync = () => {
    if (shouldRun() && timer === undefined) {
      timer = window.setInterval(fn, intervalMs);
    } else if (!shouldRun() && timer !== undefined) {
      window.clearInterval(timer);
      timer = undefined;
    }
  };

  const observer = new IntersectionObserver(
    ([entry]) => {
      onScreen = entry.isIntersecting;
      sync();
    },
    { threshold: 0.3 },
  );
  observer.observe(el);
  document.addEventListener("visibilitychange", sync);

  return () => {
    observer.disconnect();
    document.removeEventListener("visibilitychange", sync);
    if (timer !== undefined) window.clearInterval(timer);
  };
}
