// API Response Types based on internal/computing/*.go

export interface InferenceMetrics {
  total_requests: number;
  successful_requests: number;
  failed_requests: number;
  avg_latency_ms: number;
  p95_latency_ms: number;
  p99_latency_ms: number;
  total_tokens_processed: number;
  tokens_per_second: number;
  active_connections: number;
  uptime_seconds: number;
  models: Record<string, ModelMetrics>;
  gpu: GPUMetrics;
}

export interface ModelMetrics {
  requests: number;
  successes: number;
  failures: number;
  avg_latency_ms: number;
  tokens_processed: number;
}

export interface GPUMetrics {
  gpu_count: number;
  total_memory_mb: number;
  used_memory_mb: number;
  avg_utilization: number;
  avg_temperature: number;
  gpus: GPUInfo[];
}

export interface GPUInfo {
  index: number;
  name: string;
  memory_total_mb: number;
  memory_used_mb: number;
  memory_free_mb: number;
  utilization: number;
  temperature: number;
}

export interface ModelStatus {
  id: string;
  endpoint: string;
  enabled: boolean;
  healthy: boolean;
  last_check: string;
  consecutive_failures: number;
  error_message: string;
  gpu_memory: number;
  category: string;
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
  active_requests: number;
  max_concurrent: number;
  total_acquired: number;
  total_rejected: number;
  wait_time_avg_ms: number;
  models: Record<string, ModelConcurrencyMetrics>;
}

export interface ModelConcurrencyMetrics {
  active: number;
  max: number;
  acquired: number;
  rejected: number;
}

export interface QueueMetrics {
  current_size: number;
  max_size: number;
  total_enqueued: number;
  total_dequeued: number;
  total_dropped: number;
  avg_wait_time_ms: number;
}

export interface ConnectionStatus {
  connected: boolean;
  websocket_url: string;
  node_id: string;
  reconnect_attempts: number;
  last_connected: string;
  models: string[];
}

export interface RequestManagement {
  rate_limiter: RateLimiterMetrics;
  concurrency: ConcurrencyMetrics;
  queue: QueueMetrics;
}

export interface HealthResponse {
  models: Record<string, ModelHealth>;
  overall_healthy: boolean;
}

export interface ModelHealth {
  healthy: boolean;
  last_check: string;
  latency_ms: number;
  error: string;
}
