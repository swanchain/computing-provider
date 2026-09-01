import type { ModelStatus } from '../types';

interface ModelHealthMapProps {
  models: ModelStatus[];
  loading: boolean;
  onModelClick?: (id: string) => void;
}

type Bucket = 'healthy' | 'degraded' | 'offline' | 'disabled' | 'unknown';

// A model an operator switched off is not a model that failed. Collapsing the
// two would have the map cry wolf every time somebody disables one on purpose.
function bucketOf(m: ModelStatus): Bucket {
  if (!m.enabled) return 'disabled';
  switch ((m.health_string || '').toLowerCase()) {
    case 'healthy': return 'healthy';
    case 'degraded': return 'degraded';
    case 'unhealthy': return 'offline';
    default: return 'unknown';
  }
}

// Each bucket differs by more than hue: filled, ringed, hollow. Colour alone
// carries nothing for a colour-blind operator, and this strip is meant to be
// readable in one glance rather than after a legend lookup.
const STYLE: Record<Bucket, { dot: string; label: string }> = {
  healthy:  { dot: 'bg-emerald-400',                              label: 'healthy' },
  degraded: { dot: 'bg-amber-400 ring-2 ring-amber-400/40',       label: 'degraded' },
  offline:  { dot: 'bg-red-500 ring-2 ring-red-500/40',           label: 'offline' },
  disabled: { dot: 'bg-transparent border-2 border-slate-600',    label: 'disabled' },
  unknown:  { dot: 'bg-slate-600',                                label: 'unknown' },
};

const ORDER: Bucket[] = ['offline', 'degraded', 'unknown', 'disabled', 'healthy'];

export function ModelHealthMap({ models, loading, onModelClick }: ModelHealthMapProps) {
  if (loading && models.length === 0) {
    return <div className="mb-3 h-14 animate-pulse rounded-xl bg-slate-800/50" />;
  }
  if (models.length === 0) return null;

  // Problems first: the whole point is that a bad model is seen immediately,
  // not hunted for among thirty green dots.
  const sorted = [...models].sort((a, b) => {
    const d = ORDER.indexOf(bucketOf(a)) - ORDER.indexOf(bucketOf(b));
    return d !== 0 ? d : a.id.localeCompare(b.id);
  });

  const counts = sorted.reduce<Record<Bucket, number>>((acc, m) => {
    const b = bucketOf(m);
    acc[b] = (acc[b] ?? 0) + 1;
    return acc;
  }, {} as Record<Bucket, number>);

  // The counts are written out, so the state is legible without seeing colour.
  const summary = ORDER.filter((b) => counts[b]).map((b) => `${counts[b]} ${STYLE[b].label}`).join(' · ');

  return (
    <div className="mb-3 rounded-xl border border-slate-800 bg-slate-900/60 px-4 py-3">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <h3 className="text-sm font-medium text-slate-300">Model status</h3>
        <p className="text-xs text-slate-400">{summary}</p>
      </div>

      {/* One row per model, named. An unlabelled dot answers "is something
          wrong" but not "which one" — and the second question is the one an
          operator has the moment the answer to the first is yes. */}
      <ul className="mt-3 grid gap-1 sm:grid-cols-2" aria-label="Model health">
        {sorted.map((m) => {
          const b = bucketOf(m);
          return (
            <li key={m.id}>
              <button
                type="button"
                onClick={() => onModelClick?.(m.id)}
                aria-label={`${m.id}: ${STYLE[b].label}`}
                className="flex w-full items-center gap-2 rounded-lg px-2 py-1.5 text-left transition hover:bg-slate-800 focus:outline-none focus:ring-2 focus:ring-blue-500"
              >
                <span className={`block h-2.5 w-2.5 shrink-0 rounded-full ${STYLE[b].dot}`} />
                <span className="min-w-0 flex-1 truncate font-mono text-xs text-slate-200" title={m.id}>{m.id}</span>
                <span className={`shrink-0 text-xs ${b === 'healthy' ? 'text-slate-500' : 'text-slate-300'}`}>
                  {STYLE[b].label}
                </span>
              </button>
            </li>
          );
        })}
      </ul>

    </div>
  );
}
