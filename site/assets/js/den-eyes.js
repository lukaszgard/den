// The home page's gopher (layouts/_shortcodes/den/hero.html) looks at the pointer and
// blinks now and then. Without this script its pupils stay where the drawing has them.
(() => {
  const svg = document.querySelector(".den-hero-eyes");
  if (!svg) return;

  const IDLE_MS = 4000; // the pupils drift back to the drawing's after this long still
  const BLINK_MS = 300; // the den-blink animation's length in custom.css
  const EASE = 0.2; // share of the way to the target covered each frame
  const reduced = matchMedia("(prefers-reduced-motion: reduce)");

  const eyes = [...svg.querySelectorAll(".den-pupil")].map((g) => {
    const [cx, cy, ax, ay] = g.dataset.eye.split(" ").map(Number);
    const rest = g.transform.baseVal.consolidate().matrix;
    return { g, cx, cy, ax, ay, restX: rest.e, restY: rest.f, x: rest.e, y: rest.f };
  });

  let pointer = null;
  let frame = 0;
  let idle = 0;

  function target(eye, box) {
    if (!pointer) return [eye.restX, eye.restY];
    const scale = box.width / 1440;
    const dx = pointer.x - (box.left + eye.cx * scale);
    const dy = pointer.y - (box.top + eye.cy * scale);
    const d = Math.hypot(dx, dy);
    if (d < 1) return [eye.cx, eye.cy];
    const ux = dx / d;
    const uy = dy / d;
    // As far as the pupil can go that way without leaving the white (an ellipse),
    // reached once the pointer is half the picture's width away.
    const edge = 1 / Math.hypot(ux / eye.ax, uy / eye.ay);
    const travel = edge * Math.min(1, d / (box.width / 2));
    return [eye.cx + ux * travel, eye.cy + uy * travel];
  }

  function step() {
    frame = 0;
    const box = svg.getBoundingClientRect();
    let moving = false;
    for (const eye of eyes) {
      const [tx, ty] = target(eye, box);
      eye.x += (tx - eye.x) * EASE;
      eye.y += (ty - eye.y) * EASE;
      if (Math.abs(tx - eye.x) < 0.05 && Math.abs(ty - eye.y) < 0.05) {
        eye.x = tx;
        eye.y = ty;
      } else {
        moving = true;
      }
      eye.g.setAttribute("transform", `translate(${eye.x.toFixed(2)} ${eye.y.toFixed(2)})`);
    }
    // A settled gaze costs nothing: the next pointer move or scroll starts the loop again.
    if (moving) schedule();
  }

  function schedule() {
    if (!frame) frame = requestAnimationFrame(step);
  }

  function rest() {
    pointer = null;
    schedule();
  }

  function look(e) {
    pointer = { x: e.clientX, y: e.clientY };
    clearTimeout(idle);
    idle = setTimeout(rest, IDLE_MS);
    schedule();
  }

  // pointerdown as well, because a tap sends no pointermove: the gopher looks at it.
  addEventListener("pointermove", look, { passive: true });
  addEventListener("pointerdown", look, { passive: true });
  // A finger leaves the page every time it lifts; only a mouse or pen leaving means
  // nobody is there to look at.
  document.documentElement.addEventListener("pointerleave", (e) => {
    if (e.pointerType !== "touch") rest();
  });
  // Scrolling moves the eyes under a pointer that stays put.
  addEventListener("scroll", () => pointer && schedule(), { passive: true });

  function blinkLater() {
    setTimeout(() => {
      if (!reduced.matches && !document.hidden) {
        svg.classList.add("blinking");
        setTimeout(() => svg.classList.remove("blinking"), BLINK_MS);
      }
      blinkLater();
    }, 3000 + Math.random() * 4000);
  }
  blinkLater();
})();
