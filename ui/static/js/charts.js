// Chart rendering. Server templates emit:
//   <div class="chart" data-chart="ID"></div>
//   <script type="application/json" id="ID">{ "type": "...", ... }</script>
// and this file turns them into ECharts instances. Charts follow the page
// language (number/currency format) and the current light/dark theme.
(function () {
  "use strict";

  function cssVar(name) {
    return getComputedStyle(document.documentElement).getPropertyValue(name).trim();
  }

  function locale() {
    return document.documentElement.lang === "en" ? "en-IE" : "es-ES";
  }

  function moneyFormatter(compact) {
    return new Intl.NumberFormat(locale(), {
      style: "currency",
      currency: "EUR",
      maximumFractionDigits: compact ? 1 : 0,
      notation: compact ? "compact" : "standard",
      // es-ES skips the separator for 4-digit amounts; the app always groups (6.578 €).
      useGrouping: compact ? "auto" : "always",
    });
  }

  function palette() {
    return [cssVar("--chart-1"), cssVar("--chart-2"), cssVar("--chart-3"), cssVar("--chart-4"), cssVar("--chart-5")];
  }

  function baseOption() {
    const text = cssVar("--chart-text");
    const grid = cssVar("--chart-grid");
    return {
      color: palette(),
      textStyle: { fontFamily: getComputedStyle(document.body).fontFamily, color: text },
      grid: { left: 8, right: 16, top: 40, bottom: 8, containLabel: true },
      legend: { top: 0, left: 0, textStyle: { color: text }, icon: "roundRect" },
      tooltip: {
        trigger: "axis",
        backgroundColor: cssVar("--chart-tooltip-bg"),
        borderColor: grid,
        textStyle: { color: text },
      },
      xAxis: {
        type: "category",
        boundaryGap: false,
        axisLine: { lineStyle: { color: grid } },
        axisTick: { show: false },
        axisLabel: { color: text },
      },
      yAxis: {
        type: "value",
        splitLine: { lineStyle: { color: grid } },
        axisLabel: { color: text, formatter: (v) => moneyFormatter(true).format(v) },
      },
    };
  }

  const builders = {
    // Stacked area: categories on X, one series per component.
    "stacked-area": function (spec) {
      const opt = baseOption();
      const fmt = moneyFormatter(false);
      if (spec.hideLegend) {
        opt.legend.show = false;
        opt.grid.top = 12;
      }
      opt.xAxis.data = spec.categories;
      opt.tooltip.valueFormatter = (v) => fmt.format(v);
      opt.series = spec.series.map((s) => ({
        name: s.name,
        type: "line",
        stack: "total",
        smooth: 0.2,
        symbol: "none",
        lineStyle: { width: 1.5 },
        areaStyle: { opacity: 0.85 },
        emphasis: { disabled: true },
        data: s.data,
      }));
      return opt;
    },
  };

  // Projection: stacked account balances, debt below zero, net worth as a line.
  // Accounts cycle through the non-signal banknote colors (red is reserved for debt).
  builders.projection = function (spec, el) {
    const opt = baseOption();
    const fmt = moneyFormatter(false);
    const accountColors = [cssVar("--chart-1"), cssVar("--chart-2"), cssVar("--chart-3"), cssVar("--chart-5")];
    const text = cssVar("--app-text");
    const negative = cssVar("--app-negative");

    opt.xAxis.data = spec.categories;
    opt.xAxis.axisLabel.hideOverlap = true;
    opt.legend.type = "scroll";
    opt.tooltip.valueFormatter = (v) => fmt.format(v);
    opt.tooltip.formatter = function (items) {
      if (!items.length) return "";
      const i = items[0].dataIndex;
      const age = spec.ages && spec.ages[i] ? " · " + spec.ageFormat.replace("%s", spec.ages[i]) : "";
      const lines = items
        .filter((it) => it.value !== 0)
        .map((it) => it.marker + " " + echarts.format.encodeHTML(it.seriesName) +
          '<span style="float:right;margin-left:1.5em;font-weight:600">' + fmt.format(it.value) + "</span>");
      return "<strong>" + echarts.format.encodeHTML(spec.categories[i]) + age + "</strong><br>" + lines.join("<br>");
    };

    const series = (spec.accounts || []).map((s, i) => ({
      name: s.name,
      type: "line",
      stack: "assets",
      symbol: "none",
      lineStyle: { width: 1 },
      areaStyle: { opacity: i < accountColors.length ? 0.8 : 0.45 },
      color: accountColors[i % accountColors.length],
      emphasis: { disabled: true },
      data: s.data,
    }));
    if (spec.debt) {
      series.push({
        name: spec.debt.name,
        type: "line",
        stack: "debt",
        symbol: "none",
        lineStyle: { width: 1 },
        areaStyle: { opacity: 0.5 },
        color: negative,
        emphasis: { disabled: true },
        data: spec.debt.data,
      });
    }
    const net = {
      name: spec.netWorth.name,
      type: "line",
      symbol: "none",
      z: 5,
      lineStyle: { width: 2.5, color: text },
      color: text,
      emphasis: { disabled: true },
      data: spec.netWorth.data,
    };
    // Vertical marks: milestones (ink), debts paid off (green), money runs out (red).
    // Labels sit in a band above the plot, alternating between two rows so
    // neighbouring marks do not overlap.
    const muted = cssVar("--app-muted");
    const positive = cssVar("--app-positive");

    // Merge marks that fall in the same period into one label.
    const byIndex = new Map();
    (spec.marks || []).forEach((m) => {
      const g = byIndex.get(m.index) || { index: m.index, labels: [], kind: "debt" };
      g.labels.push(m.label);
      if (m.kind !== "debt") g.kind = "milestone";
      byIndex.set(m.index, g);
    });
    if (spec.failIndex >= 0) {
      const g = byIndex.get(spec.failIndex) || { index: spec.failIndex, labels: [], kind: "fail" };
      g.labels.push(spec.failLabel);
      g.kind = "fail";
      byIndex.set(spec.failIndex, g);
    }
    const groups = [...byIndex.values()].sort((a, b) => a.index - b.index);

    // Place each label in the first row where it does not collide with the
    // previous label in that row (centred on its line, widths estimated from
    // the font size).
    const rowGap = 15, fontSize = 11;
    const plotWidth = Math.max(200, (el ? el.clientWidth : 800) - 90);
    const step = plotWidth / Math.max(1, spec.categories.length - 1);
    const rowEnds = [];
    const lines = groups.map((g) => {
      const label = g.labels.join(", ");
      const width = label.length * fontSize * 0.56 + 8;
      const x = g.index * step;
      let row = rowEnds.findIndex((end) => x - width / 2 > end);
      if (row < 0) row = rowEnds.length;
      rowEnds[row] = x + width / 2;
      const color = g.kind === "fail" ? negative : g.kind === "debt" ? positive : muted;
      return {
        xAxis: spec.categories[g.index],
        lineStyle: { color: color, type: g.kind === "debt" ? "dotted" : "dashed", width: g.kind === "fail" ? 1.5 : 1.25 },
        label: {
          formatter: label,
          color: g.kind === "milestone" ? text : color,
          fontWeight: g.kind === "fail" ? 600 : 400,
          position: "end",
          distance: 4 + row * rowGap,
          fontSize: fontSize,
        },
      };
    });
    if (lines.length) {
      net.markLine = { symbol: "none", silent: true, animation: false, data: lines };
      opt.grid.top = 52 + rowEnds.length * rowGap; // legend plus one band per label row
    }
    series.push(net);
    opt.series = series;
    opt.animation = false;
    return opt;
  };

  // Cash flow for one year: money in (left) → "your money" → money out (right).
  // Colours keep their meaning: green income, ochre savings drawn, red gaps
  // and debts, blue money going into accounts, muted tax and expenses.
  builders.sankey = function (spec, el) {
    const fmt = moneyFormatter(false);
    const text = cssVar("--chart-text");
    const colors = {
      income: cssVar("--app-positive"),
      savings: cssVar("--chart-5"),
      shortfall: cssVar("--app-negative"),
      center: cssVar("--app-text"),
      tax: cssVar("--app-muted"),
      expense: cssVar("--chart-text"),
      debt: cssVar("--app-negative"),
      saving: cssVar("--app-contributed"),
    };
    const labels = {};
    spec.nodes.forEach((n) => (labels[n.name] = n.label));
    const narrow = el && el.clientWidth < 640;
    // Tiny flows get no label (they would overlap); they stay in the tooltip and ledger.
    const total = spec.links.filter((l) => l.target === "center").reduce((t, l) => t + l.value, 0) || 1;
    return {
      textStyle: { fontFamily: getComputedStyle(document.body).fontFamily, color: text },
      tooltip: {
        trigger: "item",
        backgroundColor: cssVar("--chart-tooltip-bg"),
        borderColor: cssVar("--chart-grid"),
        textStyle: { color: text },
        formatter: (p) =>
          p.dataType === "edge"
            ? echarts.format.encodeHTML(labels[p.data.source] + " → " + labels[p.data.target]) + "<br><strong>" + fmt.format(p.data.value) + "</strong>"
            : echarts.format.encodeHTML(labels[p.data.name]) + "<br><strong>" + fmt.format(p.value) + "</strong>",
      },
      series: [{
        type: "sankey",
        left: narrow ? 4 : 8,
        right: narrow ? 90 : 170,
        top: 8,
        bottom: 8,
        nodeWidth: 12,
        nodeGap: 10,
        layoutIterations: 0, // keep the order the server sends
        draggable: false,
        emphasis: { focus: "adjacency" },
        data: spec.nodes.map((n) => ({
          name: n.name,
          itemStyle: { color: colors[n.kind] || text, borderWidth: 0 },
          label: {
            color: text,
            fontSize: narrow ? 10 : 12,
            formatter: () => labels[n.name] + "\n" + fmt.format(n.value),
            position: n.kind === "center" ? "inside" : undefined,
            show: n.kind !== "center" && n.value / total >= 0.025,
          },
        })),
        links: spec.links.map((l) => ({ source: l.source, target: l.target, value: l.value })),
        lineStyle: { color: "gradient", opacity: 0.32, curveness: 0.5 },
        label: { color: text },
      }],
    };
  };

  // Savings rate per year: green when you save, red when you spend savings.
  builders["savings-rate"] = function (spec) {
    const opt = baseOption();
    const pct = new Intl.NumberFormat(locale(), { style: "percent", maximumFractionDigits: 0 });
    opt.legend.show = false;
    opt.grid.top = 16;
    opt.xAxis.data = spec.categories;
    opt.xAxis.boundaryGap = true;
    opt.xAxis.axisLabel.hideOverlap = true;
    opt.yAxis.axisLabel.formatter = (v) => pct.format(v);
    opt.tooltip.valueFormatter = (v) => (v == null ? "—" : pct.format(v));
    const pos = cssVar("--app-positive"), neg = cssVar("--app-negative");
    opt.series = [{
      name: spec.series[0].name,
      type: "bar",
      barMaxWidth: 14,
      data: spec.series[0].data.map((v) => (v == null ? null : { value: v, itemStyle: { color: v < 0 ? neg : pos } })),
    }];
    return opt;
  };

  // Stacked bars per year (expenses by category; debts in red).
  builders["stacked-bars"] = function (spec) {
    const opt = baseOption();
    const fmt = moneyFormatter(false);
    const palette = [cssVar("--chart-1"), cssVar("--chart-2"), cssVar("--chart-5"), cssVar("--app-muted")];
    opt.xAxis.data = spec.categories;
    opt.xAxis.boundaryGap = true;
    opt.xAxis.axisLabel.hideOverlap = true;
    opt.legend.type = "scroll";
    opt.tooltip.valueFormatter = (v) => fmt.format(v);
    let k = 0;
    opt.series = spec.series.map((s) => {
      const isDebt = s.kind === "debt";
      const color = isDebt ? cssVar("--app-negative") : palette[k % palette.length];
      const opacity = isDebt ? 0.85 : k < palette.length ? 0.9 : 0.55;
      if (!isDebt) k++;
      return {
        name: s.name, type: "bar", stack: "total", barMaxWidth: 14,
        itemStyle: { color: color, opacity: opacity },
        emphasis: { focus: "series" },
        data: s.data,
      };
    });
    return opt;
  };

  function ageHeader(spec, i) {
    const age = spec.ages && spec.ages[i] ? " · " + spec.ageFormat.replace("%s", spec.ages[i]) : "";
    return "<strong>" + echarts.format.encodeHTML(spec.categories[i] + age) + "</strong>";
  }

  // Monte Carlo fan: 10–90 % and 25–75 % bands of net worth, the median, and
  // the deterministic plan for comparison. Bands are drawn by stacking a
  // transparent base with the band's height.
  builders.fan = function (spec) {
    const opt = baseOption();
    const fmt = moneyFormatter(false);
    const ink = cssVar("--app-text");
    const accent = cssVar("--app-accent");
    const b = spec.bands;
    const diff = (hi, lo) => hi.map((v, i) => v - lo[i]);
    const L = spec.labels;
    opt.xAxis.data = spec.categories;
    opt.xAxis.axisLabel.hideOverlap = true;
    opt.legend.data = [L.wide, L.narrow, L.median, L.plan];
    opt.tooltip.formatter = (items) => {
      const i = items[0].dataIndex;
      const row = (label, v) => label + '<span style="float:right;margin-left:1.5em;font-weight:600">' + fmt.format(v) + "</span>";
      // Same words as the legend: each band as a range, then the median and the plan.
      const range = (label, lo, hi) => label + '<span style="float:right;margin-left:1.5em;font-weight:600">' +
        fmt.format(lo) + " – " + fmt.format(hi) + "</span>";
      return ageHeader(spec, i) + "<br>" + [
        range(L.wide, b[0][i], b[4][i]), range(L.narrow, b[1][i], b[3][i]),
        row(L.median, b[2][i]), row(L.plan, spec.plan[i]),
      ].join("<br>");
    };
    const base = (stack, data) => ({
      type: "line", stack: stack, data: data, symbol: "none", silent: true,
      lineStyle: { opacity: 0 }, areaStyle: { opacity: 0 }, tooltip: { show: false },
    });
    const band = (name, stack, data, opacity) => ({
      name: name, type: "line", stack: stack, data: data, symbol: "none", color: ink,
      itemStyle: { color: ink, opacity: opacity * 2.5 }, // legend swatch shows the band's tone
      lineStyle: { opacity: 0 }, areaStyle: { color: ink, opacity: opacity }, emphasis: { disabled: true },
    });
    opt.series = [
      base("wide", b[0]), band(L.wide, "wide", diff(b[4], b[0]), 0.12),
      base("narrow", b[1]), band(L.narrow, "narrow", diff(b[3], b[1]), 0.18),
      { name: L.median, type: "line", data: b[2], symbol: "none", color: ink, lineStyle: { width: 2.5, color: ink }, z: 5 },
      { name: L.plan, type: "line", data: spec.plan, symbol: "none", color: accent, lineStyle: { width: 1.5, type: "dashed", color: accent }, z: 4 },
    ];
    opt.animation = false;
    return opt;
  };

  // Share of scenarios that still have money at each age.
  builders.survival = function (spec) {
    const opt = baseOption();
    const pct = new Intl.NumberFormat(locale(), { style: "percent", maximumFractionDigits: 0 });
    const ink = cssVar("--app-text");
    opt.legend.show = false;
    opt.grid.top = 16;
    opt.xAxis.data = spec.categories;
    opt.xAxis.axisLabel.hideOverlap = true;
    opt.yAxis.min = 0;
    opt.yAxis.max = 1;
    opt.yAxis.axisLabel.formatter = (v) => pct.format(v);
    opt.tooltip.formatter = (items) => ageHeader(spec, items[0].dataIndex) + "<br>" +
      spec.label + ': <strong>' + pct.format(items[0].value) + "</strong>";
    opt.series = [{
      name: spec.label, type: "line", data: spec.data, symbol: "none", step: "end",
      color: ink, lineStyle: { width: 2, color: ink }, areaStyle: { color: ink, opacity: 0.1 },
    }];
    opt.animation = false;
    return opt;
  };

  // Every historical start year as a cell (rows = decades, columns = year in
  // the decade): green when the money lasts (stronger = more left at the end),
  // red when it runs out (stronger = earlier). Clicking a year replays it.
  builders["start-years"] = function (spec) {
    const fmt = moneyFormatter(true);
    const text = cssVar("--chart-text");
    const pos = cssVar("--app-positive"), neg = cssVar("--app-negative");
    const ends = spec.cells.filter((c) => !c.failed).map((c) => c.end);
    const maxEnd = Math.max(1, ...ends);
    const ages = spec.cells.filter((c) => c.failed).map((c) => c.failAge);
    const minAge = Math.min(...ages), maxAge = Math.max(...ages);
    const data = spec.cells.map((c) => {
      let opacity;
      if (c.failed) {
        const span = Math.max(1, maxAge - minAge);
        opacity = 0.45 + 0.55 * (1 - (c.failAge - minAge) / span);
      } else {
        opacity = 0.3 + 0.7 * Math.max(0, c.end) / maxEnd;
      }
      if (c.partial) opacity *= 0.5;
      return {
        value: [c.col, c.row, c.year], cell: c,
        itemStyle: c.partial
          ? { color: c.failed ? neg : pos, opacity: opacity, borderColor: cssVar("--app-muted"), borderWidth: 1, borderType: "dotted" }
          : { color: c.failed ? neg : pos, opacity: opacity, borderColor: cssVar("--app-bg"), borderWidth: 2 },
        label: { show: true, formatter: String(c.year).slice(-2), color: cssVar("--app-text"), fontSize: 10 },
      };
    });
    return {
      textStyle: { fontFamily: getComputedStyle(document.body).fontFamily, color: text },
      grid: { left: 52, right: 8, top: 8, bottom: 24 },
      tooltip: {
        backgroundColor: cssVar("--chart-tooltip-bg"), borderColor: cssVar("--chart-grid"), textStyle: { color: text },
        formatter: (p) => {
          const c = p.data.cell;
          return "<strong>" + c.year + "</strong><br>" + (c.failed ? spec.fail.replace("%s", c.failAge) : spec.ok.replace("%s", fmt.format(c.end))) +
            (c.partial ? '<br><span style="opacity:.75">' + echarts.format.encodeHTML(spec.partial.replace("%s", c.years)) + "</span>" : "");
        },
      },
      xAxis: { type: "category", data: ["0", "1", "2", "3", "4", "5", "6", "7", "8", "9"], axisLine: { show: false }, axisTick: { show: false }, axisLabel: { color: text } },
      yAxis: { type: "category", data: spec.decades, inverse: true, axisLine: { show: false }, axisTick: { show: false }, axisLabel: { color: text } },
      series: [{ type: "heatmap", data: data, emphasis: { itemStyle: { borderColor: cssVar("--app-accent"), borderWidth: 2 } } }],
    };
  };

  // One replayed historical scenario against the fixed-return plan.
  builders.scenario = function (spec) {
    const opt = baseOption();
    const fmt = moneyFormatter(false);
    const ink = cssVar("--app-text"), accent = cssVar("--app-accent"), neg = cssVar("--app-negative"), muted = cssVar("--app-muted");
    opt.xAxis.data = spec.categories;
    opt.xAxis.axisLabel.hideOverlap = true;
    opt.tooltip.formatter = (items) => ageHeader(spec, items[0].dataIndex) + "<br>" + items.map((it) =>
      it.marker + " " + echarts.format.encodeHTML(it.seriesName) + '<span style="float:right;margin-left:1.5em;font-weight:600">' + fmt.format(it.value) + "</span>").join("<br>");
    const marks = [];
    if (spec.dataEndIndex >= 0) {
      marks.push({ xAxis: spec.categories[spec.dataEndIndex], lineStyle: { color: muted, type: "dotted" },
        label: { formatter: spec.labels.dataEnd, color: muted, position: "end", fontSize: 11 } });
    }
    if (spec.failIndex >= 0) {
      marks.push({ xAxis: spec.categories[spec.failIndex], lineStyle: { color: neg, type: "dashed", width: 1.5 },
        label: { formatter: spec.labels.fail, color: neg, position: "end", fontSize: 11, fontWeight: 600, distance: marks.length ? 18 : 4 } });
    }
    if (marks.length) opt.grid.top = 70;
    opt.series = [
      { name: spec.labels.scenario, type: "line", data: spec.scenario, symbol: "none", color: ink,
        lineStyle: { width: 2.5, color: ink }, areaStyle: { color: ink, opacity: 0.08 },
        markLine: marks.length ? { symbol: "none", silent: true, animation: false, data: marks } : undefined },
      { name: spec.labels.plan, type: "line", data: spec.plan, symbol: "none", color: accent, lineStyle: { width: 1.5, type: "dashed", color: accent } },
    ];
    opt.animation = false;
    return opt;
  };

  // Charts that select a year: clicking a bar moves the year picker.
  function wireYearSelect(chart, spec) {
    chart.off("click");
    if (spec.type === "start-years" && spec.url) {
      // Clicking a start year replays it in the scenario section.
      chart.on("click", (p) => {
        if (!p.data || !p.data.cell) return;
        htmx.ajax("GET", spec.url + p.data.cell.year, { target: "#bt-scenario", swap: "outerHTML" }).then(() => {
          const el = document.getElementById("bt-scenario");
          if (el) el.scrollIntoView({ behavior: "smooth", block: "start" });
        });
      });
      return;
    }
    if (!spec.selectYear) return;
    chart.on("click", (p) => {
      const slider = document.querySelector("[data-year-slider]");
      if (!slider || p.dataIndex == null) return;
      slider.value = spec.categories[p.dataIndex];
      slider.dispatchEvent(new Event("input", { bubbles: true }));
      slider.scrollIntoView({ behavior: "smooth", block: "center" });
    });
  }

  function render(el) {
    const dataEl = document.getElementById(el.dataset.chart);
    if (!dataEl || typeof echarts === "undefined") return;
    let spec;
    try {
      spec = JSON.parse(dataEl.textContent);
    } catch (e) {
      console.error("chart: invalid JSON for", el.dataset.chart, e);
      return;
    }
    const build = builders[spec.type];
    if (!build) {
      console.error("chart: unknown type", spec.type);
      return;
    }
    const chart = echarts.getInstanceByDom(el) || echarts.init(el, null, { renderer: "svg" });
    chart.setOption(build(spec, el), true);
    wireYearSelect(chart, spec);
  }

  function renderAll(root) {
    (root || document).querySelectorAll("[data-chart]").forEach(render);
  }

  window.Charts = { renderAll: renderAll };

  document.addEventListener("DOMContentLoaded", () => renderAll());
  // htmx swaps (boosted navigation or partial updates).
  document.addEventListener("htmx:load", (e) => renderAll(e.target));
  // Re-theme on light/dark changes.
  document.addEventListener("themechange", () => renderAll());
  window.matchMedia("(prefers-color-scheme: dark)").addEventListener("change", () => renderAll());

  let resizeTimer;
  window.addEventListener("resize", () => {
    clearTimeout(resizeTimer);
    resizeTimer = setTimeout(() => {
      document.querySelectorAll("[data-chart]").forEach((el) => {
        const c = echarts.getInstanceByDom(el);
        if (c) c.resize();
      });
    }, 100);
  });
})();
