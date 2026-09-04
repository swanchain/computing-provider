import { AlertTriangle, CheckCircle2, CircleX, ChevronDown } from 'lucide-react';
import type { ConnectionStatus, InferenceMetrics, ModelsResponse } from '../types';

interface DataIssue {
  label: string;
  error: Error | null;
}

interface OperationalSummaryProps {
  status: ConnectionStatus | null;
  metrics: InferenceMetrics | null;
  models: ModelsResponse | null;
  dataIssues: DataIssue[];
  loading?: boolean;
}

type Severity = 'healthy' | 'warning' | 'critical';

export function OperationalSummary({ status, metrics, models, dataIssues, loading }: OperationalSummaryProps) {
  if (loading && !status && !metrics && !models) {
    return (
      <div role="status" className="mb-4 flex items-center gap-3 rounded-xl border border-slate-800 bg-slate-900 px-4 py-3">
        <span aria-hidden="true" className="h-5 w-5 animate-pulse rounded-full bg-slate-700" />
        <div>
          <p className="font-medium text-slate-200">Checking operational status…</p>
          <p className="mt-0.5 text-sm text-slate-400">Loading connection, model, and capacity signals.</p>
        </div>
      </div>
    );
  }

  const messages: string[] = [];
  let severity: Severity = 'healthy';
  const failedSources = dataIssues.filter((item) => item.error).map((item) => item.label);

  if (failedSources.length > 0) {
    severity = 'warning';
    messages.push(`Stale or unavailable: ${failedSources.join(', ')}`);
  }
  if (status && !status.connected) {
    severity = 'critical';
    messages.push('Disconnected from Swan Inference');
  }
  if (models?.summary.unhealthy) {
    severity = 'critical';
    messages.push(`${models.summary.unhealthy} unhealthy model${models.summary.unhealthy === 1 ? '' : 's'}`);
  }
  if (models && models.summary.total === 0) {
    severity = 'critical';
    messages.push('No models configured');
  }

  const hotGPUs = metrics?.gpu_metrics.filter((gpu) => gpu.temperature_c >= 85).length ?? 0;
  if (hotGPUs > 0) {
    if (severity === 'healthy') severity = 'warning';
    messages.push(`${hotGPUs} GPU${hotGPUs === 1 ? '' : 's'} at or above 85°C`);
  }

  if (metrics && metrics.total_requests >= 10) {
    const failureRate = metrics.failed_requests / metrics.total_requests;
    if (failureRate >= 0.05) {
      if (severity === 'healthy') severity = 'warning';
      messages.push(`${(failureRate * 100).toFixed(1)}% session failure rate`);
    }
  }

  const visual = {
    healthy: {
      Icon: CheckCircle2,
      title: 'All operational signals look healthy',
      copy: models ? `${models.summary.ready} of ${models.summary.total} models ready` : 'Waiting for model status',
      className: 'border-emerald-800/70 bg-emerald-950/30',
      iconClass: 'text-emerald-300',
      titleClass: 'text-emerald-100',
    },
    warning: {
      Icon: AlertTriangle,
      title: 'Provider status needs a closer look',
      copy: messages.join(' · '),
      className: 'border-amber-800/70 bg-amber-950/30',
      iconClass: 'text-amber-300',
      titleClass: 'text-amber-100',
    },
    critical: {
      Icon: CircleX,
      title: 'Provider needs attention',
      copy: messages.join(' · '),
      className: 'border-red-800/70 bg-red-950/30',
      iconClass: 'text-red-300',
      titleClass: 'text-red-100',
    },
  }[severity];
  const Icon = visual.Icon;

  // A single row beside the page title rather than a full-width card below it.
  // The banner earns its space when something is wrong; when every signal is
  // healthy it was a paragraph saying so, pushing the actual figures down the
  // page. Severity still carries its own colour and icon, the detail is kept in
  // the title attribute rather than dropped, and anything non-healthy keeps the
  // jump-to-operations action.
  const detail = `${visual.title}. ${visual.copy}`;
  return (
    <div
      role={severity === 'critical' ? 'alert' : 'status'}
      title={detail}
      className={`flex min-w-0 max-w-full items-center gap-2 rounded-lg border px-3 py-1.5 ${visual.className}`}
    >
      <Icon aria-hidden="true" size={16} className={`shrink-0 ${visual.iconClass}`} />
      <p className={`min-w-0 truncate text-sm font-medium ${visual.titleClass}`}>
        {visual.title}
        {visual.copy && <span className="ml-2 font-normal text-slate-300">{visual.copy}</span>}
      </p>
      {severity !== 'healthy' && (
        <button
          type="button"
          onClick={() => document.getElementById('operations-heading')?.scrollIntoView({ behavior: 'smooth' })}
          className="ml-1 inline-flex shrink-0 items-center gap-1 rounded-md border border-current/30 px-2 py-1 text-xs font-medium text-slate-200 hover:bg-white/5 focus:outline-none focus:ring-2 focus:ring-blue-400"
        >
          Review <ChevronDown aria-hidden="true" size={13} />
        </button>
      )}
    </div>
  );
}
