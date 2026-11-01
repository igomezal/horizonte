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
  builders.projection = function (spec) {
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
    if (spec.failIndex >= 0) {
      net.markLine = {
        symbol: "none",
        silent: true,
        lineStyle: { color: negative, type: "dashed", width: 1.5 },
        label: { formatter: spec.failLabel, color: negative, position: "insideEndTop" },
        data: [{ xAxis: spec.categories[spec.failIndex] }],
      };
    }
    series.push(net);
    opt.series = series;
    opt.animation = false;
    return opt;
  };

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
    chart.setOption(build(spec), true);
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
