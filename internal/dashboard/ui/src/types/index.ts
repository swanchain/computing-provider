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

export interface EmailSettings {
  host: string;
  port: number;
  username: string;
  password?: string;
  password_set: boolean;
  clear_password?: boolean;
  from: string;
  to: string[];
}

export interface AlertSettings {
  webhook_url: string;
  cooldown_minutes: number;
  disconnect_after_min: number;
  error_rate_threshold: number;
  error_rate_min_requests: number;
  email: EmailSettings;
}

export interface SelfCheckSettings {
  enable: boolean;
  interval_minutes: number;
  auto_disable: boolean;
  auto_recover: boolean;
  failures_before_disable: number;
}

export interface LogSettings {
  dir: string;
  level: string;
  max_size_mb: number;
  max_backups: number;
  max_age_days: number;
  compress: boolean;
  stdout: boolean;
}

export interface RequestLimitSettings {
  requests_per_second: number;
  max_concurrent: number;
}

export interface DashboardModel {
  id: string;
  container?: string;
  endpoint: string;
  gpu_memory: number;
  category: string;
  local_model?: string;
  format?: string;
  quantization?: string;
  api_key?: string;
  api_key_set: boolean;
  clear_api_key?: boolean;
  context_length?: number;
}

export interface DashboardSettings {
  alerts: AlertSettings;
  self_check: SelfCheckSettings;
  log: LogSettings;
  limits: RequestLimitSettings;
  models: DashboardModel[];
}

export interface SettingsSaveResult {
  status: string;
  restart_required: boolean;
}

export interface ModelsResponse {
  /** Rolling per-model health samples, oldest first. Resets when the node restarts. */
  health_log?: Record<string, string[]>;
  models: ModelStatus[];
  prices: Record<string, ModelPrice>;
  summary: {
    total: number;
    ready: number;
    unhealthy: number;
    disabled: number;
  };
}

export interface ModelPrice {
  input_price: number;
  output_price: number;
  provider_input_price: number;
  provider_output_price: number;
  tier?: string;
  unit: string;
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
  /** Semantic version of the running provider, e.g. "0.5.0". */
  version?: string;
  /** Full build string, e.g. "0.5.0+mainnet+git.5f7e316". */
  build?: string;
}

export interface RequestManagement {
  rate_limiter: RateLimiterMetrics;
  concurrency_limiter: ConcurrencyMetrics;
  retry_policy: RetryMetrics;
}

export interface ModelEarnings {
  model: string;
  tokens_in: number;
  tokens_out: number;
  input_usd: number;
  output_usd: number;
  total_usd: number;
  /** False when no rate was available — the row is shown as unpriced, not $0. */
  priced: boolean;
}

export interface PlatformEarnings {
  total_usd: number;
  total_tokens: number;
  total_inferences: number;
  failed_inferences: number;
  uptime_7d_percent: number;
  /** Why the platform figure is missing, when it is. */
  unavailable?: string;
}

export interface Earnings {
  /** Lifetime and authoritative — what the operator is actually paid. */
  platform: PlatformEarnings;
  models: ModelEarnings[];
  /** This process only; the node's counters reset on restart. */
  session_usd: number;
  currency: string;
  unpriced_models: number;
}

export interface EarningsPoint {
  timestamp: string;
  tokens_in: number;
  tokens_out: number;
  usd: number;
}

export interface EarningsSeries {
  points: EarningsPoint[];
  total_usd: number;
  currency: string;
  duration: string;
  /** Counter resets inside the window; the total is a floor, not exact. */
  restarts: number;
  /** How far back the stored history actually reaches. */
  covers?: string;
}

export interface RequestLog {
  request_id: string;
  model: string;
  start_time: string;
  end_time: string;
  latency_ms: number;
  tokens_in: number;
  tokens_out: number;
  streaming: boolean;
  success: boolean;
  error_reason?: string;
  /**
   * Where the request entered this node: "hub" for work routed over the
   * WebSocket, "health" for the engine probe, "selfcheck" for the audit probe.
   * Absent on records written before the field existed.
   */
  source?: 'hub' | 'health' | 'selfcheck';
}

export interface RequestHistoryResponse {
  requests: RequestLog[];
}

export interface HistoricalDataPoint {
  timestamp: string;
  total_requests: number;
  success_rate: number;
  avg_latency_ms: number;
  p99_latency_ms: number;
  tokens_per_second: number;
  requests_per_minute: number;
}

export interface HistoricalMetricsResponse {
  data: HistoricalDataPoint[];
  duration: string;
  resolution: string;
}

export interface ModelDetailedMetrics {
  model?: ModelStatus;
  health?: {
    health: number;
    health_string: string;
    last_check: string;
    consecutive_fails: number;
    last_error?: string;
  };
  metrics?: {
    model_name: string;
    total_requests: number;
    successful_requests: number;
    failed_requests: number;
    avg_latency_ms: number;
    total_tokens_in: number;
    total_tokens_out: number;
    tokens_per_second: number;
    active_requests: number;
  };
  price?: ModelPrice;
  recent_requests?: RequestLog[];
}
