// Thin client for the ai-gateway management API.
// The console is a pure client of the documented public API — zero private
// endpoints (docs/design/08-web-console.md).

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
  get: <T>(path: string) => request<T>(path),
  post: <T>(path: string, data?: unknown) =>
    request<T>(path, { method: "POST", body: JSON.stringify(data ?? {}) }),
  put: <T>(path: string, data?: unknown) =>
    request<T>(path, { method: "PUT", body: JSON.stringify(data ?? {}) }),
  del: <T>(path: string) => request<T>(path, { method: "DELETE" }),
};

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
}

export interface KeyStats {
  total?: number;
  enabled?: number;
  disabled?: number;
  [k: string]: unknown;
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

export interface AuditLog {
  id: number;
  createdAt: string;
  keyId?: number;
  keyName?: string;
  providerId?: number;
  model?: string;
  promptTokens?: number;
  completionTokens?: number;
  latencyMs?: number;
  statusCode?: number;
  errMsg?: string;
  clientIp?: string;
  [k: string]: unknown;
}

export interface PageResp<T> {
  list?: T[];
  items?: T[];
  total?: number;
  [k: string]: unknown;
}
