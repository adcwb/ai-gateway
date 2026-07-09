// Thin client for the ai-gateway management API.
// The console is a pure client of the documented public API — zero private
// endpoints (docs/design/08-web-console.md).
import { useCallback, useEffect, useRef, useState } from "react";
import type { DependencyList, Dispatch, SetStateAction } from "react";

const TOKEN_KEY = "aigw_admin_token";

export function getToken(): string {
  return localStorage.getItem(TOKEN_KEY) ?? "";
}

export function setToken(token: string) {
  localStorage.setItem(TOKEN_KEY, token);
}

export function clearToken() {
  localStorage.removeItem(TOKEN_KEY);
}

export class ApiError extends Error {
  constructor(
    public status: number,
    public code: string | number,
    message: string,
  ) {
    super(message);
  }
}

interface Envelope<T> {
  code: number | string;
  data?: T;
  msg: string;
}

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const headers: Record<string, string> = {
    "Content-Type": "application/json",
    ...(init?.headers as Record<string, string>),
  };
  const token = getToken();
  if (token) headers["Authorization"] = `Bearer ${token}`;

  const resp = await fetch(path, { ...init, headers });
  let body: Envelope<T>;
  try {
    body = await resp.json();
  } catch {
    throw new ApiError(resp.status, resp.status, resp.statusText);
  }
  if (!resp.ok || (typeof body.code === "number" && body.code !== 0) || (typeof body.code === "string" && body.code !== "")) {
    if (resp.ok && body.code === 0) return body.data as T;
    throw new ApiError(resp.status, body.code ?? resp.status, body.msg ?? "request failed");
  }
  return body.data as T;
}

export const api = {
  get: <T>(path: string, init?: RequestInit) => request<T>(path, init),
  post: <T>(path: string, data?: unknown) =>
    request<T>(path, { method: "POST", body: JSON.stringify(data ?? {}) }),
  put: <T>(path: string, data?: unknown) =>
    request<T>(path, { method: "PUT", body: JSON.stringify(data ?? {}) }),
  del: <T>(path: string) => request<T>(path, { method: "DELETE" }),
};

// ---------------------------------------------------------------------------
// useAsync: race-safe data fetching with optional polling.
// Replaces the repeated load() + setInterval + manual fetch boilerplate.
// AbortController cancels in-flight requests on unmount / dep change so stale
// responses never overwrite fresh ones; polling ticks are skipped while the
// document is hidden.
// ---------------------------------------------------------------------------

export interface UseAsyncResult<T> {
  data: T | null;
  loading: boolean;
  error: string;
  /** Stable error code from the API envelope (e.g. "BILLING_ACCOUNT_NOT_FOUND")
   *  when the request failed with an ApiError; "" otherwise. Lets pages tailor
   *  their empty/error state on a known code instead of matching the message. */
  errorCode: string;
  refresh: () => void;
  setData: Dispatch<SetStateAction<T | null>>;
}

