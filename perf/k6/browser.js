// Synthetic traffic with real browsers: each virtual user drives headless
// Chromium through Horizonte by clicking links, the way a person would, so the
// app's browser JavaScript runs (htmx, charts, Sentry tracing, logs and Session
// Replay) and the server sees real page loads and htmx requests.
//
// Read-only by design: only <a href> links are clicked (no buttons, forms or
// selects), and links that download files (exports) are skipped, so the plan
// on the target server is never changed.
//
//   k6 run -e BASE_URL=http://192.168.1.128:8347 perf/k6/browser.js
//   options: -e VUS=30 -e DURATION=1h -e CLICKS=15 -e THINK_MIN=2 -e THINK_MAX=8
//            -e SETTLE_TIMEOUT=15 -e SLOW_SETTLE_TIMEOUT=60 (seconds)
//            -e SIMULATION_P95=10 (seconds, p95 limit for simulation pages)
//            -e CLICK_FAILURE_RATE=0.05 (highest share of clicks that may fail)
//
// Each iteration is one visit: a fresh browser context (new cookies, so a new
// Sentry session and replay sample), CLICKS link clicks with think time, then
// the context is closed.
import { browser } from "k6/browser";
import { check } from "k6";
import { Counter, Rate, Trend } from "k6/metrics";

const BASE_URL = (__ENV.BASE_URL || "http://127.0.0.1:8347").replace(/\/$/, "");
const VUS = parseInt(__ENV.VUS || "30", 10);
const DURATION = __ENV.DURATION || "1h";
const CLICKS = parseInt(__ENV.CLICKS || "15", 10);
const THINK_MIN = parseFloat(__ENV.THINK_MIN || "2");
const THINK_MAX = parseFloat(__ENV.THINK_MAX || "8");

// How long a click may take to finish its htmx swap. Simulation pages (Monte
// Carlo, scenarios, backtest) render a result straight away when it is cached
// (the cache is warmed at startup). Otherwise they render a placeholder and the
// browser fetches the result, which can be slow under load, so they get their
// own timeout. SIMULATION_P95 must be lower than that timeout: a click slower
// than the timeout is a failure and records no time, so a limit at or above the
// timeout could never be crossed. The script refuses to start otherwise.
const SETTLE_TIMEOUT = parseFloat(__ENV.SETTLE_TIMEOUT || "15") * 1000;
const SLOW_SETTLE_TIMEOUT = parseFloat(__ENV.SLOW_SETTLE_TIMEOUT || "60") * 1000;
const SIMULATION_P95 = parseFloat(__ENV.SIMULATION_P95 || "10") * 1000;
const CLICK_FAILURE_RATE = Number(__ENV.CLICK_FAILURE_RATE || "0.05");
const SLOW_PATHS = /^\/(success|scenarios|backtest)/;

if (!(CLICK_FAILURE_RATE >= 0 && CLICK_FAILURE_RATE <= 1)) {
  throw new Error(
    `CLICK_FAILURE_RATE ("${__ENV.CLICK_FAILURE_RATE}") must be a number from 0 to 1: ` +
      "it is the highest share of clicks that may fail, and 0 allows none",
  );
}

if (!(SIMULATION_P95 < SLOW_SETTLE_TIMEOUT)) {
  throw new Error(
    `SIMULATION_P95 (${SIMULATION_P95 / 1000} s) must be lower than SLOW_SETTLE_TIMEOUT (${SLOW_SETTLE_TIMEOUT / 1000} s): ` +
      "a click over the timeout records no time, so this limit could never be crossed",
  );
}

// "1h", "30m", "90s", "500ms" or "1h30m" to milliseconds.
function parseDuration(d) {
  let ms = 0;
  const re = /(\d+(?:\.\d+)?)(ms|h|m|s)/g;
  let m;
  while ((m = re.exec(d)) !== null) {
    ms += parseFloat(m[1]) * { h: 3600000, m: 60000, s: 1000, ms: 1 }[m[2]];
  }
  return ms;
}

// A regular desktop Chrome user agent, so Sentry's inbound filters don't drop
// the events as a headless browser or crawler.
const USER_AGENT =
  "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0.0.0 Safari/537.36";

export const options = {
  scenarios: {
    visitors: {
      executor: "constant-vus",
      vus: VUS,
      duration: DURATION,
      // Visits stop clicking once DURATION is over (see setup) and end on their
      // own; this only has to cover the last click and the close. Interrupting
      // a visit mid-click can leave its browser hanging and keep k6 from exiting.
      gracefulStop: `${Math.ceil(SLOW_SETTLE_TIMEOUT / 1000) + 60}s`,
      options: { browser: { type: "chromium" } },
    },
  },
  thresholds: {
    checks: ["rate>0.95"],
    browser_web_vital_lcp: ["p(75)<2500"],
    "synthetic_click_settle{kind:page}": ["p(95)<5000"],
    "synthetic_click_settle{kind:simulation}": [`p(95)<${SIMULATION_P95}`],
    // <= so that 0 means no failures allowed; rate<0 could never pass.
    synthetic_click_failed: [`rate<=${CLICK_FAILURE_RATE}`],
  },
};

