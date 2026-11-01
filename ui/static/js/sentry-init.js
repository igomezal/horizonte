// Browser error and performance reporting. The server only includes this script
// when SENTRY_DSN is set, and passes the settings as data attributes.
//
// Horizonte holds personal financial data, so nothing beyond what's needed to
// debug is sent: no user info, no console output, and query strings are
// stripped from every URL (htmx GET forms put amounts in them).
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

  Sentry.init({
    dsn: el.dataset.dsn,
    environment: el.dataset.environment || undefined,
    release: el.dataset.release || undefined,
    sendDefaultPii: false,
    integrations: [
      Sentry.browserTracingIntegration(),
      Sentry.breadcrumbsIntegration({ console: false }),
    ],
    tracesSampleRate: 1.0,
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
})();
