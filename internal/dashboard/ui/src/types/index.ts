// API Response Types based on actual API responses

export interface InferenceMetrics {
  connection_state: string;
  last_connected_at: string;
  last_disconnected_at: string;
  reconnect_count: number;
  total_requests: number;
  successful_requests: number;
  failed_requests: number;
  streaming_requests: number;
  avg_latency_ms: number;
  p50_latency_ms: number;
  p95_latency_ms: number;
  p99_latency_ms: number;
  total_tokens_in: number;
  total_tokens_out: number;
  tokens_per_second: number;
  active_requests: number;
  requests_per_minute: number;
  model_metrics: Record<string, ModelMetrics>;
  gpu_metrics: GPUInfo[];
  cpu_usage_percent: number;
  memory_usage_percent: number;
  memory_used_gb: number;
  memory_total_gb: number;
}

export interface ModelMetrics {
  requests: number;
  successes: number;
  failures: number;
  avg_latency_ms: number;
  tokens_processed: number;
}

export interface GPUInfo {
  index: number;
  name: string;
  utilization_percent: number;
  memory_used_mb: number;
  memory_total_mb: number;
  memory_usage_percent: number;
  temperature_c: number;
  power_draw_w: number;
  power_limit_w: number;
  compute_processes: number;
}

export interface ModelStatus {
  id: string;
  container: string;
  endpoint: string;
  gpu_memory: number;
  category: string;
  state: number;
  state_string: string;
  health: number;
  health_string: string;
  loaded_at: string;
  updated_at: string;
  enabled: boolean;
}

export interface ModelsResponse {
  models: ModelStatus[];
  summary: {
    total: number;
    ready: number;
    unhealthy: number;
    disabled: number;
  };
}

export interface RateLimiterMetrics {
  total_allowed: number;
  total_throttled: number;
  current_rate: number;
  current_tokens: number;
  burst_size: number;
  adaptive_enabled: boolean;
}

export interface ConcurrencyMetrics {
  global_active: number;
  global_max: number;
  total_acquired: number;
  total_released: number;
  total_rejected: number;
  total_timeouts: number;
  per_model_active: Record<string, number>;
  per_model_max: Record<string, number>;
  avg_hold_time_ms: number;
}

export interface RetryMetrics {
  total_attempts: number;
  total_retries: number;
  total_successes: number;
  total_failures: number;
  total_non_retryable: number;
  avg_retries_per_request: number;
  retry_success_rate: number;
}

export interface ConnectionStatus {
  connected: boolean;
  active_models: string[];
}

export interface RequestManagement {
  rate_limiter: RateLimiterMetrics;
  concurrency_limiter: ConcurrencyMetrics;
  retry_policy: RetryMetrics;
}
