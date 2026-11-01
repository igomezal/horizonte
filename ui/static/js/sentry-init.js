// Browser error and performance reporting. The server only includes this script
// when SENTRY_DSN is set, and passes the settings as data attributes.
//
// Horizonte holds personal financial data, so nothing beyond what's needed to
// debug is sent: no user info, console output only when it can't hold figures,
// query strings are stripped
// from every URL (htmx GET forms put amounts in them), and Session Replay
// masks all text and blocks all media.
(function () {
  var el = document.currentScript;
  if (!window.Sentry || !el || !el.dataset.dsn) return;

  function stripQuery(url) {
    return typeof url === "string" ? url.split(/[?#]/)[0] : url;
  }

  function scrubBreadcrumb(b) {
    if (b.data) {
      if (b.data.url) b.data.url = stripQuery(b.data.url);
      if (b.data.from) b.data.from = stripQuery(b.data.from);
      if (b.data.to) b.data.to = stripQuery(b.data.to);
    }
    return b;
  }

  function scrubRequest(event) {
    if (event.request) {
      event.request.url = stripQuery(event.request.url);
      delete event.request.query_string;
      delete event.request.cookies;
    }
    return event;
  }

  // Backstop for logs and metrics: strip query strings from any URL-like
  // attribute, in case a future call passes a full URL.
  function scrubAttributes(item) {
    var attrs = item.attributes || {};
    Object.keys(attrs).forEach(function (k) {
      if (typeof attrs[k] === "string" && /(^|\.)(url|path|page)/.test(k)) attrs[k] = stripQuery(attrs[k]);
    });
    return item;
  }

  // Console capture rule: a console message that might hold financial data
  // is not sent at all. Redacting is not enough, since app code, htmx and
  // Alpine can print anything (DOM elements, form values, chart data). A
  // console message is dropped when, after stripping URL query strings, it
  // contains any of:
  //   - a currency sign or code (€, $, £, EUR)
  //   - a number with a decimal or thousands separator (1.234, 12,5)
  //   - a run of 4 or more digits (amounts, account numbers; years too)
  //   - HTML markup (serialized elements carry their text and values)
  //   - a serialized object or array (could hold any field)
  // Short integers (HTTP status codes, counts) and plain text pass. The
  // console arguments are never sent as separate attributes.
  var FINANCIAL_PATTERNS = [
    /[\u20ac$\u00a3]|\bEUR\b/i,
    /\d[.,]\d/,
    /\d{4,}/,
    /<[a-z!\/][^>]*>/i,
    /[{[]\s*"/,
  ];

  function stripQueries(text) {
    return text.replace(/(\/[^\s?#"'<>]*)[?#][^\s"'<>]*/g, "$1");
  }

  function scrubConsoleLog(entry) {
    var message = stripQueries(String(entry.message || ""));
    for (var i = 0; i < FINANCIAL_PATTERNS.length; i++) {
      if (FINANCIAL_PATTERNS[i].test(message)) return null;
    }
    entry.message = message.length > 500 ? message.slice(0, 500) + "…" : message;
    var attrs = entry.attributes || {};
    Object.keys(attrs).forEach(function (k) {
      if (k.indexOf("sentry.message.") === 0) delete attrs[k]; // raw console arguments
    });
    return entry;
  }

  function scrubLog(entry) {
    if (entry.level === "debug" || entry.level === "trace") return null; // noise
    var attrs = entry.attributes || {};
    if (attrs["sentry.origin"] === "auto.log.console" && !scrubConsoleLog(entry)) return null;
    return scrubAttributes(entry);
  }

  // Session Replay recording events: network and navigation entries carry
  // full URLs, so strip their query strings; drop console entries outright.
  function scrubRecordingEvent(event) {
    var data = event.data || {};
    var payload = data.payload;
    if (!payload) return event;
    if (data.tag === "performanceSpan") {
      payload.description = stripQuery(payload.description);
    } else if (data.tag === "breadcrumb") {
      if (payload.category === "console") return null;
      scrubBreadcrumb(payload);
    }
    return event;
  }

  Sentry.init({
    dsn: el.dataset.dsn,
    environment: el.dataset.environment || undefined,
    release: el.dataset.release || undefined,
    sendDefaultPii: false,
    integrations: [
      Sentry.browserTracingIntegration(),
      Sentry.breadcrumbsIntegration({ console: false }),
      // Browser console (ours, htmx, Alpine) → Sentry Logs, filtered by
      // scrubConsoleLog: anything that might hold financial data is dropped.
      // Console breadcrumbs on error events stay off.
      Sentry.consoleLoggingIntegration({ levels: ["log", "info", "warn", "error", "assert"] }),
      // Session Replay, locked down for financial data: every text node and
      // input value is masked (shown as ***), and all media is blocked, which
      // includes the charts (ECharts renders SVG), so no amounts and no chart
      // shapes are recorded. Network requests are kept to method, status and
      // size: no headers or bodies (networkDetailAllowUrls stays empty).
      Sentry.replayIntegration({
        maskAllText: true,
        maskAllInputs: true,
        blockAllMedia: true,
        beforeAddRecordingEvent: scrubRecordingEvent,
      }),
    ],
    // Record every session in development, 10% otherwise; sessions with an
    // error always keep the 60 seconds before it. Unsampled sessions are only
    // buffered in memory and never leave the browser.
    replaysSessionSampleRate: el.dataset.environment === "development" ? 1.0 : 0.1,
    replaysOnErrorSampleRate: 1.0,
    tracesSampleRate: 1.0,
    // Sentry Logs: Sentry.logger.* calls in app.js and charts.js, plus the
    // filtered browser console (see consoleLoggingIntegration above).
    enableLogs: true,
    beforeSendLog: scrubLog,
    // Sentry Metrics (Sentry.metrics.* in app.js and charts.js) are on by
    // default; the same scrub keeps query strings out of their attributes.
    beforeSendMetric: scrubAttributes,
    // Same-origin requests (htmx) carry trace headers, linking browser and server spans.
    beforeBreadcrumb: scrubBreadcrumb,
    beforeSend: scrubRequest,
    beforeSendTransaction: function (event) {
      scrubRequest(event);
      if (event.transaction) event.transaction = stripQuery(event.transaction);
      (event.spans || []).forEach(function (s) {
        if (s.description) s.description = s.description.replace(/\?[^\s]*/, "");
        if (s.data && s.data.url) s.data.url = stripQuery(s.data.url);
        if (s.data && s.data["http.query"]) delete s.data["http.query"];
      });
      return event;
    },
  });

  // Replay events list the page URLs visited; keep only their paths.
  Sentry.addEventProcessor(function (event) {
    if (event.type === "replay_event" && Array.isArray(event.urls)) {
      event.urls = event.urls.map(stripQuery);
    }
    return event;
  });
})();
