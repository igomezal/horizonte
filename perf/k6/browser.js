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
//
// Each iteration is one visit: a fresh browser context (new cookies, so a new
// Sentry session and replay sample), CLICKS link clicks with think time, then
// the context is closed.
import { browser } from "k6/experimental/browser";
import { check, sleep } from "k6";
import { Counter, Trend } from "k6/metrics";

const BASE_URL = (__ENV.BASE_URL || "http://127.0.0.1:8347").replace(/\/$/, "");
const VUS = parseInt(__ENV.VUS || "30", 10);
const DURATION = __ENV.DURATION || "1h";
const CLICKS = parseInt(__ENV.CLICKS || "15", 10);
const THINK_MIN = parseFloat(__ENV.THINK_MIN || "2");
const THINK_MAX = parseFloat(__ENV.THINK_MAX || "8");

// How long a click may take to finish its htmx swap. Simulation pages (Monte
// Carlo, scenarios, backtest) run the engine on request and are much slower
// under load, so they get their own limit: a slow page is measured, not failed.
const SETTLE_TIMEOUT = parseFloat(__ENV.SETTLE_TIMEOUT || "15") * 1000;
const SLOW_SETTLE_TIMEOUT = parseFloat(__ENV.SLOW_SETTLE_TIMEOUT || "60") * 1000;
const SLOW_PATHS = /^\/(success|scenarios|backtest)/;

// "1h", "30m", "90s" or "1h30m" to milliseconds.
function parseDuration(d) {
  let ms = 0;
  const re = /(\d+(?:\.\d+)?)(h|m|s)/g;
  let m;
  while ((m = re.exec(d)) !== null) {
    ms += parseFloat(m[1]) * { h: 3600000, m: 60000, s: 1000 }[m[2]];
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
    "synthetic_click_settle{kind:simulation}": [`p(95)<${SLOW_SETTLE_TIMEOUT}`],
  },
};

const clicks = new Counter("synthetic_clicks");
const failedClicks = new Counter("synthetic_click_failures");
const settle = new Trend("synthetic_click_settle", true); // click to htmx swap finished

function think() {
  sleep(THINK_MIN + Math.random() * (THINK_MAX - THINK_MIN));
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

// Waits until htmx has no request in flight (boosted links and toggles swap
// content without a full navigation) and the simulation pages have replaced
// their placeholders with results, which is what the person is waiting for.
async function settled(page, path) {
  const slow = SLOW_PATHS.test(path);
  const start = Date.now();
  await page.waitForTimeout(150);
  await page.waitForFunction(() => !document.querySelector(".htmx-request, .is-pending, .sim-inline"), {
    timeout: slow ? SLOW_SETTLE_TIMEOUT : SETTLE_TIMEOUT,
    polling: 100,
  });
  settle.add(Date.now() - start, { kind: slow ? "simulation" : "page" });
}

// The test's end time, shared by every VU, so each visit knows when DURATION
// is over and stops by itself instead of being interrupted by k6.
export function setup() {
  return { deadline: Date.now() + parseDuration(DURATION) };
}

function timeUp(data) {
  return Date.now() >= data.deadline;
}

export default async function (data) {
  if (timeUp(data)) {
    // setup runs just before the scenario starts, so the deadline comes a few
    // milliseconds before k6 stops the VUs: wait instead of spinning.
    sleep(1);
    return;
  }
  const context = browser.newContext({ userAgent: USER_AGENT, viewport: { width: 1440, height: 900 } });
  const page = context.newPage();
  try {
    const res = await page.goto(`${BASE_URL}/`, { waitUntil: "networkidle", timeout: 30000 });
    check(res, { "home loads": (r) => r && r.status() === 200 });
    think();

    for (let i = 0; i < CLICKS && !timeUp(data); i++) {
      const path = pickLink(page);
      if (!path) {
        await page.goto(`${BASE_URL}/`, { waitUntil: "networkidle" });
        continue;
      }
      try {
        await page.locator('[data-synthetic="next"]').click({ timeout: 10000 });
        await settled(page, path);
        clicks.add(1, { path });
        check(page, { "page rendered": (p) => p.evaluate(() => !!document.querySelector("main h1")) });
      } catch (e) {
        failedClicks.add(1, { path });
        console.warn(`click on ${path} failed: ${e}`);
        await page.goto(`${BASE_URL}/`, { waitUntil: "networkidle" });
      }
      think();
    }
  } finally {
    // Give the Sentry SDK a moment to flush the last transaction and replay
    // segment, then close; a failed close must not stop the VU's next visit.
    try {
      await page.waitForTimeout(2000);
      page.close();
    } catch (e) {
      console.warn(`closing the page failed: ${e}`);
    }
    try {
      context.close();
    } catch (e) {
      console.warn(`closing the browser context failed: ${e}`);
    }
  }
}
