// Small global behaviours that don't need Alpine.
(function () {
  "use strict";

  function effectiveTheme() {
    const set = document.documentElement.dataset.theme;
    if (set) return set;
    return window.matchMedia("(prefers-color-scheme: dark)").matches ? "dark" : "light";
  }

  // Sentry Logs (only when the server enabled Sentry; otherwise a no-op).
  // Conventions match the Go side (cmd/server/logging.go): a short, stable
  // message plus attributes using standard keys (http.*, url.path) or the
  // horizonte.* namespace. Never log amounts, names or anything typed in.
  function log(level, message, attrs) {
    const logger = window.Sentry && window.Sentry.logger;
    if (logger) logger[level](message, attrs);
  }

  // Sentry Metrics (same conventions as internal/web/metrics.go): names are
  // horizonte.<domain>.<measure>, distributions always carry a unit, and
  // attributes reuse the log keys. No-op when Sentry is not enabled.
  function metric(type, name, value, options) {
    const metrics = window.Sentry && window.Sentry.metrics;
    if (metrics) metrics[type](name, value, options);
  }

  function pathOf(url) {
    return String(url || "").split(/[?#]/)[0];
  }

  // Attributes shared by the htmx request logs below.
  function requestAttrs(detail) {
    const attrs = {
      "http.request.method": String((detail.requestConfig && detail.requestConfig.verb) || "").toUpperCase(),
      "url.path": pathOf(detail.pathInfo && detail.pathInfo.requestPath),
      "horizonte.page": location.pathname,
    };
    if (detail.xhr && detail.xhr.status) attrs["http.response.status_code"] = detail.xhr.status;
    return attrs;
  }

  // htmx request time as the user experiences it (network + server). Boosted
  // navigation and partial updates happen after the page-load trace has
  // ended, so without this nothing measures them on the browser side.
  const started = new WeakMap();
  document.addEventListener("htmx:beforeRequest", (e) => {
    if (e.detail.xhr) started.set(e.detail.xhr, performance.now());
  });
  document.addEventListener("htmx:afterRequest", (e) => {
    const xhr = e.detail.xhr;
    const t0 = xhr && started.get(xhr);
    if (t0 === undefined || !xhr.status) return; // not sent: counted below
    const attrs = requestAttrs(e.detail);
    attrs["horizonte.htmx.boosted"] = !!e.detail.boosted;
    metric("distribution", "horizonte.htmx.request.duration", performance.now() - t0, {
      unit: "millisecond",
      attributes: attrs,
    });
  });

  // Failed htmx requests by reason, to chart or alert on without searching logs.
  function countFailure(reason, detail) {
    const attrs = requestAttrs(detail);
    attrs["horizonte.htmx.failure"] = reason;
    metric("count", "horizonte.htmx.request.failed", 1, { attributes: attrs });
  }

  // The server returned a status htmx won't swap (4xx other than 422, or 5xx),
  // so the user saw nothing happen. 5xx is also an error issue server-side;
  // this log ties it to the page and element the user was on.
  document.addEventListener("htmx:responseError", (e) => {
    const attrs = requestAttrs(e.detail);
    attrs["horizonte.htmx.trigger"] = (e.detail.elt && (e.detail.elt.id || e.detail.elt.tagName.toLowerCase())) || "";
    log(attrs["http.response.status_code"] >= 500 ? "error" : "warn", "htmx request failed", attrs);
    countFailure("response", e.detail);
  });

  // The request never reached the server (stopped, restarted, network
  // down). The server can't see these, so only the browser can report them.
  document.addEventListener("htmx:sendError", (e) => {
    log("error", "htmx request not sent", requestAttrs(e.detail));
    countFailure("send", e.detail);
  });

  document.addEventListener("htmx:timeout", (e) => {
    log("warn", "htmx request timed out", requestAttrs(e.detail));
    countFailure("timeout", e.detail);
  });

  // hx-target points at an element that isn't on the page: a template bug.
  document.addEventListener("htmx:targetError", (e) => {
    // detail.target is the hx-target selector (empty for htmx.ajax calls), so
    // the triggering element is logged too, to find the template.
    const elt = e.target instanceof Element ? e.target : null;
    log("error", "htmx target not found", {
      "horizonte.htmx.target": String(e.detail.target || ""),
      "horizonte.htmx.trigger": elt ? elt.id || elt.tagName.toLowerCase() : "",
      "horizonte.page": location.pathname,
    });
  });

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

  // A deploy changed the static files: this tab still runs the old CSS and
  // JavaScript (boosted navigation never reloads them), so new HTML would
  // render with the wrong styles. Load the page for real instead.
  const assets = document.querySelector('meta[name="horizonte-assets"]');
  document.addEventListener("htmx:beforeSwap", (e) => {
    const xhr = e.detail.xhr;
    const served = xhr && xhr.getResponseHeader("X-Horizonte-Assets");
    if (!assets || !served || served === assets.content) return;
    e.detail.shouldSwap = false;
    log("info", "assets changed, reloading", { "horizonte.page": location.pathname });
    if (e.detail.boosted && xhr.responseURL) location.assign(xhr.responseURL);
    else location.reload();
  });

  // Whatever was clicked shows it is working (a spinner on the button or
  // link, styled by .is-busy), so a slow response never looks like a click
  // that did nothing.
  const busy = new WeakMap();
  document.addEventListener("htmx:beforeRequest", (e) => {
    const elt = e.detail.elt;
    if (!elt || !elt.classList || !e.detail.xhr) return;
    if (elt.matches("a, button, [data-busy]")) {
      elt.classList.add("is-busy");
      busy.set(e.detail.xhr, elt);
    }
  });
  document.addEventListener("htmx:afterRequest", (e) => {
    const elt = e.detail.xhr && busy.get(e.detail.xhr);
    if (elt) elt.classList.remove("is-busy");
  });

  // Buttons that update a section further down the page (data-scroll-to)
  // bring it into view as the request starts, so the person sees it
  // working and then sees the result, instead of nothing changing.
  document.addEventListener("htmx:beforeRequest", (e) => {
    const sel = e.detail.elt && e.detail.elt.dataset && e.detail.elt.dataset.scrollTo;
    const target = sel && document.querySelector(sel);
    if (target) target.scrollIntoView({ behavior: "smooth", block: "start" });
  });

  // Seconds elapsed, in the "calculating" messages: a counter that moves
  // tells the person the app is alive even when the server is slow.
  setInterval(() => {
    document.querySelectorAll("[data-elapsed]").forEach((el) => {
      const active = el.closest(".sim-pending, .htmx-request");
      if (!active || (active.closest("[data-state]") && active.closest("[data-state]").dataset.state === "error")) {
        delete el.dataset.t0;
        el.textContent = "";
        return;
      }
      if (!el.dataset.t0) el.dataset.t0 = String(Date.now());
      const s = Math.floor((Date.now() - Number(el.dataset.t0)) / 1000);
      el.textContent = s >= 2 ? s + " s" : "";
    });
  }, 500);

  // Loading feedback. A thin bar at the top of the page appears when a
  // navigation or update takes more than a moment, so a slow server never
  // looks like a click that did nothing. Requests from simulation
  // placeholders ([data-sim]) are left out: they show their own spinner.
  const root = document.documentElement;
  let inFlight = 0;
  let showTimer = null;
  const tracked = new WeakSet();
  document.addEventListener("htmx:beforeRequest", (e) => {
    const elt = e.detail.elt;
    if (elt && elt.closest && elt.closest("[data-sim]")) return;
    if (e.detail.xhr) tracked.add(e.detail.xhr);
    inFlight++;
    if (!showTimer && !root.classList.contains("is-loading")) {
      showTimer = setTimeout(() => {
        showTimer = null;
        if (inFlight > 0) root.classList.add("is-loading");
      }, 300);
    }
  });
  document.addEventListener("htmx:afterRequest", (e) => {
    if (!e.detail.xhr || !tracked.has(e.detail.xhr)) return;
    tracked.delete(e.detail.xhr);
    inFlight = Math.max(0, inFlight - 1);
    if (inFlight === 0) {
      clearTimeout(showTimer);
      showTimer = null;
      root.classList.remove("is-loading");
    }
  });

  // A simulation placeholder whose request failed shows an error and a
  // "try again" button instead of spinning forever.
  function simFailed(e) {
    const elt = e.detail.elt;
    const sim = elt && elt.closest && elt.closest("[data-sim]");
    if (sim) sim.dataset.state = "error";
  }
  document.addEventListener("htmx:responseError", simFailed);
  document.addEventListener("htmx:sendError", simFailed);
  document.addEventListener("htmx:timeout", simFailed);
  document.addEventListener("click", (e) => {
    const btn = e.target.closest("[data-sim-retry]");
    if (!btn || !window.htmx) return;
    const sim = btn.closest("[data-sim]");
    if (!sim) return;
    delete sim.dataset.state;
    htmx.trigger(sim, "retry");
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
