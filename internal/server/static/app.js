// Fall back to the app's initial when an icon fails to load. Inline onerror
// handlers are blocked by the CSP, so this listens in the capture phase.
document.addEventListener("error", (e) => {
  const img = e.target;
  if (img.tagName !== "IMG" || !img.parentElement?.classList.contains("icon")) return;
  const icon = img.parentElement;
  img.remove();
  icon.textContent = icon.dataset.initial || "?";
}, true);

// Grow every tile so the longest single word in any name fits on one line,
// rather than being split mid-word. Tiles stay square and the same size.
const fitTiles = () => {
  const root = document.documentElement;
  const labels = [...document.querySelectorAll(".tile .label")];
  if (!labels.length) return;
  root.style.removeProperty("--tile");
  const tile = labels[0].closest(".tile");
  const cs = getComputedStyle(tile);
  const ctx = document.createElement("canvas").getContext("2d");
  ctx.font = getComputedStyle(labels[0]).font;
  let widest = 0;
  for (const l of labels) {
    for (const word of l.textContent.split(/\s+/)) {
      widest = Math.max(widest, ctx.measureText(word).width);
    }
  }
  const needed = Math.ceil(widest + parseFloat(cs.paddingLeft) + parseFloat(cs.paddingRight) + 4);
  if (needed > tile.offsetWidth) root.style.setProperty("--tile", needed + "px");
};

document.addEventListener("DOMContentLoaded", () => {
  setupThemeToggle();
  fitTiles();
  window.matchMedia("(max-width: 600px)").addEventListener("change", fitTiles);
  document.fonts?.ready.then(fitTiles);

  setupFavorites();

  const search = document.getElementById("search");
  if (!search) return;
  const nomatch = document.getElementById("nomatch");

  const filter = () => {
    const q = search.value.trim().toLowerCase();
    let shown = 0;
    for (const t of document.querySelectorAll(".tile")) {
      const hit = !q || t.dataset.name.toLowerCase().includes(q) || t.title.toLowerCase().includes(q);
      t.parentElement.classList.toggle("hidden", !hit);
      if (hit) shown++;
    }
    for (const s of document.querySelectorAll("main section")) {
      s.classList.toggle("hidden", !s.querySelector("li:not(.hidden)"));
    }
    shown = document.querySelectorAll("main section:not(#favorites) li:not(.hidden)").length;
    nomatch.classList.toggle("hidden", shown > 0);
  };

  search.addEventListener("input", filter);
  search.addEventListener("keydown", (e) => {
    if (e.key === "Enter") {
      const first = [...document.querySelectorAll(".tile")].find((t) => !t.parentElement.classList.contains("hidden"));
      if (first) first.click();
    } else if (e.key === "Escape") {
      search.value = "";
      filter();
      search.blur();
    }
  });
  document.addEventListener("keydown", (e) => {
    if (e.key === "/" && document.activeElement !== search) {
      e.preventDefault();
      search.focus();
    }
  });
});

// Favourites: the star on each tile saves to the server, then the page is
// updated in place. The same app can appear twice (in Favourites and in its
// own section), so every star for that app is kept in sync.
function setupFavorites() {
  const section = document.getElementById("favorites");
  if (!section) return;
  const list = section.querySelector(".grid");

  document.querySelector("main").addEventListener("click", async (e) => {
    const star = e.target.closest(".star");
    if (!star) return;
    const li = star.closest("li");
    const id = li.dataset.app;
    const on = star.getAttribute("aria-pressed") !== "true";
    star.disabled = true;
    try {
      const res = await fetch("/api/favorites/" + encodeURIComponent(id), { method: on ? "PUT" : "DELETE" });
      if (!res.ok) throw new Error(res.status);
    } catch {
      star.classList.add("failed");
      setTimeout(() => star.classList.remove("failed"), 600);
      return;
    } finally {
      star.disabled = false;
    }
    for (const s of document.querySelectorAll(`li[data-app="${CSS.escape(id)}"] .star`)) {
      s.setAttribute("aria-pressed", String(on));
    }
    if (on) {
      const copy = li.cloneNode(true);
      copy.classList.remove("hidden");
      list.append(copy);
    } else {
      list.querySelector(`li[data-app="${CSS.escape(id)}"]`)?.remove();
    }
    section.classList.toggle("hidden", !list.children.length);
  });
}

// Theme button: cycles auto → light → dark. The page switches immediately;
// the server stores the choice (cookie, plus the database when there is one)
// and renders it on the next load.
function setupThemeToggle() {
  const button = document.querySelector(".theme-toggle");
  if (!button) return;
  const order = ["auto", "light", "dark"];
  const label = { auto: "Auto (follows your system)", light: "Light", dark: "Dark" };
  const apply = (mode) => {
    const root = document.documentElement;
    if (mode === "auto") root.removeAttribute("data-theme");
    else root.setAttribute("data-theme", mode);
    document.querySelector('meta[name="color-scheme"]').content = mode === "auto" ? "light dark" : mode;
    button.dataset.mode = mode;
    button.title = button.ariaLabel = "Theme: " + label[mode];
  };
  apply(button.dataset.mode);
  button.addEventListener("click", () => {
    const next = order[(order.indexOf(button.dataset.mode) + 1) % order.length];
    apply(next);
    fetch("/api/theme/" + next, { method: "PUT" }).catch(() => {});
  });
}