const clicks = new Counter("synthetic_clicks");
const failedClicks = new Counter("synthetic_click_failures");
const clickFailed = new Rate("synthetic_click_failed"); // every click attempt, so failures can fail the run
const failedVisits = new Counter("synthetic_visit_failures");
const settle = new Trend("synthetic_click_settle", true); // click to htmx swap finished

async function think(page) {
  await page.waitForTimeout(1000 * (THINK_MIN + Math.random() * (THINK_MAX - THINK_MIN)));
}

// Loads the home page and records a check for it. Returns false, with the
// visit counted as failed, when it can't be loaded: page.goto throws on a
// connection error or timeout, and the caller then ends the visit.
async function loadHome(page) {
  let status = 0;
  try {
    const res = await page.goto(`${BASE_URL}/`, { waitUntil: "networkidle", timeout: 30000 });
    status = res ? await res.status() : 0;
  } catch (e) {
    console.warn(`loading the home page failed: ${e}`);
  }
  const ok = check(status, { "home loads": (s) => s === 200 });
  if (!ok) failedVisits.add(1);
  return ok;
}

// Marks one random visible, same-origin, non-download link with
// data-synthetic="next" and returns its path, or "" when there is none.
// Links in the sidebar are weighted down so visits also use in-page links
// (toggles, tool links, years), not only the menu.
function pickLink(page) {
  return page.evaluate(() => {
    document.querySelectorAll("[data-synthetic]").forEach((el) => el.removeAttribute("data-synthetic"));
    const here = location.pathname + location.search;
    const pool = [];
    document.querySelectorAll("a[href]").forEach((a) => {
      const url = new URL(a.href, location.href);
      if (url.origin !== location.origin) return;
      if (a.hasAttribute("download") || /\/export/.test(url.pathname)) return;
      if (url.pathname + url.search === here) return;
      const r = a.getBoundingClientRect();
      if (r.width === 0 || r.height === 0 || getComputedStyle(a).visibility === "hidden") return;
      const weight = a.closest("nav, aside") ? 1 : 3;
      for (let i = 0; i < weight; i++) pool.push(a);
    });
    if (pool.length === 0) return "";
    const a = pool[Math.floor(Math.random() * pool.length)];
    a.setAttribute("data-synthetic", "next");
    return new URL(a.href, location.href).pathname;
  });
}

// Runs inside the page, in every document of the visit, and times each click
// there. The page has to do this itself: k6 needs about 50 ms of round trips to
// start waiting and to read the result, and a swap on a fast page takes about
// that long, so timing from k6 would add a floor of its own.
//
// The page must not look finished just because the click has had no effect
// yet, so after a click the timer first asks what the click started:
//   - an htmx request (begun): finished when the busy selectors below are gone.
//     They cover requests in flight and simulation placeholders.
//   - a full navigation (leaving, hx-boost="false" links): not finished while
//     the old document is showing. The new document runs this function again
//     and finds the click time in sessionStorage, which survives navigation.
//   - nothing yet: keep looking for 500 ms after the click, then accept that
//     this click starts nothing. Such a click records no time, so that 500 ms
//     never enters the measurement.
// The time is taken from the click event to the first 10 ms check that finds
// the page finished, with no fixed wait in between.
//
// A request that fails ends the click at once, as a failure with no time. htmx
// clears .htmx-request and swaps nothing, so without this the old page would
// look finished and its heading would pass the "page rendered" check. htmx
// fires these events before it clears the class, in the same task, so a 10 ms
// check can't see the page as finished first.
//
// A full navigation fires no htmx event, and the app's error page has a heading
// too, so its status is read from the new document's navigation entry. That
// entry belongs to the document and never changes, so a boosted click on a page
// that itself loaded with an error would read the old status. Only a document
// that arrived through the current click reads it (arrived, which armClick
// clears before every click).
function clickTracker() {
  if (!location.protocol.startsWith("http")) return;
  const key = "synthetic-click-at";
  const at = Number(sessionStorage.getItem(key)) || 0;
  const s = { at, begun: at > 0, arrived: at > 0, leaving: false, finished: false, ms: null, failed: null };
  window.__syntheticClick = s;

  const watch = () => {
    const timer = setInterval(() => {
      if (s.finished) {
        clearInterval(timer);
        return;
      }
      if (s.leaving) return;
      if (!s.begun && Date.now() - s.at < 500) return;
      if (document.readyState !== "complete") return;
      if (s.arrived) {
        const nav = performance.getEntriesByType("navigation")[0];
        if (nav && nav.responseStatus >= 400) {
          s.failed = `full navigation (status ${nav.responseStatus})`;
          s.finished = true;
          clearInterval(timer);
          return;
        }
      }
      if (document.querySelector(".htmx-request, .is-pending, .sim-inline")) return;
      clearInterval(timer);
      s.ms = s.begun ? Date.now() - s.at : null;
      s.finished = true;
    }, 10);
  };

  document.addEventListener(
    "click",
    () => {
      if (s.at) return;
      s.at = Date.now();
      sessionStorage.setItem(key, String(s.at));
      watch();
    },
    true,
  );
  document.addEventListener("htmx:beforeRequest", () => {
    s.begun = true;
  });
  for (const name of ["htmx:responseError", "htmx:sendError", "htmx:timeout", "htmx:swapError"]) {
    document.addEventListener(name, (e) => {
      if (!s.at || s.finished) return; // not part of a click attempt
      const xhr = e.detail && e.detail.xhr;
      s.failed = `${name}${xhr && xhr.status ? ` (status ${xhr.status})` : ""}`;
      s.finished = true;
    });
  }
  window.addEventListener("beforeunload", () => {
    s.leaving = true;
  });
  if (at > 0) watch();
}

