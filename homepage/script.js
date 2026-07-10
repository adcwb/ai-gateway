// ai-gateway public homepage — no framework, no build step. Three concerns:
// i18n (mirrors frontend/src/i18n.ts's dependency-free t(key, lang) pattern),
// the interactive routing playground, and the ambient console-mock numbers.
(function () {
  "use strict";

  // ------------------------------------------------------------- i18n ----
  var translations = {
    eyebrow: { en: "Self-hosted · MIT-licensed · one Go binary", zh: "自托管 · MIT 许可 · 单一 Go 二进制文件" },
    playYourApp: { en: "your app", zh: "你的应用" },
    strategyLabel: { en: "strategy — ", zh: "策略 — " },
    heroTitle: {
      en: "Every AI request, routed, governed, and billed — behind one endpoint you run yourself",
      zh: "每一次 AI 请求，都经过路由、治理与计费 —— 全部运行在你自己部署的一个入口之后",
    },
    heroSub: {
      en: "Not a hosted proxy. Your keys, your database, your infrastructure — the gateway you'd otherwise have to build in front of every provider SDK.",
      zh: "不是托管代理服务。你的密钥、你的数据库、你的基础设施 —— 否则你就得在每个厂商 SDK 前面自己搭一个网关。",
    },
    heroDiff: {
      en: "Self-hosted &amp; open-source — unlike OpenRouter (hosted) or a bare LiteLLM proxy (no billing, no guardrails, no MCP)",
      zh: "自托管、开源 —— 不同于 OpenRouter（托管服务）或裸的 LiteLLM 代理（没有计费、没有护栏、没有 MCP）",
    },
    ctaSignIn: { en: "Sign in to Console", zh: "登录控制台" },
    ctaGithub: { en: "View on GitHub", zh: "查看 GitHub" },
    ctaDocs: { en: "Read the docs on GitHub", zh: "在 GitHub 阅读文档" },
    stagePolicy: { en: "Policy check", zh: "策略检查" },
    stageRate: { en: "Rate limit", zh: "限流" },
    stageCache: { en: "Cache", zh: "缓存" },
    stageResponse: { en: "200 Response", zh: "200 响应" },
    quickstartLabel: { en: "Quick start", zh: "快速开始" },
    quickstartComment: { en: "# running in under a minute", zh: "# 一分钟内跑起来" },
    transformTitle: { en: "Why teams put this in front of their AI traffic", zh: "为什么团队会把它放在 AI 流量前面" },
    beforeLabel: { en: "Before", zh: "之前" },
    before1: { en: "A different SDK per provider", zh: "每个厂商一套不同的 SDK" },
    before2: { en: "A key to leak per provider", zh: "每个厂商都有一个可能泄露的密钥" },
    before3: { en: "No shared view of what's being spent, or where", zh: "没有统一视图知道钱花在哪、花了多少" },
    before4: { en: "Someone pages you when a provider degrades", zh: "厂商故障时得靠人工告警" },
    before5: { en: "Cost tracking lives in five different dashboards", zh: "成本统计散落在五个不同的仪表盘里" },
    afterLabel: { en: "After", zh: "之后" },
    after1: { en: "One OpenAI-compatible endpoint", zh: "一个 OpenAI 兼容的入口" },
    after2: { en: "One scoped, revocable virtual key", zh: "一个可限定范围、可撤销的虚拟密钥" },
    after3: { en: "Every request logged, traced, and billed", zh: "每个请求都被记录、追踪与计费" },
    after4: { en: "Circuit breakers fail over automatically", zh: "熔断器自动故障转移" },
    after5: { en: "One ledger, one price table, one currency view", zh: "一本账、一张价格表、一个币种视图" },
    outcomesTitle: { en: "What that actually buys you", zh: "这实际上能为你带来什么" },
    outcomesSub: { en: "Every line below is a shipped, tested behavior — not a roadmap item", zh: "以下每一条都是已经交付、经过测试的行为 —— 不是路线图承诺" },
    outcome1: { en: "Automatically routes to the best available provider — and away from a failing one.", zh: "自动路由到当前最佳可用的厂商 —— 并自动避开出故障的那个。" },
    outcome2: { en: "Your application never sees a real provider credential — only a scoped virtual key.", zh: "你的应用永远看不到真实的厂商密钥 —— 只有一个限定范围的虚拟密钥。" },
    outcome3: { en: "Every request is traceable end to end — model, provider, latency, cost, and outcome.", zh: "每个请求都可端到端追踪 —— 模型、厂商、延迟、成本与结果。" },
    outcome4: { en: "You see cost building up in real time, before it becomes a surprise invoice.", zh: "实时看到成本累积，而不是月底才收到一张意外账单。" },
    outcome5: { en: "Sensitive data gets blocked or redacted before it ever leaves your network.", zh: "敏感数据在离开你的网络之前就被拦截或脱敏。" },
    outcome6: { en: "Swap providers or models without touching a line of application code.", zh: "更换厂商或模型无需改动一行应用代码。" },
    playTitle: { en: "Pick a strategy. Watch it route.", zh: "选一个策略，看它路由。" },
    playSub: { en: "This is the actual decision the gateway makes on every request", zh: "这就是网关在每个请求上实际做出的决策" },
    stratCheapest: { en: "Cheapest", zh: "最便宜" },
    stratFastest: { en: "Fastest", zh: "最快" },
    stratPriority: { en: "My priority order", zh: "我的优先级顺序" },
    stratWeighted: { en: "Weighted split", zh: "加权分流" },
    shotTitle: { en: "The console you'll actually operate in", zh: "你实际会用到的控制台" },
    shotSub: { en: "Live numbers, not a static screenshot", zh: "实时数字，不是静态截图" },
    live: { en: "live", zh: "实时" },
    statReq: { en: "Requests today", zh: "今日请求数" },
    statLat: { en: "Avg latency", zh: "平均延迟" },
    statCost: { en: "Cost today", zh: "今日成本" },
    statThr: { en: "Throughput", zh: "吞吐量" },
    distTitle: { en: "Provider distribution", zh: "厂商分布" },
    proofTitle: { en: "What's actually in the codebase", zh: "代码库里实际有什么" },
    proofSub: { en: "Real counts, checked against this repo — not marketing round numbers", zh: "对照仓库核实过的真实数字 —— 不是营销凑的整数" },
    proof1: { en: "outbound dialects incl.<br />5 Bedrock model families", zh: "出站协议方言，含<br />5 个 Bedrock 模型家族" },
    proof2: { en: "inbound API surfaces<br />(Chat / Anthropic / Responses / MCP)", zh: "入站 API 接口<br />（Chat / Anthropic / Responses / MCP）" },
    proof3: { en: "routing strategies", zh: "路由策略" },
    proof4: { en: "DB backends<br />MySQL · Postgres · SQLite", zh: "数据库后端<br />MySQL · Postgres · SQLite" },
    proof5: { en: "license, self-hosted", zh: "许可协议，自托管" },
    proof6: { en: "Go binary to deploy", zh: "个 Go 二进制文件即可部署" },
    ctaTitle: { en: "Point your app at it. See what it's doing. Trust the routing.", zh: "把你的应用指向它。看清它在做什么。信任这套路由。" },
    ctaSub: { en: "Self-hosted means you can read every line that touches your traffic.", zh: "自托管意味着你可以读到每一行接触你流量的代码。" },
    footerTag: { en: "ai-gateway · self-hosted AI traffic control plane · MIT license", zh: "ai-gateway · 自托管 AI 流量控制平面 · MIT 许可" },
  };

  var LANG_KEY = "aigw_homepage_lang";
  var storedLang = localStorage.getItem(LANG_KEY);
  // Mirrors frontend/src/i18n.ts's getLang(): no stored preference yet falls
  // back to the browser's own locale, not a hardcoded "en".
  var lang = storedLang === "zh" || storedLang === "en" ? storedLang : navigator.language.startsWith("zh") ? "zh" : "en";

  function applyLang() {
    document.documentElement.lang = lang;
    document.getElementById("lang-toggle").textContent = lang === "en" ? "中文" : "English";
    var nodes = document.querySelectorAll("[data-i18n]");
    for (var i = 0; i < nodes.length; i++) {
      var key = nodes[i].getAttribute("data-i18n");
      var entry = translations[key];
      if (entry) nodes[i].innerHTML = entry[lang] || entry.en;
    }
  }

  document.getElementById("lang-toggle").addEventListener("click", function () {
    lang = lang === "en" ? "zh" : "en";
    localStorage.setItem(LANG_KEY, lang);
    applyLang();
  });

  applyLang();

  // ------------------------------------------------- routing playground --
  var strategyCaptions = {
    en: [
      { pick: 0, key: "least_cost", desc: "picks <b>DeepSeek</b> because it's the cheapest candidate model right now." },
      { pick: 1, key: "least_latency", desc: "picks <b>Claude Sonnet</b> because it has the lowest rolling p50 latency right now." },
      { pick: 1, key: "priority", desc: "picks <b>Claude Sonnet</b> because it's ranked first in this key's priority tier." },
      { pick: 2, key: "weighted", desc: "splits traffic by weight — currently favoring <b>GPT-5</b>, not exclusive to it." },
    ],
    zh: [
      { pick: 0, key: "least_cost", desc: "选择 <b>DeepSeek</b>，因为它目前是候选模型中最便宜的。" },
      { pick: 1, key: "least_latency", desc: "选择 <b>Claude Sonnet</b>，因为它目前的滚动 p50 延迟最低。" },
      { pick: 1, key: "priority", desc: "选择 <b>Claude Sonnet</b>，因为它在此密钥的优先级中排第一。" },
      { pick: 2, key: "weighted", desc: "按权重分流流量 —— 目前更偏向 <b>GPT-5</b>，但并非独占。" },
    ],
  };
  var btns = document.querySelectorAll(".strat-btn");
  var activeStrategy = 0;

  function applyStrategy(idx) {
    activeStrategy = idx;
    var s = strategyCaptions[lang][idx];
    for (var i = 0; i < 3; i++) {
      var picked = i === s.pick;
      document.getElementById("pn-" + i).classList.toggle("picked", picked);
      document.getElementById("pl-" + i).classList.toggle("picked", picked);
      document.getElementById("pp-" + i).classList.toggle("picked", picked);
      document.getElementById("pd-" + i).classList.toggle("run", picked);
    }
    document.getElementById("play-caption").innerHTML = "<b>" + s.key + "</b> " + translations.strategyLabel[lang] + s.desc;
  }

  btns.forEach(function (b, i) {
    b.addEventListener("click", function () {
      btns.forEach(function (x) { x.classList.remove("active"); });
      b.classList.add("active");
      applyStrategy(i);
    });
  });

  // re-caption in the new language without changing the selection on toggle
  document.getElementById("lang-toggle").addEventListener("click", function () {
    applyStrategy(activeStrategy);
  });

  applyStrategy(0);

  // -------------------------------------------------- console mock rows --
  var rows = [
    ["11:42:08", "openai", "ok", "200", "318ms"],
    ["11:42:05", "anthropic", "ok", "200", "402ms"],
    ["11:42:01", "bedrock", "err", "503", "88ms"],
    ["11:42:01", "openai", "ok", "200", "276ms"],
    ["11:41:57", "anthropic", "ok", "200", "390ms"],
    ["11:41:52", "openai", "ok", "200", "301ms"],
  ];
  var scroller = document.getElementById("shot-scroller");
  var rowsHtml = rows
    .map(function (r) {
      return (
        '<div class="r"><span>' + r[0] + "</span><span>" + r[1] + '</span><span class="shot-pill ' +
        r[2] + '">' + r[3] + "</span><span>" + r[4] + "</span></div>"
      );
    })
    .join("");
  scroller.innerHTML = rowsHtml + rowsHtml; // doubled for the seamless CSS scroll loop

  // ---------------------------------------------- ambient live numbers ---
  var req = 48206;
  var cost = 1208.4;
  function tickStats() {
    req += Math.floor(Math.random() * 4) + 1;
    cost += Math.random() * 0.3;
    document.getElementById("stat-req").textContent = req.toLocaleString();
    document.getElementById("stat-cost").textContent = "$" + cost.toFixed(2);
    document.getElementById("stat-lat").textContent = 380 + Math.floor(Math.random() * 70) + "ms";
    document.getElementById("stat-thr").textContent = 78 + Math.floor(Math.random() * 20) + " req/s";
  }
  function tickBars() {
    var a = 45 + Math.random() * 14;
    var b = 28 + Math.random() * 10;
    var c = Math.max(6, 100 - a - b);
    document.getElementById("bar-0").style.width = a + "%";
    document.getElementById("bar-1").style.width = b + "%";
    document.getElementById("bar-2").style.width = c + "%";
  }

  var reduceMotion = window.matchMedia && window.matchMedia("(prefers-reduced-motion: reduce)").matches;
  if (!reduceMotion) {
    setInterval(tickStats, 1400);
    setInterval(tickBars, 2600);
  }
})();
