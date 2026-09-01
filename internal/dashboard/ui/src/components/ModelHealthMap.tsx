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
const STYLE: Record<Bucket, { dot: string; label: string; swatch: string }> = {
  healthy:  { dot: 'bg-emerald-400',                              label: 'healthy',  swatch: 'bg-emerald-400' },
  degraded: { dot: 'bg-amber-400 ring-2 ring-amber-400/40',       label: 'degraded', swatch: 'bg-amber-400' },
  offline:  { dot: 'bg-red-500 ring-2 ring-red-500/40',           label: 'offline',  swatch: 'bg-red-500' },
  disabled: { dot: 'bg-transparent border-2 border-slate-600',    label: 'disabled', swatch: 'border-2 border-slate-600' },
  unknown:  { dot: 'bg-slate-600',                                label: 'unknown',  swatch: 'bg-slate-600' },
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

      <ul className="mt-3 flex flex-wrap gap-2" aria-label="Model health">
        {sorted.map((m) => {
          const b = bucketOf(m);
          const text = `${m.id}: ${STYLE[b].label}`;
          return (
            <li key={m.id}>
              <button
                type="button"
                onClick={() => onModelClick?.(m.id)}
                title={text}
                aria-label={text}
                className="flex h-6 w-6 items-center justify-center rounded-full transition hover:bg-slate-800 focus:outline-none focus:ring-2 focus:ring-blue-500"
              >
                <span className={`block h-3 w-3 rounded-full ${STYLE[b].dot}`} />
              </button>
            </li>
          );
        })}
      </ul>

      <ul className="mt-3 flex flex-wrap gap-x-4 gap-y-1 text-xs text-slate-400">
        {ORDER.filter((b) => counts[b]).map((b) => (
          <li key={b} className="flex items-center gap-1.5">
            <span className={`block h-2.5 w-2.5 rounded-full ${STYLE[b].swatch}`} />
            {STYLE[b].label}
          </li>
        ))}
      </ul>
    </div>
  );
}
