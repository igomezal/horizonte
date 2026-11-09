// Load test: 15 virtual users navigating Horizonte for 20 minutes.
//
//   k6 run perf/k6/navigation.js
//   k6 run -e BASE_URL=http://127.0.0.1:8090 -e VUS=15 -e DURATION=20m perf/k6/navigation.js
//
// Each virtual user (VU) repeats one of four journeys, picked by weight, with
// 0.5 to 6 s of think time between steps and journeys. Full pages are requested like a boosted
// htmx navigation; partial updates (plan view toggles, Monte Carlo reseed,
// cash-flow year slider, backtest scenarios) send the same HX-Request header
// htmx sends. The run is read-only apart from seeding an empty database in
// setup(), so 15 VUs sharing one single-user database don't interfere.
//
// /success, /backtest and /scenarios answer at once with a placeholder when
// their simulation is not cached, and the browser then fetches the results
// through elements with hx-trigger="load, ...". followDeferred() issues those
// same requests, found in the response HTML, so a page that renders its result
// inline issues none. Only these deferred results and the reseed count as
// kind:simulation. After the first request a result comes from the server's
// simulation cache (which the server also warms at startup), so most deferred
// requests are cache lookups or do not happen at all. reseed=1 is the request
// that forces a new Monte Carlo run, and it writes nothing to the database.
//
// Run it against a dedicated database, never your real one (see perf/README.md).

import http from "k6/http";
import { check, group, sleep, fail } from "k6";
import { Trend, Counter } from "k6/metrics";

const BASE = __ENV.BASE_URL || "http://127.0.0.1:8090";
const VUS = Number(__ENV.VUS || 15);
const DURATION = __ENV.DURATION || "20m";

// Request names (the `name` tag). Each gets a no-op threshold below so k6
// reports its latency separately in the summary.
const ENDPOINTS = [
  "GET /", "GET /plan", "GET /plan/results", "GET /success", "GET /success/results",
  "GET /success/results (reseed)", "GET /scenarios", "GET /scenarios/{id}/odds",
  "GET /cashflow", "GET /cashflow/year", "GET /backtest", "GET /backtest/summary",
  "GET /backtest/heat", "GET /backtest/scenario", "GET /finances", "GET /finances/{kind}",
  "GET /finances/summary", "GET /finances/pension", "GET /finances/health", "GET /tracking",
  "static",
];
const PER_ENDPOINT = "max>=0"; // always passes; only there to create the sub-metric

export const options = {
  scenarios: {
    navigation: {
      executor: "constant-vus",
      vus: VUS,
      duration: DURATION,
      gracefulStop: "30s",
    },
  },
  // Pass/fail criteria. Simulation results (Monte Carlo, rolling backtest,
  // scenario odds) get a looser budget than plain pages.
  thresholds: {
    http_req_failed: ["rate<0.01"],
    checks: ["rate>0.99"],
    "checks{kind:static}": ["rate>0.99"],
    "http_req_duration{kind:page}": ["p(95)<800"],
    "http_req_duration{kind:partial}": ["p(95)<800"],
    "http_req_duration{kind:simulation}": ["p(95)<3000"],
    journey_duration: ["p(95)<60000"],
  },
  summaryTrendStats: ["count", "avg", "min", "med", "p(90)", "p(95)", "p(99)", "max"],
};

for (const n of ENDPOINTS) options.thresholds[`http_req_duration{name:${n}}`] = [PER_ENDPOINT];

const journeyDuration = new Trend("journey_duration", true);
const journeys = new Counter("journeys");

// ---------- request helpers ----------

// Browsers always accept compressed responses. k6 hands back the decompressed
// body, so the marker checks below still read the HTML.
const GZIP = { "Accept-Encoding": "gzip" };

const partialParams = (name, kind) => ({
  headers: { ...GZIP, "HX-Request": "true", "HX-Current-URL": BASE + "/" },
  tags: { name, kind },
});

