// Minimal bilingual dictionary (en / zh). Kept dependency-free for the MVP;
// swap for react-i18next when the console grows (docs/design/08-web-console.md).

export type Lang = "en" | "zh";

const LANG_KEY = "aigw_lang";

export function getLang(): Lang {
  const v = localStorage.getItem(LANG_KEY);
  return v === "zh" || v === "en" ? v : navigator.language.startsWith("zh") ? "zh" : "en";
}

export function setLang(lang: Lang) {
  localStorage.setItem(LANG_KEY, lang);
}

const dict: Record<string, { en: string; zh: string }> = {
  appName: { en: "ai-gateway Console", zh: "ai-gateway 控制台" },
  login: { en: "Sign in", zh: "登录" },
  loginHint: {
    en: "Enter the admin token configured in system.admin_token (AIGW_ADMIN_TOKEN).",
    zh: "输入 system.admin_token（AIGW_ADMIN_TOKEN）配置的管理令牌。",
  },
  adminToken: { en: "Admin token", zh: "管理令牌" },
  logout: { en: "Sign out", zh: "退出" },
  dashboard: { en: "Dashboard", zh: "仪表盘" },
  keys: { en: "Virtual Keys", zh: "虚拟 Key" },
  providers: { en: "Providers", zh: "提供方" },
  audit: { en: "Audit", zh: "审计中心" },
  totalKeys: { en: "Total keys", zh: "Key 总数" },
  enabledKeys: { en: "Enabled", zh: "已启用" },
  disabledKeys: { en: "Disabled", zh: "已停用" },
  providerHealth: { en: "Provider health", zh: "提供方健康" },
  name: { en: "Name", zh: "名称" },
  status: { en: "Status", zh: "状态" },
  state: { en: "Breaker", zh: "熔断" },
  weight: { en: "Weight", zh: "权重" },
  priority: { en: "Priority", zh: "优先级" },
  baseUrl: { en: "Base URL", zh: "上游地址" },
  models: { en: "Models", zh: "模型" },
  enabled: { en: "enabled", zh: "启用" },
  disabled: { en: "disabled", zh: "停用" },
  time: { en: "Time", zh: "时间" },
  model: { en: "Model", zh: "模型" },
  tokens: { en: "Tokens (in/out)", zh: "Token（入/出）" },
  latency: { en: "Latency", zh: "延迟" },
  httpStatus: { en: "HTTP", zh: "HTTP" },
  clientIp: { en: "Client IP", zh: "客户端 IP" },
  error: { en: "Error", zh: "错误" },
  refresh: { en: "Refresh", zh: "刷新" },
  loading: { en: "Loading…", zh: "加载中…" },
  empty: { en: "No data", zh: "暂无数据" },
  expires: { en: "Expires", zh: "过期时间" },
  never: { en: "never", zh: "永不" },
  loadFailed: { en: "Failed to load", zh: "加载失败" },
  unauthorized: {
    en: "Unauthorized — check the admin token.",
    zh: "认证失败——请检查管理令牌。",
  },
  breaker_closed: { en: "closed", zh: "关闭（健康）" },
  breaker_half_open: { en: "half-open", zh: "半开（探测中）" },
  breaker_open: { en: "open", zh: "打开（熔断）" },
  tenants: { en: "Tenants", zh: "租户" },
  billing: { en: "Billing", zh: "计费中心" },
  createTenant: { en: "Create tenant", zh: "新建租户" },
  createProject: { en: "Create project", zh: "新建项目" },
  displayName: { en: "Display name", zh: "显示名" },
  keyCount: { en: "Keys", zh: "Key 数" },
  balance: { en: "Balance", zh: "余额" },
  billingEnabled: { en: "Billing", zh: "计费" },
  billingMode: { en: "Mode", zh: "模式" },
  recharge: { en: "Recharge", zh: "充值" },
  rechargeAmount: { en: "Amount (credits)", zh: "金额（积分）" },
  ledger: { en: "Ledger", zh: "流水" },
  entryType: { en: "Type", zh: "类型" },
  amount: { en: "Amount", zh: "金额" },
  balanceAfter: { en: "Balance after", zh: "变动后余额" },
  remark: { en: "Remark", zh: "备注" },
  selectTenant: { en: "Tenant", zh: "租户" },
  usage7d: { en: "Usage (7 days)", zh: "近 7 天用量" },
  requests: { en: "Requests", zh: "请求数" },
  promptTokens: { en: "Input tokens", zh: "输入 Token" },
  completionTokens: { en: "Output tokens", zh: "输出 Token" },
  cost: { en: "Cost (credits)", zh: "成本（积分）" },
  price: { en: "Billed (credits)", zh: "计费（积分）" },
  cacheHits: { en: "Cache hits", zh: "缓存命中" },
  topModels: { en: "Top models", zh: "Top 模型" },
  enableBilling: { en: "Enable billing", zh: "启用计费" },
  disableBilling: { en: "Disable billing", zh: "停用计费" },
  status_active: { en: "active", zh: "正常" },
  status_grace: { en: "grace", zh: "宽限期" },
  status_suspended: { en: "suspended", zh: "已停用" },
  submit: { en: "Submit", zh: "提交" },
  project: { en: "Project", zh: "项目" },
  actions: { en: "Actions", zh: "操作" },
};

export function t(key: string, lang: Lang): string {
  return dict[key]?.[lang] ?? key;
}