export function useAsync<T>(
  fn: (signal: AbortSignal) => Promise<T>,
  deps: DependencyList,
  opts: { intervalMs?: number; skip?: boolean } = {},
): UseAsyncResult<T> {
  const { intervalMs, skip = false } = opts;
  const [data, setData] = useState<T | null>(null);
  const [loading, setLoading] = useState(!skip);
  const [error, setError] = useState("");
  const [errorCode, setErrorCode] = useState("");
  const [nonce, setNonce] = useState(0);
  const reqId = useRef(0);

  const refresh = useCallback(() => setNonce((n) => n + 1), []);

  useEffect(() => {
    if (skip) {
      setLoading(false);
      return;
    }
    const ac = new AbortController();
    const myId = ++reqId.current;
    setLoading(true);
    fn(ac.signal)
      .then((val) => {
        if (myId !== reqId.current) return;
        setData(val);
        setError("");
        setErrorCode("");
      })
      .catch((e: unknown) => {
        if (myId !== reqId.current || ac.signal.aborted) return;
        setError(e instanceof Error ? e.message : String(e));
        setErrorCode(e instanceof ApiError ? String(e.code) : "");
      })
      .finally(() => {
        if (myId !== reqId.current) return;
        setLoading(false);
      });
    return () => ac.abort();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [...deps, nonce, skip]);

  useEffect(() => {
    if (!intervalMs || skip) return;
    const id = window.setInterval(() => {
      if (document.hidden) return;
      setNonce((n) => n + 1);
    }, intervalMs);
    return () => window.clearInterval(id);
  }, [intervalMs, skip]);

  return { data, loading, error, errorCode, refresh, setData };
}

// ---------------------------------------------------------------------------
// Typed shapes for the endpoints the console consumes
// ---------------------------------------------------------------------------

export interface VirtualKey {
  id: number;
  name: string;
  keyPrefix?: string;
  isEnabled: boolean;
  projectId?: string | null;
  envId?: string | null;
  expiresAt?: string | null;
  createdAt: string;
  piiPolicyId?: number | null;
  cacheConfig?: CacheConfig | null;
}

// CacheConfig mirrors keyCacheConfig in backend/internal/biz/respcache.go —
// stored as the VirtualKey's raw `cacheConfig` JSON column.
export interface CacheConfig {
  exactEnabled?: boolean;
  ttlSec?: number;
  billingPolicy?: "free" | "discount" | "full";
  discountPercent?: number;
  semanticEnabled?: boolean;
  semanticThreshold?: number;
  semanticTtlSec?: number;
}

export interface KeyStats {
  total?: number;
  enabled?: number;
  disabled?: number;
  [k: string]: unknown;
}

// Mirrors dto.QuotaConfigItem — per-model quota override with its live usage.
export interface QuotaConfigItem {
  modelName: string;
  dailyTokenQuota: number;
  hourlyTokenQuota: number;
  hourlyReqQuota: number;
  dailyPointQuota: number;
  hourlyPointQuota: number;
  dailyTokenUsed: number;
  hourlyTokenUsed: number;
  hourlyReqUsed: number;
  dailyPointUsed: number;
  hourlyPointUsed: number;
}

// Mirrors dto.QuotaConfigResp (GET /ai/gateway/key/quota-config).
export interface QuotaConfig {
  keyId: number;
  name: string;
  keyPrefix: string;
  providerId: number;
  allowedModels?: unknown;
  dailyTokenQuota: number;
  hourlyTokenQuota: number;
  hourlyReqQuota: number;
  maxConcurrency: number;
  dailyPointQuota: number;
  hourlyPointQuota: number;
  modelQuotas: QuotaConfigItem[];
}

// Mirrors dto.KeyQuotaUsageResp (GET /ai/gateway/key/quota-usage).
export interface KeyQuotaUsage {
  keyId: number;
  dailyTokenQuota: number;
  dailyTokenUsed: number;
  hourlyTokenQuota: number;
  hourlyTokenUsed: number;
  hourlyReqQuota: number;
  hourlyReqUsed: number;
  maxConcurrency: number;
  currentConcurrency: number;
  dailyPointQuota: number;
  dailyPointUsed: number;
  hourlyPointQuota: number;
  hourlyPointUsed: number;
}

export interface Provider {
  id: number;
  name: string;
  baseUrl: string;
  providerType: string;
  models: { name: string; is_default?: boolean }[] | null;
  isEnabled: boolean;
  weight: number;
  priority: number;
  description: string;
}

export interface ProviderHealth {
  providerId: number;
  name: string;
  state: "closed" | "half_open" | "open";
  isEnabled: boolean;
  weight: number;
  priority: number;
}

export interface AttemptRecord {
  providerId: number;
  status: number;
  err?: string;
  latencyMs: number;
}

export interface AuditLog {
  id: number;
  createdAt: string;
  keyId?: number;
  keyName?: string;
  providerId?: number;
  model?: string;
  requestedModel?: string;
  promptTokens?: number;
  completionTokens?: number;
  cacheReadTokens?: number;
  latencyMs?: number;
  statusCode?: number;
  errorMessage?: string;
  errMsg?: string;
  clientIp?: string;
  clientAgent?: string;
  protocol?: string;
  sessionId?: string;
  traceId?: string;
  spanId?: string;
  attemptsTotal?: number;
  providerAttempts?: AttemptRecord[] | string;
  piiBlocked?: boolean;
  piiAction?: string;
  piiTypes?: string;
  requestBody?: string;
  responseBody?: string;
  pointsConsumed?: number;
  priceConsumed?: number;
  [k: string]: unknown;
}

export interface AuditSessionSummary {
  sessionId: string;
  firstAt: string;
  lastAt: string;
  reqCount: number;
  promptTokens: number;
  completionTokens: number;
  totalTokens: number;
  pointsConsumed: number;
  priceConsumed: number;
  finalStatusCode: number;
  keyName: string;
  clientAgent: string;
  protocol: string;
  model: string;
}

export interface SecurityOverview {
  totalRequests: number;
  blockCount: number;
  redactCount: number;
  errorCount: number;
  errorRate: number;
  topPiiTypes: { type: string; count: number }[];
  topErrorModels: { model: string; error_count: number }[];
}

export interface PageResp<T> {
  list?: T[];
  items?: T[];
  total?: number;
  [k: string]: unknown;
}

export interface Tenant {
  id: number;
  name: string;
  displayName: string;
  status: string;
  keyCount: number;
  account?: BillingAccount | null;
}

export interface BillingAccount {
  id: number;
  tenantId: number;
  isEnabled: boolean;
  mode: "prepaid" | "postpaid";
  currency: string;
  balanceMicro: number;
  creditLimitMicro: number;
  lowWatermarkMicro: number;
  status: "active" | "grace" | "suspended";
  graceUntil?: string | null;
}

export interface LedgerEntry {
  id: number;
  createdAt: string;
  entryType: string;
  amountMicro: number;
  balanceAfterMicro: number;
  idempotencyKey: string;
  refType: string;
  refId: string;
  remark: string;
}

export interface UsageOverview {
  days: number;
  requests: number;
  promptTokens: number;
  completionTokens: number;
  costCredits: number;
  priceCredits: number;
  cacheHits: number;
  topModels: { model: string; requests: number; priceMicro: number }[];
}

export interface Project {
  id: number;
  tenantId: number;
  name: string;
  description: string;
}

export const MICRO = 1_000_000;
export const credits = (micro: number) => (micro / MICRO).toFixed(4);

export interface CreateKeyResp {
  id: number;
  name: string;
  keyPrefix: string;
  plainKey: string;
}

export interface UsagePoint {
  day: string;
  requests: number;
  promptTokens: number;
  completionTokens: number;
  costCredits: number;
  priceCredits: number;
}

export interface AuthConfig {
  oidcEnabled: boolean;
}

export interface SessionInfo {
  userId: number;
  email: string;
  displayName: string;
  isPlatformAdmin: boolean;
}

export interface UserItem {
  id: number;
  email: string;
  displayName: string;
  isPlatformAdmin: boolean;
  isEnabled: boolean;
  role: string;
}

export interface AdminKey {
  id: number;
  name: string;
  keyPrefix: string;
  tenantId: number;
  role: string;
  isEnabled: boolean;
  lastUsedAt?: string | null;
}

export interface CreateAdminKeyResp {
  id: number;
  name: string;
  keyPrefix: string;
  plainKey: string;
}

export interface ModelItem {
  id: number;
  providerId: number;
  name: string;
  modelType: string;
  contextWindow: number;
  isDefault: boolean;
  isEnabled: boolean;
  source: string;
  description: string;
  inputPricePerMillion: number;
  outputPricePerMillion: number;
  cacheReadPricePerMillion: number;
  cacheWritePricePerMillion: number;
}

export interface PriceTableItem {
  id: number;
  priceTableId: number;
  modelPattern: string;
  inputPricePerMillion: number;
  outputPricePerMillion: number;
  cacheReadPerMillion: number;
}

export interface PriceTable {
  id: number;
  name: string;
  currency: string;
  items?: PriceTableItem[];
}

export interface PatternTestResp {
  matched: string[];
  isRegex: boolean;
}

export interface Settings {
  alertWebhook: string;
  alertWebhookIsOverride: boolean;
  cacheEmbeddingProviderId?: number;
  cacheEmbeddingModel?: string;
  cacheEmbeddingDim?: number;
}

export interface CreditsRate {
  id: number;
  currency: string;
  ratePerCredit: number;
  isEnabled: boolean;
  description: string;
}

export interface McpServer {
  id: number;
  name: string;
  baseUrl: string;
  description: string;
  isEnabled: boolean;
  createdAt: string;
}

export interface FallbackChainEntry {
  providerId: number;
  model: string;
}

export interface ModelMapping {
  id: number;
  virtualKeyId: number;
  virtualModel: string;
  realModelId: number;
  realModel?: { id: number; name: string; modelType?: string } | null;
  isEnabled: boolean;
  description: string;
  fallbackChain?: FallbackChainEntry[] | null;
  createdAt: string;
}

export interface CheckerConfig {
  name: "pii_rules" | "prompt_injection" | "topic_fence" | "external";
  settings?: Record<string, unknown>;
}

export interface PIIPolicy {
  id: number;
  name: string;
  enabled: boolean;
  action: "block" | "redact" | "log";
  isDefault: boolean;
  ruleConfig?: Record<string, unknown> | null;
  description: string;
  checkerChain?: CheckerConfig[] | null;
  failMode: "open" | "closed";
  boundKeyCount: number;
  createdAt: string;
}