// page: a full page, as hx-boost requests it (whole document swapped in).
function page(path, name, kind = "page", marker = "<main") {
  const res = http.get(BASE + path, {
    headers: { ...GZIP, "HX-Request": "true", "HX-Boosted": "true", "HX-Current-URL": BASE + "/" },
    tags: { name, kind },
  });
  check(res, {
    [`${name} 200`]: (r) => r.status === 200,
    [`${name} rendered`]: (r) => typeof r.body === "string" && r.body.includes(marker),
  });
  return res;
}

// partial: an htmx fragment request (hx-get on a button, link or slider).
function partial(path, name, kind = "partial") {
  const res = http.get(BASE + path, partialParams(name, kind));
  check(res, { [`${name} 200`]: (r) => r.status === 200 });
  return res;
}

const decodeAmp = (s) => s.replace(/&amp;/g, "&");

// Opening tags, allowing ">" inside quoted attribute values.
const TAG = /<[a-z][^\s>]*\s(?:"[^"]*"|'[^']*'|[^'">])*>/gi;
const attr = (tag, name) => (tag.match(new RegExp(`\\s${name}="([^"]*)"`)) || [])[1];

// deferredUrls: the hx-get URL of every element htmx would fetch on load.
function deferredUrls(html) {
  const urls = new Set();
  for (const [tag] of html.matchAll(TAG)) {
    const url = attr(tag, "hx-get");
    const trigger = attr(tag, "hx-trigger");
    if (url && trigger && trigger.split(",").some((t) => t.trim().split(/\s+/)[0] === "load")) {
      urls.add(decodeAmp(url));
    }
  }
  return [...urls];
}

// followDeferred issues a page's deferred requests together, as htmx does, so
// /backtest/summary and /backtest/heat share one calculation on the server.
//
// There is nothing to poll: the server queues a busy simulation and answers
// when it is done. It has no "not ready" or retry response. A simulation that
// fails returns 500, and the browser then shows an error and waits for a click
// on "try again", so a failure here is counted, not retried. An empty 200 is
// what the server sends when the client left, which is also a failure here.
function followDeferred(pageRes) {
  if (typeof pageRes.body !== "string") return;
  const urls = deferredUrls(pageRes.body);
  if (!urls.length) return;
  const names = urls.map((u) => `GET ${u.split("?")[0].replace(/\/\d+/g, "/{id}")}`);
  const responses = http.batch(urls.map((u, i) => ["GET", BASE + u, null, partialParams(names[i], "simulation")]));
  responses.forEach((res, i) => {
    check(res, {
      [`${names[i]} 200`]: (r) => r.status === 200,
      [`${names[i]} has a result`]: (r) => typeof r.body === "string" && r.body.length > 0,
    });
  });
}

function think(min = 1, max = 4) {
  sleep(min + Math.random() * (max - min));
}

function pick(list) {
  return list[Math.floor(Math.random() * list.length)];
}

// ---------- setup: seed a plan once, discover dynamic values ----------

// Same plan the Go tests use (internal/web/cashflow_test.go seedCashflow,
// success_test.go seedMC): one person, accounts, incomes, expenses, a debt
// and milestones, so every page has real work to do.
const SEED = [
  ["/finances/people", { name: "Ana", birth: "1990-04", life_expectancy: "90" }],
  ["/finances/milestones", { name: "Jubilación", kind: "age", person: "1", age: "65" }],
  ["/finances/milestones", { name: "Pensión", kind: "age", person: "1", age: "67" }],
  ["/finances/accounts", { name: "Cuenta", kind: "cash", balance: "40000" }],
  ["/finances/accounts", { name: "Fondo", kind: "investment", balance: "20000", contribution: "300", contrib_end_ms: "1" }],
  ["/finances/incomes", { name: "Nómina", kind: "salary", amount: "2500", payments: "14", end_ms: "1" }],
  ["/finances/incomes", { name: "Pensión pública", kind: "pension", amount: "1800", payments: "14", start_ms: "2" }],
  ["/finances/expenses", { name: "Alquiler", category: "housing", amount: "900", payments: "12" }],
  ["/finances/expenses", { name: "Comida", category: "food", amount: "400", payments: "12" }],
  ["/finances/debts", { name: "Coche", kind: "car", balance: "6000", rate: "0", payment: "250" }],
  ["/finances/milestones", { name: "FIRE", kind: "fi", fi_variant: "standard", multiple: "25" }],
];

