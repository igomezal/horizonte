// Small global behaviours that don't need Alpine.
(function () {
  "use strict";

  function effectiveTheme() {
    const set = document.documentElement.dataset.theme;
    if (set) return set;
    return window.matchMedia("(prefers-color-scheme: dark)").matches ? "dark" : "light";
  }

  // Forms with validation errors come back as 422; swap them in so the
  // errors show next to the fields (htmx ignores 4xx responses by default).
  if (window.htmx) {
    htmx.config.responseHandling.unshift({ code: "422", swap: true });
  }

  // Focus the first field of a form that htmx just inserted, or the first
  // invalid field when a form comes back with errors.
  // htmx:load fires for each newly inserted element, so whole-section swaps
  // (which are not forms) never steal focus.
  document.addEventListener("htmx:load", (e) => {
    const form = e.detail.elt;
    if (!form || !form.matches || !form.matches("form.entry-form")) return;
    const field = form.querySelector('[aria-invalid="true"]') || form.querySelector("input, select");
    if (field) field.focus();
  });

  // Year picker: previous/next buttons move the slider and let htmx load the year.
  document.addEventListener("click", (e) => {
    const btn = e.target.closest("[data-year-step]");
    if (!btn) return;
    const slider = document.querySelector("[data-year-slider]");
    if (!slider) return;
    const next = Number(slider.value) + Number(btn.dataset.yearStep);
    if (next < Number(slider.min) || next > Number(slider.max)) return;
    slider.value = String(next);
    slider.dispatchEvent(new Event("input", { bubbles: true }));
  });

  // Theme toggle (event delegation, so it keeps working after htmx body swaps).
  document.addEventListener("click", (e) => {
    const btn = e.target.closest("[data-theme-toggle]");
    if (!btn) return;
    const next = effectiveTheme() === "dark" ? "light" : "dark";
    document.documentElement.dataset.theme = next;
    try { localStorage.setItem("theme", next); } catch (err) { /* storage unavailable */ }
    document.dispatchEvent(new CustomEvent("themechange", { detail: next }));
  });
})();
