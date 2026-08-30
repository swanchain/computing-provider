import type {
  InferenceMetrics,
  ModelsResponse,
  ConnectionStatus,
  RequestManagement,
  RequestHistoryResponse,
  HistoricalMetricsResponse,
  ModelDetailedMetrics,
  DashboardSettings,
  AlertSettings,
  SelfCheckSettings,
  LogSettings,
  RequestLimitSettings,
  DashboardModel,
  SettingsSaveResult,
} from '../types';

const API_BASE = '/api/v1/computing/inference';
const TOKEN_KEY = 'computing-provider-control-token';

let accessToken = sessionStorage.getItem(TOKEN_KEY) ?? '';

function modelPath(id: string): string {
  return encodeURIComponent(id);
}

export class ApiError extends Error {
  status: number;

  constructor(status: number, message: string) {
    super(message);
    this.name = 'ApiError';
    this.status = status;
  }
}

function authHeaders(): HeadersInit {
  return accessToken ? { Authorization: `Bearer ${accessToken}` } : {};
}

async function fetchJson<T>(endpoint: string): Promise<T> {
  const response = await fetch(`${API_BASE}${endpoint}`, { headers: authHeaders() });
  if (!response.ok) {
    const body = await response.json().catch(() => null) as { error?: string } | null;
    throw new ApiError(response.status, body?.error ?? `API error: ${response.status} ${response.statusText}`);
  }
  return response.json();
}

async function postJson<T>(endpoint: string, body?: unknown): Promise<T> {
  const response = await fetch(`${API_BASE}${endpoint}`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json', ...authHeaders() },
    body: body ? JSON.stringify(body) : undefined,
  });
  if (!response.ok) {
    const responseBody = await response.json().catch(() => null) as { error?: string } | null;
    throw new ApiError(response.status, responseBody?.error ?? `API error: ${response.status} ${response.statusText}`);
  }
  return response.json();
}

async function putJson<T>(endpoint: string, body: unknown): Promise<T> {
  const response = await fetch(`${API_BASE}${endpoint}`, {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json', ...authHeaders() },
    body: JSON.stringify(body),
  });
  if (!response.ok) {
    const responseBody = await response.json().catch(() => null) as { error?: string } | null;
    throw new ApiError(response.status, responseBody?.error ?? `API error: ${response.status} ${response.statusText}`);
  }
  return response.json();
}

export const api = {
  setAccessToken: (token: string) => {
    accessToken = token.trim();
    if (accessToken) sessionStorage.setItem(TOKEN_KEY, accessToken);
    else sessionStorage.removeItem(TOKEN_KEY);
  },
  hasAccessToken: () => Boolean(accessToken),
  clearAccessToken: () => {
    accessToken = '';
    sessionStorage.removeItem(TOKEN_KEY);
  },

  // Metrics
  getMetrics: () => fetchJson<InferenceMetrics>('/metrics'),
  getStatus: () => fetchJson<ConnectionStatus>('/status'),

  // Models
  getModels: () => fetchJson<ModelsResponse>('/models'),
  enableModel: (id: string) => postJson<{ success: boolean }>(`/models/${modelPath(id)}/enable`),
  disableModel: (id: string) => postJson<{ success: boolean }>(`/models/${modelPath(id)}/disable`),
  reloadModels: () => postJson<{ success: boolean }>('/models/reload'),
  forceHealthCheck: (id: string) => postJson<{ success: boolean }>(`/models/${modelPath(id)}/healthcheck`),

  // Request Management
  getRequestManagement: () => fetchJson<RequestManagement>('/request-management'),
  setGlobalRateLimit: (rate: number) => postJson<{ success: boolean }>('/ratelimit/global', { rate }),
  setModelRateLimit: (id: string, rate: number) => postJson<{ success: boolean }>(`/ratelimit/model/${modelPath(id)}`, { rate }),
  setGlobalConcurrency: (max: number) => postJson<{ success: boolean }>('/concurrency/global', { max }),
  setModelConcurrency: (id: string, max: number) => postJson<{ success: boolean }>(`/concurrency/model/${modelPath(id)}`, { max }),

  // Request History
  getRequestHistory: (limit?: number, model?: string) => {
    const params = new URLSearchParams();
    if (limit) params.set('limit', limit.toString());
    if (model) params.set('model', model);
    const query = params.toString();
    return fetchJson<RequestHistoryResponse>(`/requests${query ? `?${query}` : ''}`);
  },

  // Historical Metrics
  getMetricsHistory: (duration?: string, resolution?: string) => {
    const params = new URLSearchParams();
    if (duration) params.set('duration', duration);
    if (resolution) params.set('resolution', resolution);
    const query = params.toString();
    return fetchJson<HistoricalMetricsResponse>(`/metrics/history${query ? `?${query}` : ''}`);
  },

  // Model Detailed Metrics
  getModelMetrics: (id: string) => fetchJson<ModelDetailedMetrics>(`/models/${modelPath(id)}/metrics`),

  // Authenticated operator settings
  getSettings: () => fetchJson<DashboardSettings>('/settings'),
  updateAlerts: (settings: AlertSettings) => putJson<SettingsSaveResult>('/settings/alerts', settings),
  updateSelfCheck: (settings: SelfCheckSettings) => putJson<SettingsSaveResult>('/settings/self-check', settings),
  updateLogging: (settings: LogSettings) => putJson<SettingsSaveResult>('/settings/logging', settings),
  updateLimits: (settings: RequestLimitSettings) => putJson<SettingsSaveResult>('/settings/limits', settings),
  updateModels: (models: DashboardModel[]) => putJson<SettingsSaveResult>('/settings/models', { models }),
};