export function setup() {
  const plan = http.get(BASE + "/plan", { tags: { name: "setup" } });
  if (plan.status !== 200) fail(`server not reachable at ${BASE} (status ${plan.status})`);

  // Seed only an empty database, so a re-run reuses the same data.
  if (plan.body.includes('id="plan-summary"') === false) {
    for (const [path, form] of SEED) {
      const res = http.post(BASE + path, form, { headers: { "HX-Request": "true" }, tags: { name: "setup" } });
      if (res.status !== 200) fail(`seeding ${path} failed: ${res.status} ${String(res.body).slice(0, 200)}`);
    }
  }

  // Values the journeys need, read from the pages like a browser would.
  const bt = http.get(BASE + "/backtest", { tags: { name: "setup" } }).body;
  const crises = [...new Set([...bt.matchAll(/crisis=([a-z0-9_-]+)/g)].map((m) => m[1]))];
  const cf = http.get(BASE + "/cashflow", { tags: { name: "setup" } }).body;
  const slider = cf.match(/<input[^>]*data-year-slider[^>]*>/);
  const min = slider && Number((slider[0].match(/min="(\d+)"/) || [])[1]);
  const max = slider && Number((slider[0].match(/max="(\d+)"/) || [])[1]);
  if (!crises.length || !min || !max) fail(`could not discover crises (${crises.length}) or cash-flow years (${min}-${max})`);

  // The assets the page really links, with their ?v= URLs, so this list cannot
  // go out of date. ECharts is only named in data-echarts: charts.js loads it
  // on demand, so it is not part of the cold-start batch.
  const home = http.get(BASE + "/", { tags: { name: "setup" } }).body;
  const assets = [...new Set([...home.matchAll(/\s(?:src|href)="(\/static\/[^"]+)"/g)].map((m) => decodeAmp(m[1])))];
  const echarts = decodeAmp((home.match(/\sdata-echarts="(\/static\/[^"]+)"/) || [])[1] || "");
  if (!assets.length || !echarts) fail(`could not discover static assets (${assets.length}) or the ECharts URL ("${echarts}") on the home page`);
  console.log(`cold-start assets: ${assets.join(" ")}`);
  console.log(`on-demand ECharts: ${echarts}`);
  return { crises, years: { min, max }, assets, echarts };
}

// ---------- journeys ----------

// Planner: checks the projection, flips views, looks at success odds.
function planner() {
  group("planner", () => {
    page("/", "GET /");
    think();
    page("/plan", "GET /plan");
    think();
    partial("/plan/results?view=month&real=1", "GET /plan/results");
    think();
    partial("/plan/results?view=year&real=0", "GET /plan/results");
    think();
    followDeferred(page("/success", "GET /success"));
    think(2, 5);
    partial("/success/results?reseed=1", "GET /success/results (reseed)", "simulation");
    think();
    followDeferred(page("/scenarios", "GET /scenarios"));
  });
}

// Cash flow: opens the year view and drags the slider through a few years.
function cashflow(data) {
  group("cashflow", () => {
    page("/cashflow", "GET /cashflow");
    let y = data.years.min + Math.floor(Math.random() * (data.years.max - data.years.min));
    for (let i = 0; i < 4; i++) {
      think(0.5, 2);
      y = Math.min(data.years.max, y + 1 + Math.floor(Math.random() * 5));
      partial(`/cashflow/year?y=${y}&view=year&real=1`, "GET /cashflow/year");
    }
    think();
    page("/cashflow?real=0&view=month", "GET /cashflow");
  });
}

// Backtester: historical results, then a few crises and start years.
function backtester(data) {
  group("backtester", () => {
    followDeferred(page("/backtest", "GET /backtest"));
    for (let i = 0; i < 3; i++) {
      think(2, 5);
      partial(`/backtest/scenario?crisis=${pick(data.crises)}`, "GET /backtest/scenario");
    }
    think();
    partial(`/backtest/scenario?year=${1900 + Math.floor(Math.random() * 100)}`, "GET /backtest/scenario");
  });
}

// Bookkeeper: reviews finances section by section, then the health costs and
// the net worth record. /tracking only reads saved entries and draws no chart
// on this data, so neither page needs a simulation or ECharts.
function bookkeeper() {
  group("bookkeeper", () => {
    page("/finances", "GET /finances");
    for (const kind of ["accounts", "incomes", "expenses", "debts", "milestones"]) {
      think(1, 3);
      partial(`/finances/${kind}`, "GET /finances/{kind}");
    }
    think();
    partial("/finances/summary", "GET /finances/summary");
    think();
    page("/finances/pension", "GET /finances/pension");
    think();
    page("/finances/health", "GET /finances/health");
    think();
    page("/tracking", "GET /tracking");
  });
}

const JOURNEYS = [
  { weight: 40, run: planner, charts: true },
  { weight: 20, run: cashflow, charts: true },
  { weight: 20, run: backtester, charts: true },
  { weight: 20, run: bookkeeper, charts: false },
];
const TOTAL_WEIGHT = JOURNEYS.reduce((n, j) => n + j.weight, 0);

const staticRequest = (p) => ["GET", BASE + p, null, { headers: GZIP, tags: { name: "static", kind: "static" } }];
let echartsCached = false; // per VU: ECharts is only fetched by pages with a chart

// loadStatic fetches assets together and reports whether all of them arrived.
// The checks carry their own tag: one broken asset per VU is too little to
// move the overall http_req_failed or checks rates on a long run.
function loadStatic(paths) {
  return http.batch(paths.map(staticRequest))
    .map((res, i) => check(res, { [`static ${paths[i].split("?")[0]} 200`]: (r) => r.status === 200 }, { kind: "static" }))
    .every(Boolean);
}

export default function (data) {
  // First iteration of each VU: load static assets once, like a cold cache.
  if (__ITER === 0) {
    loadStatic(data.assets);
  }
  let r = Math.random() * TOTAL_WEIGHT;
  const journey = JOURNEYS.find((j) => (r -= j.weight) < 0) || JOURNEYS[0];
  if (journey.charts && !echartsCached) {
    echartsCached = loadStatic([data.echarts]);
  }
  const start = Date.now();
  journey.run(data);
  journeyDuration.add(Date.now() - start, { journey: journey.run.name });
  journeys.add(1, { journey: journey.run.name });
  think(2, 6); // pause before the next journey
}

export function handleSummary(data) {
  const out = __ENV.SUMMARY_DIR || "perf/results";
  return {
    [`${out}/summary.json`]: JSON.stringify(data, null, 2),
    stdout: textSummary(data),
  };
}

// Compact console summary: per-endpoint latency plus threshold results.
function textSummary(data) {
  const lines = ["", "Horizonte load test", "==================="];
  const m = data.metrics;
  const fmt = (v) => (v === undefined ? "-" : `${v.toFixed(1)} ms`);
  lines.push(`requests: ${m.http_reqs.values.count} (${m.http_reqs.values.rate.toFixed(2)}/s), failed: ${(m.http_req_failed.values.rate * 100).toFixed(2)}%`);
  lines.push(`journeys: ${m.journeys ? m.journeys.values.count : 0}, checks passed: ${(m.checks.values.rate * 100).toFixed(2)}%`);
  lines.push("", "endpoint                          count       p50       p95       p99       max");
  for (const [key, metric] of Object.entries(m)) {
    const match = key.match(/^http_req_duration\{name:(.+)\}$/);
    if (!match) continue;
    const v = metric.values;
    lines.push(`${match[1].padEnd(32)}${String(v.count).padStart(7)}  ${fmt(v.med).padStart(8)}  ${fmt(v["p(95)"]).padStart(8)}  ${fmt(v["p(99)"]).padStart(8)}  ${fmt(v.max).padStart(8)}`);
  }
  lines.push("", "thresholds:");
  for (const [key, metric] of Object.entries(m)) {
    for (const [expr, t] of Object.entries(metric.thresholds || {})) {
      if (expr === PER_ENDPOINT) continue;
      lines.push(`  ${t.ok ? "PASS" : "FAIL"}  ${key} ${expr}`);
    }
  }
  return lines.join("\n") + "\n";
}