// Clears the previous click so the tracker times the next one.
function armClick(page) {
  return page.evaluate(() => {
    sessionStorage.removeItem("synthetic-click-at");
    Object.assign(window.__syntheticClick, { at: 0, begun: false, arrived: false, leaving: false, finished: false, ms: null, failed: null });
  });
}

// Waits until clickTracker says the page is finished, which is what the person
// is waiting for, and records the time it measured. The timeout is checked
// against that time too: the page can finish before k6 starts waiting, and then
// waitForFunction returns at once however long the click took.
async function settled(page, path) {
  const slow = SLOW_PATHS.test(path);
  const limit = slow ? SLOW_SETTLE_TIMEOUT : SETTLE_TIMEOUT;
  await page.waitForFunction(() => !!window.__syntheticClick && window.__syntheticClick.finished, {
    timeout: limit,
    polling: 25,
  });
  const { ms, failed } = await page.evaluate(() => {
    const s = window.__syntheticClick;
    return { ms: s.ms, failed: s.failed };
  });
  if (failed) throw new Error(`request failed: ${failed}`);
  if (ms === null) return;
  if (ms > limit) throw new Error(`settled after ${ms} ms, over the ${limit} ms limit`);
  settle.add(ms, { kind: slow ? "simulation" : "page" });
}

// The test's end time, shared by every VU, so each visit knows when DURATION
// is over and stops by itself instead of being interrupted by k6.
export function setup() {
  return { deadline: Date.now() + parseDuration(DURATION) };
}

function timeUp(data) {
  return Date.now() >= data.deadline;
}

// One visit: the home page, then up to CLICKS clicks. It returns early when the
// home page can't be loaded, because there is nothing left to click.
async function visit(page, data) {
  if (!(await loadHome(page))) return;
  await think(page);

  for (let i = 0; i < CLICKS && !timeUp(data); i++) {
    const path = await pickLink(page);
    if (!path) {
      if (!(await loadHome(page))) return;
      continue;
    }
    try {
      await armClick(page);
      await page.locator('[data-synthetic="next"]').click({ timeout: 10000 });
      await settled(page, path);
      // k6's check does not await, so read the page first.
      const rendered = await page.evaluate(() => !!document.querySelector("main h1"));
      check(rendered, { "page rendered": (r) => r });
      clicks.add(1, { path });
      clickFailed.add(false);
    } catch (e) {
      failedClicks.add(1, { path });
      clickFailed.add(true);
      console.warn(`click on ${path} failed: ${e}`);
      if (!(await loadHome(page))) return;
    }
    await think(page);
  }
}

export default async function (data) {
  if (timeUp(data)) {
    // setup runs just before the scenario starts, so the deadline comes a few
    // milliseconds before k6 stops the VUs: wait instead of spinning. No page
    // is open here, and sleep() would block the event loop.
    await new Promise((resolve) => setTimeout(resolve, 1000));
    return;
  }
  let context;
  let page;
  try {
    context = await browser.newContext({ userAgent: USER_AGENT, viewport: { width: 1440, height: 900 } });
    await context.addInitScript(`(${clickTracker.toString()})()`);
    page = await context.newPage();
    await visit(page, data);
  } catch (e) {
    // Anything else that breaks the visit (a page that can't be opened or
    // crashes, for example) ends it here, so the exception can't escape the
    // iteration.
    failedVisits.add(1);
    console.warn(`visit failed: ${e}`);
  } finally {
    // Give the Sentry SDK a moment to flush the last transaction and replay
    // segment, then close; a failed close must not stop the VU's next visit.
    try {
      if (page) {
        await page.waitForTimeout(2000);
        await page.close();
      }
    } catch (e) {
      console.warn(`closing the page failed: ${e}`);
    }
    try {
      if (context) await context.close();
    } catch (e) {
      console.warn(`closing the browser context failed: ${e}`);
    }
  }
}
