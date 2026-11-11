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
      const active = el.closest(".is-pending, .htmx-request");
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
    // The plan card's chip must not keep saying "calculating".
    const chip = sim && sim.closest(".glance") && document.getElementById("glance-chip");
    if (chip && chip.dataset.errorLabel && !chip.dataset.was) {
      chip.dataset.was = chip.className + "|" + chip.textContent;
      chip.className = "chip chip-warning";
      chip.textContent = chip.dataset.errorLabel;
    }
  }
  function restoreGlanceChip() {
    const chip = document.getElementById("glance-chip");
    if (!chip || !chip.dataset.was) return;
    const i = chip.dataset.was.indexOf("|");
    chip.className = chip.dataset.was.slice(0, i);
    chip.textContent = chip.dataset.was.slice(i + 1);
    delete chip.dataset.was;
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
    restoreGlanceChip();
    htmx.trigger(sim, "retry");
  });

  // Entry animation: html.enter is set by theme-init.js on a full load and
  // here for boosted navigation (before the swap). It is removed once the
  // animation has played, so result swaps never replay it.
  let enterTimer = null;
  function enterOnce() {
    if (window.matchMedia("(prefers-reduced-motion: reduce)").matches) return;
    delete root.dataset.enterSkip;
    root.classList.add("enter");
    clearTimeout(enterTimer);
    enterTimer = setTimeout(() => { root.classList.remove("enter"); delete root.dataset.enterSkip; }, 1400);
  }
  const RESULT_REGIONS = ".sim-region, #plan-summary, #plan-table, .glance-odds";
  // True while the swap in progress replaces a placeholder (a pending region),
  // false when it replaces an existing result (a recalculation).
  let swapFromPending = false;
  enterOnce();
  // A page navigation (boosted) animates; any other swap (a result replacing
  // its placeholder, a section reloading) must never fade in, whatever the
  // timing, so the first one switches the entry animation off. This is done
  // with the class rather than per element because htmx re-applies attributes
  // when it settles a swapped node, which would restart a per-element override.
  document.addEventListener("htmx:beforeSwap", (e) => {
    const target = e.detail.target;
    swapFromPending = !!(target && target.classList && target.classList.contains("is-pending"));
    if (e.detail.boosted) {
      if (e.detail.shouldSwap !== false) enterOnce();
    } else if (target && target.matches && target.matches(RESULT_REGIONS)) {
      // A result arriving: only the result regions stop their entry animation
      // (the rest of the page keeps fading in); the result has its own fade.
      // Only the swapped region stops its entry; static sections keep their stagger.
      if (root.classList.contains("enter") && target.id) root.dataset.enterSkip = ((root.dataset.enterSkip || "") + " " + target.id).trim();
    } else {
      clearTimeout(enterTimer);
      root.classList.remove("enter");
      delete root.dataset.enterSkip;
    }
  });

  // A result that replaces its placeholder fades in from nothing (opacity only,
  // 260ms). A recalculation replaces a result the reader is already looking at,
  // so it only crossfades from the dimmed state: the card never blanks. Never
  // under reduced motion; the page entry animation is not involved.
  document.addEventListener("htmx:load", (e) => {
    const el = e.detail.elt;
    if (!el || !el.matches || !el.matches(RESULT_REGIONS)) return;
    if (window.matchMedia("(prefers-reduced-motion: reduce)").matches || !el.animate) return;
    el.animate([{ opacity: swapFromPending ? 0 : 0.45 }, { opacity: 1 }], { duration: 260, easing: "ease-out" });
  });

  // Scroll position and focus across a partial swap. A swap replaces the node
  // browser scroll anchoring has chosen as its anchor, and (narrow screens
  // especially) the anchor it picks next is further down the page, so the
  // viewport jumped there when a toggle in a result card was clicked.
  // So anchoring is switched off for the length of a partial swap, and the
  // position is kept by hand instead: the element the reader is working with
  // (the focused one, which may be a field below a result that changes height)
  // is measured before the swap and the page scrolled by however far it moved.
  // Boosted navigation is left alone. A link that had focus is replaced by an
  // identical one; it gets the focus back (without scrolling) so keyboard
  // users keep their place.
  // State is per request, and every way a swap can end (settle, error, a
  // timeout) releases it, so anchoring never stays off.
  const swaps = new Map();
  function releaseAnchor() {
    if (swaps.size) return;
    // Two frames later the charts have rendered and the layout has settled.
    requestAnimationFrame(() => requestAnimationFrame(() => { if (!swaps.size) root.style.overflowAnchor = ""; }));
  }
  function endSwap(key, compensate) {
    const s = swaps.get(key);
    if (!s) return;
    clearTimeout(s.timer);
    swaps.delete(key);
    if (compensate && s.ref) {
      const el = s.ref.el.isConnected ? s.ref.el : (s.ref.id && document.getElementById(s.ref.id)) || (s.refocused && s.refocused.isConnected ? s.refocused : null);
      const active = document.activeElement;
      // No place to keep when the reference was hidden by the swap (an "Add"
      // button that gives way to its form measures 0), or when focus moved
      // into the new content (a form's first field): that is where the reader
      // is now, so the page is left alone.
      const placed = el && el.getClientRects().length > 0 && (active === el || active === document.body || active === root);
      if (placed) {
        const delta = el.getBoundingClientRect().top - s.ref.top;
        if (Math.abs(delta) > 0.5) window.scrollBy({ top: delta, left: 0, behavior: "instant" });
      }
    }
    releaseAnchor();
  }
  // A link's place among the links of its toggle group (the group's accessible
  // name plus the position inside it), so links added before the toggles can't
  // shift it.
  function linkSlot(a, scope) {
    const group = a.closest("[role=group]");
    if (group && scope.contains(group)) {
      return { group: group.getAttribute("aria-label"), index: Array.from(group.querySelectorAll("a")).indexOf(a) };
    }
    return { group: null, index: Array.from(scope.querySelectorAll("a")).indexOf(a) };
  }
  function slotLink(slot, scope) {
    if (slot.group === null) return Array.from(scope.querySelectorAll("a"))[slot.index];
    const group = Array.from(scope.querySelectorAll("[role=group]")).find((g) => g.getAttribute("aria-label") === slot.group);
    return group ? group.querySelectorAll("a")[slot.index] : null;
  }
  document.addEventListener("htmx:beforeSwap", (e) => {
    const xhr = e.detail.xhr;
    if (e.detail.boosted || e.detail.shouldSwap === false || !xhr) return;
    root.style.overflowAnchor = "none";
    const target = e.detail.target;
    const active = document.activeElement;
    const s = { timer: setTimeout(() => endSwap(xhr, false), 5000), ref: null, slot: null, id: target && target.id, refocused: null };
    if (active && active !== document.body && active !== root) {
      s.ref = { el: active, id: active.id, top: active.getBoundingClientRect().top };
      if (active.tagName === "A" && target && target.contains(active)) s.slot = linkSlot(active, target);
    }
    swaps.set(xhr, s);
  });
  document.addEventListener("htmx:afterSwap", (e) => {
    const s = swaps.get(e.detail.xhr);
    if (!s || !s.slot) return;
    // outerHTML replaces the target, so the new node is found by its id.
    const t = s.id ? document.getElementById(s.id) : null;
    const link = t && slotLink(s.slot, t);
    if (link) {
      link.focus({ preventScroll: true });
      s.refocused = link;
    }
  });
  document.addEventListener("htmx:afterSettle", (e) => endSwap(e.detail.xhr, true));
  document.addEventListener("htmx:swapError", (e) => endSwap(e.detail.xhr, false));
  // A swap that only touches detached nodes (hx-swap="delete", an emptied
  // outerHTML) never reports a settle to the document; end it once the
  // request is over and any settle (20ms) would have come.
  document.addEventListener("htmx:afterRequest", (e) => {
    const xhr = e.detail.xhr;
    if (xhr && swaps.has(xhr)) setTimeout(() => endSwap(xhr, true), 150);
  });

  // Connection indicator and toast. Client-side only: navigator.onLine, the
  // online/offline events and htmx network failures (htmx:sendError). Nothing
  // polls the server.
  let offline = !navigator.onLine;
  let toastTimer = null;
  function showToast(kind) {
    const toast = document.querySelector("[data-toast]");
    if (!toast) return;
    toast.querySelector("[data-toast-text]").textContent = toast.dataset[kind === "offline" ? "textOffline" : "textOnline"];
    toast.classList.toggle("is-offline", kind === "offline");
    toast.classList.add("is-visible");
    clearTimeout(toastTimer);
    // --toast-duration comes from the stylesheet (default 5s).
    const raw = getComputedStyle(root).getPropertyValue("--toast-duration").trim() || "5s";
    const ms = raw.endsWith("ms") ? parseFloat(raw) : parseFloat(raw) * 1000;
    toastTimer = setTimeout(hideToast, ms || 5000);
  }
  function hideToast() {
    const toast = document.querySelector("[data-toast]");
    if (toast) toast.classList.remove("is-visible");
  }
  function renderConnection() {
    document.querySelectorAll("[data-conn]").forEach((el) => {
      el.classList.toggle("is-offline", offline);
      el.classList.toggle("chip-live", !offline);
      const label = el.querySelector("[data-conn-label]");
      if (label) label.textContent = offline ? el.dataset.labelOffline : el.dataset.labelOnline;
    });
  }
  function setOffline(next, announce) {
    if (offline === next) return;
    offline = next;
    renderConnection();
    if (announce) showToast(offline ? "offline" : "online");
  }
  window.addEventListener("offline", () => setOffline(true, true));
  window.addEventListener("online", () => setOffline(false, true));
  document.addEventListener("htmx:sendError", () => setOffline(true, true));
  document.addEventListener("htmx:afterRequest", (e) => {
    if (e.detail.xhr && e.detail.xhr.status) setOffline(false, true); // a response came back
  });
  document.addEventListener("htmx:afterSettle", renderConnection);
  document.addEventListener("click", (e) => {
    if (e.target.closest("[data-toast-dismiss]")) hideToast();
  });
  renderConnection(); // the initial state is shown in the chip only, not announced

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
