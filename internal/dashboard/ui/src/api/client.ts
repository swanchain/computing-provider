import type {
  InferenceMetrics,
  ModelsResponse,
  ConnectionStatus,
  RequestManagement,
} from '../types';

const API_BASE = '/api/v1/computing/inference';

async function fetchJson<T>(endpoint: string): Promise<T> {
  const response = await fetch(`${API_BASE}${endpoint}`);
  if (!response.ok) {
    throw new Error(`API error: ${response.status} ${response.statusText}`);
  }
  return response.json();
}

async function postJson<T>(endpoint: string, body?: unknown): Promise<T> {
  const response = await fetch(`${API_BASE}${endpoint}`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: body ? JSON.stringify(body) : undefined,
  });
  if (!response.ok) {
    throw new Error(`API error: ${response.status} ${response.statusText}`);
  }
  return response.json();
}

export const api = {
  // Metrics
  getMetrics: () => fetchJson<InferenceMetrics>('/metrics'),
  getStatus: () => fetchJson<ConnectionStatus>('/status'),

  // Models
  getModels: () => fetchJson<ModelsResponse>('/models'),
  enableModel: (id: string) => postJson<{ success: boolean }>(`/models/${id}/enable`),
  disableModel: (id: string) => postJson<{ success: boolean }>(`/models/${id}/disable`),
  reloadModels: () => postJson<{ success: boolean }>('/models/reload'),
  forceHealthCheck: (id: string) => postJson<{ success: boolean }>(`/models/${id}/healthcheck`),

  // Request Management
  getRequestManagement: () => fetchJson<RequestManagement>('/request-management'),
  setGlobalRateLimit: (rate: number) => postJson<{ success: boolean }>('/ratelimit/global', { rate }),
  setModelRateLimit: (id: string, rate: number) => postJson<{ success: boolean }>(`/ratelimit/model/${id}`, { rate }),
  setGlobalConcurrency: (max: number) => postJson<{ success: boolean }>('/concurrency/global', { max }),
  setModelConcurrency: (id: string, max: number) => postJson<{ success: boolean }>(`/concurrency/model/${id}`, { max }),
};
