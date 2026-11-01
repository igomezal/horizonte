// Chart rendering. Server templates emit:
//   <div class="chart" data-chart="ID"></div>
//   <script type="application/json" id="ID">{ "type": "...", ... }</script>
// and this file turns them into ECharts instances. Charts follow the page
// language (number/currency format) and the current light/dark theme.
//
// Conventions shared by every chart (see ui/DESIGN.md, "Charts"):
// - Identity colours come from the validated --series-1…7 palette, in order,
//   never cycled: anything past seven is --series-other. Green and red only
//   mean good and bad (income, savings rate, debt, money running out).
// - "Tu plan" (the fixed-return projection) is always a dashed orange line.
// - Lines are 2px; areas are washes; stacked fills are split by a thin line
//   in the surface colour instead of a border.
// - Tooltips list every series at the hovered point with a short line key,
//   the value in bold and the name in muted text.
// - Time axes show the age under each year when the plan knows it.
(function () {
  "use strict";

  // ---------- tokens and formatting ----------

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

  function percentFormatter() {
    return new Intl.NumberFormat(locale(), { style: "percent", maximumFractionDigits: 0 });
  }

  // The categorical palette, in its validated order.
  function seriesColors() {
    const out = [];
    for (let i = 1; i <= 7; i++) out.push(cssVar("--series-" + i));
    return out;
  }

  // Colour of the i-th entity (0-based). Past the palette everything is "other".
  function slotColor(i) {
    const c = seriesColors();
    return i >= 0 && i < c.length ? c[i] : cssVar("--series-other");
  }

  // Expense categories keep one colour wherever they appear ("other" is
  // gray). The assignment was searched so that categories next to each other
  // in a stack, or with one missing category between them, stay distinct.
  const CATEGORY_SLOT = { housing: 0, food: 1, transport: 4, utilities: 3, health: 5, leisure: 2, education: 6 };
  function categoryColor(key) {
    return key in CATEGORY_SLOT ? slotColor(CATEGORY_SLOT[key]) : cssVar("--series-other");
  }

  function enc(s) {
    return echarts.format.encodeHTML(String(s));
  }

  // ---------- tooltip pieces ----------

  // A short stroke of the series colour (dashed for "Tu plan").
  function lineKey(color, dashed) {
    const style = dashed
      ? "border-top:2px dashed " + color + ";height:0"
      : "background:" + color + ";height:3px;border-radius:2px";
    return '<span style="display:inline-block;width:14px;vertical-align:middle;margin-right:7px;' + style + '"></span>';
  }

  function tipHeader(text) {
    return '<div style="font-weight:600;margin-bottom:4px">' + enc(text) + "</div>";
  }

  // One row: key and muted name on the left, the value in bold on the right.
  function tipRow(color, name, value, opts) {
    opts = opts || {};
    const muted = cssVar("--chart-text");
    const strong = cssVar("--app-text");
    return '<div style="display:flex;justify-content:space-between;align-items:center;gap:1.75em;line-height:1.6">' +
      '<span style="color:' + muted + '">' + (color ? lineKey(color, opts.dashed) : "") + enc(name) + "</span>" +
      '<span style="color:' + strong + ';font-weight:' + (opts.bold === false ? 500 : 650) +
      ';font-variant-numeric:tabular-nums">' + enc(value) + "</span></div>";
  }

  function tipRule() {
    return '<div style="border-top:1px solid ' + cssVar("--chart-grid") + ';margin:4px 0"></div>';
  }

  // Header for a time axis point: "2042 · 52 años".
  function timeHeader(spec, i) {
    const age = spec.ages && spec.ages[i] ? " · " + spec.ageFormat.replace("%s", spec.ages[i]) : "";
    return tipHeader(spec.categories[i] + age);
  }

  // ---------- legend icons ----------

  // ECharts stretches path icons to the legend item box; the invisible slivers
  // at the top and bottom keep the stroke thin instead of a filled block.
  const LINE_ICON = "path://M0,0h24v.01H0zM0,5.75h24v2.5H0zM0,13.99h24v.01H0z";
  const DASH_ICON = "path://M0,0h24v.01H0zM0,5.75h7v2.5H0zM8.5,5.75h7v2.5h-7zM17,5.75h7v2.5h-7zM0,13.99h24v.01H0z";

  // ---------- base option ----------

  function baseOption() {
    const text = cssVar("--chart-text");
    const grid = cssVar("--chart-grid");
    return {
      animation: false,
      color: seriesColors(),
      textStyle: { fontFamily: getComputedStyle(document.body).fontFamily, color: text },
      grid: { left: 8, right: 16, top: 40, bottom: 8, containLabel: true },
      // Each series sets its own legendIcon: a block for areas and bars, a stroke for lines.
      legend: { top: 0, left: 0, textStyle: { color: text }, itemWidth: 18, itemHeight: 10, itemGap: 16 },
      tooltip: {
        trigger: "axis",
        confine: true,
        backgroundColor: cssVar("--chart-tooltip-bg"),
        borderColor: grid,
        padding: [8, 12],
        textStyle: { color: text, fontSize: 13 },
        extraCssText: "box-shadow:0 4px 14px rgba(15,26,43,.12);border-radius:6px;",
        axisPointer: { type: "line", lineStyle: { color: cssVar("--app-muted"), width: 1, type: "solid" } },
      },
      xAxis: {
        type: "category",
        boundaryGap: false,
        axisLine: { lineStyle: { color: grid } },
        axisTick: { show: false },
        axisLabel: { color: text, hideOverlap: true },
      },
      yAxis: {
        type: "value",
        splitLine: { lineStyle: { color: grid } },
        axisLabel: { color: text, formatter: (v) => moneyFormatter(true).format(v) },
      },
    };
  }

  // Years on the axis with the age underneath, when the plan knows it.
  function ageAxis(opt, spec) {
    if (!spec.ages || !spec.ages.length) return;
    const text = cssVar("--chart-text");
    opt.xAxis.axisLabel.formatter = (v, i) => {
      const age = spec.ages[i];
      return age ? "{y|" + v + "}\n{a|" + spec.ageFormat.replace("%s", age) + "}" : "{y|" + v + "}";
    };
    opt.xAxis.axisLabel.rich = {
      y: { color: text, fontSize: 12, lineHeight: 16 },
      a: { color: text, fontSize: 10, lineHeight: 13, opacity: 0.75 },
    };
  }

  // A solid 0 line when a chart has negative values (debts, deficits).
  function zeroLine() {
    return { yAxis: 0, lineStyle: { color: cssVar("--chart-zero"), type: "solid", width: 1.25 }, label: { show: false } };
  }

  // Label at the end of a line: its last value, in text colour.
  function endLabel(formatter, color) {
    return {
      show: true,
      formatter: formatter,
      color: color || cssVar("--app-text"),
      fontWeight: 600,
      fontSize: 12,
      distance: 6,
    };
  }

  const builders = {};

  // Stacked area (home page demo): categories on X, one series per component.
  builders["stacked-area"] = function (spec) {
    const opt = baseOption();
    const fmt = moneyFormatter(false);
    const surface = cssVar("--app-bg");
    if (spec.hideLegend) {
      opt.legend.show = false;
      opt.grid.top = 12;
    }
    opt.xAxis.data = spec.categories;
    opt.tooltip.formatter = (items) => {
      if (!items.length) return "";
      const i = items[0].dataIndex;
      const total = items.reduce((t, it) => t + (it.value || 0), 0);
      return tipHeader(spec.categories[i]) + tipRow(null, spec.totalLabel || "Total", fmt.format(total)) + tipRule() +
        items.slice().reverse().map((it) => tipRow(it.color, it.seriesName, fmt.format(it.value), { bold: false })).join("");
    };
    opt.series = spec.series.map((s, i) => ({
      name: s.name,
      type: "line",
      stack: "total",
      smooth: 0.2,
      symbol: "none",
      legendIcon: "roundRect",
      color: slotColor(i),
      lineStyle: { width: 1.5, color: surface },
      areaStyle: { opacity: 0.78 },
      emphasis: { disabled: true },
      data: s.data,
    }));
    return opt;
  };

  // Projection: stacked account balances, debt below zero, net worth as a line
  // with its final value at the end, milestone marks above the plot.
  builders.projection = function (spec, el) {
    const opt = baseOption();
    const fmt = moneyFormatter(false);
    const compact = moneyFormatter(true);
    const ink = cssVar("--app-text");
    const negative = cssVar("--app-negative");
    const surface = cssVar("--app-bg");
    const accounts = spec.accounts || [];

    opt.xAxis.data = spec.categories;
    ageAxis(opt, spec);
    opt.legend.type = "scroll";
    opt.legend.right = 0;
    opt.grid.right = 84; // room for the net worth label at the end of the line

    opt.tooltip.formatter = function (items) {
      if (!items.length) return "";
      const i = items[0].dataIndex;
      const rows = [tipRow(ink, spec.netWorth.name, fmt.format(spec.netWorth.data[i]))];
      const parts = [];
      for (let k = accounts.length - 1; k >= 0; k--) { // top of the stack first, like the chart
        const v = accounts[k].data[i];
        if (v) parts.push(tipRow(slotColor(k), accounts[k].name, fmt.format(v), { bold: false }));
      }
      if (spec.debt && spec.debt.data[i]) parts.push(tipRow(negative, spec.debt.name, fmt.format(spec.debt.data[i]), { bold: false }));
      return timeHeader(spec, i) + rows.join("") + (parts.length ? tipRule() + parts.join("") : "");
    };

    const series = accounts.map((s, i) => ({
      name: s.name,
      type: "line",
      stack: "assets",
      symbol: "none",
      legendIcon: "roundRect",
      color: slotColor(i),
      lineStyle: { width: 1.25, color: surface }, // the surface-coloured edge separates the bands
      areaStyle: { opacity: 0.72 },
      emphasis: { disabled: true },
      data: s.data,
    }));
    let hasNegative = false;
    if (spec.debt) {
      hasNegative = spec.debt.data.some((v) => v < 0);
      series.push({
        name: spec.debt.name,
        type: "line",
        stack: "debt",
        symbol: "none",
        legendIcon: "roundRect",
        color: negative,
        lineStyle: { width: 1.25, color: surface },
        areaStyle: { opacity: 0.45 },
        emphasis: { disabled: true },
        data: spec.debt.data,
      });
    }
    hasNegative = hasNegative || spec.netWorth.data.some((v) => v < 0);
    const net = {
      name: spec.netWorth.name,
      type: "line",
      symbol: "none",
      z: 5,
      legendIcon: LINE_ICON,
      lineStyle: { width: 2.25, color: ink, cap: "round", join: "round" },
      color: ink,
      emphasis: { disabled: true },
      endLabel: endLabel((p) => compact.format(p.value)),
      data: spec.netWorth.data,
    };

    // Vertical marks: milestones (ink), debts paid off (green), money runs out (red).
    // Labels sit in a band above the plot, in as many rows as needed so
    // neighbouring marks do not overlap.
    const muted = cssVar("--app-muted");
    const positive = cssVar("--app-positive");
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
    const rowGap = 15, fontSize = 11;
    const plotWidth = Math.max(200, (el ? el.clientWidth : 800) - 160);
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
        lineStyle: { color: color, type: g.kind === "debt" ? "dotted" : "dashed", width: g.kind === "fail" ? 1.5 : 1 },
        label: {
          formatter: label,
          color: g.kind === "milestone" ? ink : color,
          fontWeight: g.kind === "fail" ? 600 : 500,
          position: "end",
          distance: 4 + row * rowGap,
          fontSize: fontSize,
        },
      };
    });
    if (hasNegative) lines.push(zeroLine());
    if (lines.length) {
      net.markLine = { symbol: "none", silent: true, animation: false, data: lines };
    }
    opt.grid.top = 52 + rowEnds.length * rowGap; // legend plus one band per label row
    series.push(net);
    opt.series = series;
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
      expense: cssVar("--series-other"),
      debt: cssVar("--app-negative"),
      saving: cssVar("--app-contributed"),
    };
    const labels = {};
    spec.nodes.forEach((n) => (labels[n.name] = n.label));
    const narrow = el && el.clientWidth < 640;
    // Tiny flows get no label (they would overlap); they stay in the tooltip and ledger.
    const total = spec.links.filter((l) => l.target === "center").reduce((t, l) => t + l.value, 0) || 1;
    const share = (v) => percentFormatter().format(v / total);
    return {
      animation: false,
      textStyle: { fontFamily: getComputedStyle(document.body).fontFamily, color: text },
      tooltip: {
        trigger: "item",
        confine: true,
        backgroundColor: cssVar("--chart-tooltip-bg"),
        borderColor: cssVar("--chart-grid"),
        padding: [8, 12],
        textStyle: { color: text, fontSize: 13 },
        extraCssText: "box-shadow:0 4px 14px rgba(15,26,43,.12);border-radius:6px;",
        formatter: (p) =>
          p.dataType === "edge"
            ? tipHeader(labels[p.data.source] + " → " + labels[p.data.target]) + tipRow(null, share(p.data.value), fmt.format(p.data.value))
            : tipHeader(labels[p.data.name]) + tipRow(null, share(p.value), fmt.format(p.value)),
      },
      series: [{
        type: "sankey",
        left: narrow ? 4 : 8,
        right: narrow ? 96 : 180,
        top: 8,
        bottom: 8,
        nodeWidth: 12,
        nodeGap: 12,
        layoutIterations: 0, // keep the order the server sends
        draggable: false,
        emphasis: { focus: "adjacency" },
        data: spec.nodes.map((n) => ({
          name: n.name,
          itemStyle: { color: colors[n.kind] || text, borderWidth: 0, borderRadius: 2 },
          label: {
            color: cssVar("--app-text"),
            fontSize: narrow ? 10 : 12,
            formatter: () => "{n|" + labels[n.name] + "}\n{v|" + fmt.format(n.value) + "}",
            rich: {
              n: { color: text, fontSize: narrow ? 10 : 12, lineHeight: 16 },
              v: { color: cssVar("--app-text"), fontSize: narrow ? 11 : 13, fontWeight: 650, lineHeight: 17 },
            },
            show: n.kind !== "center" && n.value / total >= 0.025,
          },
        })),
        links: spec.links.map((l) => ({ source: l.source, target: l.target, value: l.value })),
        lineStyle: { color: "gradient", opacity: 0.3, curveness: 0.5 },
      }],
    };
  };

  // Savings rate per year: green when you save, red when you spend savings.
  // Extreme years (spending many times your income) are cut at −100 % so
  // they do not flatten every other bar: they are drawn lighter, a note says
  // so, and the tooltip gives the real value.
  builders["savings-rate"] = function (spec) {
    const opt = baseOption();
    const pct = percentFormatter();
    const raw = spec.series[0].data;
    const vals = raw.filter((v) => v != null);
    const lo = Math.max(-1, Math.min(0, ...vals));
    const hi = Math.min(1, Math.max(0, ...vals));
    const yMin = lo < 0 ? Math.floor(lo * 4) / 4 : 0;
    const yMax = hi > 0 ? Math.ceil(hi * 10) / 10 : 0.1;
    const pos = cssVar("--app-positive"), neg = cssVar("--app-negative");
    const clippedAny = raw.some((v) => v != null && (v < yMin || v > yMax));

    opt.legend.show = false;
    opt.grid.top = clippedAny && spec.clipNote ? 28 : 20;
    if (clippedAny && spec.clipNote) {
      opt.graphic = [{ type: "text", right: 16, top: 0, style: { text: spec.clipNote, fill: cssVar("--chart-text"), fontSize: 11 } }];
    }
    opt.xAxis.data = spec.categories;
    opt.xAxis.boundaryGap = true;
    opt.yAxis.min = yMin;
    opt.yAxis.max = yMax;
    const range = yMax - yMin; // clean steps that land on 0 and on the ends
    opt.yAxis.interval = range > 1 ? 0.25 : range > 0.5 ? 0.2 : 0.1;
    opt.yAxis.axisLabel.formatter = (v) => pct.format(v);
    opt.tooltip.axisPointer = { type: "shadow", shadowStyle: { color: cssVar("--chart-grid"), opacity: 0.5 } };
    opt.tooltip.formatter = (items) => {
      const i = items[0].dataIndex;
      const v = raw[i];
      return tipHeader(spec.categories[i]) +
        tipRow(v == null ? null : v < 0 ? neg : pos, spec.series[0].name, v == null ? "—" : pct.format(v));
    };
    opt.series = [{
      name: spec.series[0].name,
      type: "bar",
      barMaxWidth: 16,
      data: raw.map((v) => {
        if (v == null) return null;
        const clipped = v < yMin || v > yMax;
        const shown = Math.max(yMin, Math.min(yMax, v));
        return {
          value: shown,
          // A cut bar is lighter and loses its rounded end, so it reads as "continues".
          itemStyle: { color: v < 0 ? neg : pos, borderRadius: clipped ? 0 : v < 0 ? [0, 0, 4, 4] : [4, 4, 0, 0], opacity: clipped ? 0.55 : 1 },
        };
      }),
      markLine: { symbol: "none", silent: true, animation: false, data: lo < 0 ? [zeroLine()] : [] },
    }];
    return opt;
  };

  // Stacked bars per year: expenses by category, each category in its own
  // fixed colour, debt payments in red on top. Segments are split by a thin
  // surface-coloured gap; the top of each bar is rounded.
  builders["stacked-bars"] = function (spec) {
    const opt = baseOption();
    const fmt = moneyFormatter(false);
    const surface = cssVar("--app-bg");
    opt.xAxis.data = spec.categories;
    opt.xAxis.boundaryGap = true;
    opt.legend.type = "scroll";
    opt.tooltip.axisPointer = { type: "shadow", shadowStyle: { color: cssVar("--chart-grid"), opacity: 0.5 } };
    const colorOf = (s) => (s.kind === "debt" ? cssVar("--app-negative") : s.key ? categoryColor(s.key) : cssVar("--series-other"));
    opt.tooltip.formatter = (items) => {
      if (!items.length) return "";
      const i = items[0].dataIndex;
      const total = spec.series.reduce((t, s) => t + (s.data[i] || 0), 0);
      const rows = [];
      for (let k = spec.series.length - 1; k >= 0; k--) {
        const s = spec.series[k], v = s.data[i];
        if (v) rows.push(tipRow(colorOf(s), s.name, fmt.format(v), { bold: false }));
      }
      return tipHeader(spec.categories[i]) + tipRow(null, spec.totalLabel || "Total", fmt.format(total)) + (rows.length ? tipRule() + rows.join("") : "");
    };
    // The top non-zero segment of each bar gets the rounded end.
    const top = spec.categories.map((_, i) => {
      for (let k = spec.series.length - 1; k >= 0; k--) if (spec.series[k].data[i] > 0) return k;
      return -1;
    });
    opt.series = spec.series.map((s, k) => ({
      name: s.name, type: "bar", stack: "total", barMaxWidth: 18, legendIcon: "roundRect",
      itemStyle: { color: colorOf(s), borderColor: surface, borderWidth: 1 },
      emphasis: { focus: "series" },
      data: s.data.map((v, i) => (top[i] === k ? { value: v, itemStyle: { borderRadius: [4, 4, 0, 0] } } : v)),
    }));
    return opt;
  };

  // Monte Carlo fan: 10–90 % and 25–75 % bands of net worth (blue washes), the
  // median (ink, labelled at the end) and "Tu plan" (dashed orange) for
  // comparison. Bands are drawn by stacking a transparent base with the
  // band's height.
  builders.fan = function (spec) {
    const opt = baseOption();
    const fmt = moneyFormatter(false);
    const compact = moneyFormatter(true);
    const ink = cssVar("--app-text");
    const blue = slotColor(0);
    const plan = slotColor(1);
    const b = spec.bands;
    const diff = (hi, lo) => hi.map((v, i) => v - lo[i]);
    const L = spec.labels;
    opt.xAxis.data = spec.categories;
    ageAxis(opt, spec);
    opt.grid.right = 84;
    opt.legend.data = [L.wide, L.narrow, L.median, L.plan];
    opt.tooltip.formatter = (items) => {
      const i = items[0].dataIndex;
      return timeHeader(spec, i) +
        tipRow(ink, L.median, fmt.format(b[2][i])) +
        tipRow(plan, L.plan, fmt.format(spec.plan[i]), { dashed: true }) + tipRule() +
        tipRow(null, L.narrow, fmt.format(b[1][i]) + " – " + fmt.format(b[3][i]), { bold: false }) +
        tipRow(null, L.wide, fmt.format(b[0][i]) + " – " + fmt.format(b[4][i]), { bold: false });
    };
    const base = (stack, data) => ({
      type: "line", stack: stack, data: data, symbol: "none", silent: true,
      lineStyle: { opacity: 0 }, areaStyle: { opacity: 0 }, tooltip: { show: false },
    });
    const band = (name, stack, data, opacity) => ({
      name: name, type: "line", stack: stack, data: data, symbol: "none", color: blue, legendIcon: "roundRect",
      itemStyle: { color: blue, opacity: Math.min(1, opacity * 2.2) }, // legend swatch shows the band's tone
      lineStyle: { opacity: 0 }, areaStyle: { color: blue, opacity: opacity }, emphasis: { disabled: true },
    });
    opt.series = [
      base("wide", b[0]), band(L.wide, "wide", diff(b[4], b[0]), 0.13),
      base("narrow", b[1]), band(L.narrow, "narrow", diff(b[3], b[1]), 0.2),
      { name: L.median, type: "line", data: b[2], symbol: "none", color: ink, legendIcon: LINE_ICON,
        lineStyle: { width: 2.25, color: ink }, z: 5, endLabel: endLabel((p) => compact.format(p.value)) },
      { name: L.plan, type: "line", data: spec.plan, symbol: "none", color: plan, legendIcon: DASH_ICON,
        lineStyle: { width: 2, type: [6, 4], color: plan }, z: 4 },
    ];
    return opt;
  };

  // Share of scenarios that still have money at each age.
  builders.survival = function (spec) {
    const opt = baseOption();
    const pct = percentFormatter();
    const blue = slotColor(0);
    opt.legend.show = false;
    opt.grid.top = 16;
    opt.grid.right = 56;
    opt.xAxis.data = spec.categories;
    ageAxis(opt, spec);
    opt.yAxis.min = 0;
    opt.yAxis.max = 1;
    opt.yAxis.axisLabel.formatter = (v) => pct.format(v);
    opt.tooltip.formatter = (items) => timeHeader(spec, items[0].dataIndex) + tipRow(blue, spec.label, pct.format(items[0].value));
    opt.series = [{
      name: spec.label, type: "line", data: spec.data, symbol: "none", step: "end",
      color: blue, lineStyle: { width: 2, color: blue }, areaStyle: { color: blue, opacity: 0.1 },
      endLabel: endLabel((p) => pct.format(p.value)),
    }];
    return opt;
  };

  // Every historical start year as a cell (rows = decades, columns = year in
  // the decade): green when the money lasts (stronger = more left at the end),
  // red when it runs out (stronger = earlier). Clicking a year replays it.
  builders["start-years"] = function (spec) {
    const fmt = moneyFormatter(true);
    const text = cssVar("--chart-text");
    const pos = cssVar("--app-positive"), neg = cssVar("--app-negative");
    const surface = cssVar("--app-bg");
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
          ? { color: c.failed ? neg : pos, opacity: opacity, borderColor: cssVar("--app-muted"), borderWidth: 1, borderType: "dotted", borderRadius: 3 }
          : { color: c.failed ? neg : pos, opacity: opacity, borderColor: surface, borderWidth: 2, borderRadius: 3 },
        label: { show: true, formatter: String(c.year).slice(-2), color: cssVar("--app-text"), fontSize: 10 },
      };
    });
    return {
      animation: false,
      textStyle: { fontFamily: getComputedStyle(document.body).fontFamily, color: text },
      grid: { left: 52, right: 8, top: 8, bottom: 24 },
      tooltip: {
        confine: true,
        backgroundColor: cssVar("--chart-tooltip-bg"), borderColor: cssVar("--chart-grid"),
        padding: [8, 12], textStyle: { color: text, fontSize: 13 },
        extraCssText: "box-shadow:0 4px 14px rgba(15,26,43,.12);border-radius:6px;",
        formatter: (p) => {
          const c = p.data.cell;
          return tipHeader(c.year) + '<div style="color:' + cssVar("--app-text") + '">' +
            enc(c.failed ? spec.fail.replace("%s", c.failAge) : spec.ok.replace("%s", fmt.format(c.end))) + "</div>" +
            (c.partial ? '<div style="opacity:.75">' + enc(spec.partial.replace("%s", c.years)) + "</div>" : "");
        },
      },
      xAxis: { type: "category", data: ["0", "1", "2", "3", "4", "5", "6", "7", "8", "9"], axisLine: { show: false }, axisTick: { show: false }, axisLabel: { color: text } },
      yAxis: { type: "category", data: spec.decades, inverse: true, axisLine: { show: false }, axisTick: { show: false }, axisLabel: { color: text } },
      series: [{ type: "heatmap", data: data, emphasis: { itemStyle: { borderColor: cssVar("--app-text"), borderWidth: 2 } } }],
    };
  };

  // One replayed historical scenario against "Tu plan".
  builders.scenario = function (spec) {
    const opt = baseOption();
    const fmt = moneyFormatter(false);
    const compact = moneyFormatter(true);
    const ink = cssVar("--app-text"), plan = slotColor(1), neg = cssVar("--app-negative"), muted = cssVar("--app-muted");
    opt.xAxis.data = spec.categories;
    ageAxis(opt, spec);
    opt.grid.right = 84;
    opt.tooltip.formatter = (items) => {
      const i = items[0].dataIndex;
      return timeHeader(spec, i) + tipRow(ink, spec.labels.scenario, fmt.format(spec.scenario[i])) +
        tipRow(plan, spec.labels.plan, fmt.format(spec.plan[i]), { dashed: true });
    };
    const marks = [];
    if (spec.dataEndIndex >= 0) {
      marks.push({ xAxis: spec.categories[spec.dataEndIndex], lineStyle: { color: muted, type: "dotted" },
        label: { formatter: spec.labels.dataEnd, color: muted, position: "end", fontSize: 11 } });
    }
    if (spec.failIndex >= 0) {
      marks.push({ xAxis: spec.categories[spec.failIndex], lineStyle: { color: neg, type: "dashed", width: 1.5 },
        label: { formatter: spec.labels.fail, color: neg, position: "end", fontSize: 11, fontWeight: 600, distance: marks.length ? 18 : 4 } });
    }
    if (spec.scenario.some((v) => v < 0)) marks.push(zeroLine());
    if (marks.some((m) => m.xAxis)) opt.grid.top = 70;
    opt.series = [
      { name: spec.labels.scenario, type: "line", data: spec.scenario, symbol: "none", color: ink, legendIcon: LINE_ICON,
        lineStyle: { width: 2.25, color: ink }, areaStyle: { color: ink, opacity: 0.06 }, z: 5,
        endLabel: endLabel((p) => compact.format(p.value)),
        markLine: marks.length ? { symbol: "none", silent: true, animation: false, data: marks } : undefined },
      { name: spec.labels.plan, type: "line", data: spec.plan, symbol: "none", color: plan, legendIcon: DASH_ICON,
        lineStyle: { width: 2, type: [6, 4], color: plan } },
    ];
    return opt;
  };

  // Net worth of every scenario on one chart. Each plan keeps its colour (by
  // its place in the list); the active one is thicker. With a few plans the
  // name rides the end of each line.
  builders.scenarios = function (spec) {
    const opt = baseOption();
    const fmt = moneyFormatter(false);
    const compact = moneyFormatter(true);
    const labelled = spec.series.length <= 4;
    opt.xAxis.data = spec.categories;
    opt.legend.type = "scroll";
    if (labelled) opt.grid.right = 150;
    opt.tooltip.formatter = (items) => {
      if (!items.length) return "";
      const i = items[0].dataIndex;
      const rows = items
        .filter((it) => it.value != null)
        .sort((a, b) => b.value - a.value)
        .map((it) => tipRow(slotColor(it.seriesIndex), it.seriesName, fmt.format(it.value), { bold: spec.series[it.seriesIndex].active }));
      return tipHeader(spec.categories[i]) + rows.join("");
    };
    const short = (s) => (s.length > 18 ? s.slice(0, 17) + "…" : s);
    opt.series = spec.series.map((s, i) => ({
      name: s.name, type: "line", data: s.data, symbol: "none", connectNulls: false,
      color: slotColor(i), legendIcon: LINE_ICON,
      lineStyle: { width: s.active ? 3 : 2, color: slotColor(i), opacity: s.active ? 1 : 0.85 },
      z: s.active ? 5 : 2,
      emphasis: { focus: "series" },
      endLabel: labelled
        ? { show: true, formatter: (p) => "{n|" + short(s.name) + "}\n{v|" + compact.format(p.value) + "}", distance: 8,
            rich: { n: { color: cssVar("--chart-text"), fontSize: 11, lineHeight: 14 },
                    v: { color: cssVar("--app-text"), fontSize: 12, fontWeight: 650, lineHeight: 15 } } }
        : undefined,
    }));
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
    chart.getZr().off("click");
    // Clicking anywhere in a year's column (not only on the bar) selects it.
    chart.getZr().on("click", (e) => {
      const point = [e.offsetX, e.offsetY];
      if (!chart.containPixel("grid", point)) return;
      const idx = chart.convertFromPixel({ seriesIndex: 0 }, point)[0];
      const slider = document.querySelector("[data-year-slider]");
      if (!slider || idx == null || !spec.categories[idx]) return;
      slider.value = spec.categories[idx];
      slider.dispatchEvent(new Event("input", { bubbles: true }));
      slider.scrollIntoView({ behavior: "smooth", block: "center" });
    });
  }

  // Legend items mirror their marks: each series' legendIcon (a block by
  // default) is set on the legend's own item list, which ECharts honours.
  function applyLegendIcons(opt) {
    if (!opt.legend || opt.legend.show === false || !opt.series) return;
    const icons = {};
    opt.series.forEach((s) => {
      if (s.name) icons[s.name] = s.legendIcon || "roundRect";
    });
    const names = opt.legend.data || opt.series.filter((s) => s.name).map((s) => s.name);
    opt.legend.data = names.map((n) => (typeof n === "string" ? { name: n, icon: icons[n] || "roundRect" } : n));
  }

  // Sentry Logs helper; returns false when Sentry is not enabled, so the
  // caller can fall back to the local console.
  function logError(message, attrs) {
    const logger = window.Sentry && window.Sentry.logger;
    if (logger) logger.error(message, attrs);
    return !!logger;
  }

  function render(el) {
    const dataEl = document.getElementById(el.dataset.chart);
    if (!dataEl || typeof echarts === "undefined") return;
    let spec;
    try {
      spec = JSON.parse(dataEl.textContent);
    } catch (e) {
      // The chart silently stays empty, so log it. The data itself holds
      // financial figures and is never logged, only where it came from; the
      // SyntaxError message can quote the JSON, so it only goes to the local
      // console when Sentry is off.
      const sent = logError("chart data invalid", {
        "horizonte.chart.id": el.dataset.chart,
        "error.type": e.name,
        "horizonte.page": location.pathname,
      });
      if (!sent) console.error("chart: invalid JSON for", el.dataset.chart, e);
      return;
    }
    const build = builders[spec.type];
    if (!build) {
      const sent = logError("chart type unknown", {
        "horizonte.chart.id": el.dataset.chart,
        "horizonte.chart.type": String(spec.type),
        "horizonte.page": location.pathname,
      });
      if (!sent) console.error("chart: unknown type", spec.type);
      return;
    }
    const t0 = performance.now();
    const chart = echarts.getInstanceByDom(el) || echarts.init(el, null, { renderer: "svg" });
    const opt = build(spec, el);
    applyLegendIcons(opt);
    chart.setOption(opt, true);
    wireYearSelect(chart, spec);
    // Charts re-render on every swap, theme change and resize, on the main
    // thread. Per-type timing shows which chart makes the UI feel slow
    // (e.g. a 60-year sankey or heatmap on a low-end laptop).
    const metrics = window.Sentry && window.Sentry.metrics;
    if (metrics) {
      metrics.distribution("horizonte.chart.render.duration", performance.now() - t0, {
        unit: "millisecond",
        attributes: { "horizonte.chart.type": String(spec.type), "horizonte.page": location.pathname },
      });
    }
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
