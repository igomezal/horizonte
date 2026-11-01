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
