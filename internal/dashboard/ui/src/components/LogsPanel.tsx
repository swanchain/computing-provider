import { useCallback, useEffect, useState } from 'react';
import { AlertTriangle, Bug, Info, RefreshCw, ScrollText, Search, XCircle } from 'lucide-react';
import { usePolling } from '../hooks/usePolling';
import { api } from '../api/client';
import type { LogEntry, LogLevel } from '../types';

// Each level carries a glyph as well as a colour, so it reads the same to a
// colour-blind operator and in a screenshot pasted into a chat.
const LEVELS: Array<{ id: LogLevel; label: string; icon: typeof Info; on: string; badge: string }> = [
  { id: 'error', label: 'Error', icon: XCircle, on: 'border-red-500/70 bg-red-950/50 text-red-200', badge: 'bg-red-500/10 text-red-300 ring-red-500/30' },
  { id: 'warning', label: 'Warning', icon: AlertTriangle, on: 'border-amber-500/70 bg-amber-950/40 text-amber-200', badge: 'bg-amber-500/10 text-amber-300 ring-amber-500/30' },
  { id: 'info', label: 'Info', icon: Info, on: 'border-blue-500/70 bg-blue-950/40 text-blue-200', badge: 'bg-blue-500/10 text-blue-300 ring-blue-500/30' },
  { id: 'debug', label: 'Debug', icon: Bug, on: 'border-slate-500/70 bg-slate-800 text-slate-200', badge: 'bg-slate-500/10 text-slate-400 ring-slate-500/30' },
];

const RANGES: Array<{ id: string; label: string; ms: number }> = [
  { id: '15m', label: '15m', ms: 15 * 60_000 },
  { id: '1h', label: '1h', ms: 60 * 60_000 },
  { id: '6h', label: '6h', ms: 6 * 60 * 60_000 },
  { id: '24h', label: '24h', ms: 24 * 60 * 60_000 },
  { id: '7d', label: '7d', ms: 7 * 24 * 60 * 60_000 },
];

const LIMITS = [200, 500, 1000];

function levelMeta(level: string) {
  switch (level) {
    case 'error':
    case 'fatal':
    case 'panic':
      return LEVELS[0];
    case 'warning':
      return LEVELS[1];
    case 'info':
      return LEVELS[2];
    default:
      return LEVELS[3];
  }
}

// datetime-local wants local wall time without a zone.
function toLocalInput(d: Date) {
  const pad = (n: number) => String(n).padStart(2, '0');
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}T${pad(d.getHours())}:${pad(d.getMinutes())}`;
}

function formatTime(iso: string) {
  const d = new Date(iso);
  const date = d.toLocaleDateString(undefined, { month: 'short', day: 'numeric' });
  const time = d.toLocaleTimeString(undefined, { hour: '2-digit', minute: '2-digit', second: '2-digit', hour12: false });
  return { date, time: `${time}.${String(d.getMilliseconds()).padStart(3, '0')}` };
}

function LevelBadge({ level }: { level: string }) {
  const meta = levelMeta(level);
  const Icon = meta.icon;
  return (
    <span className={`inline-flex items-center gap-1 rounded-full px-2 py-0.5 text-xs font-medium ring-1 ring-inset ${meta.badge}`}>
      <Icon aria-hidden="true" size={12} /> {level}
    </span>
  );
}

function LogRow({ entry }: { entry: LogEntry }) {
  const { date, time } = formatTime(entry.time);
  const meta = levelMeta(entry.level);
  const tint = meta.id === 'error' ? 'bg-red-950/10' : meta.id === 'warning' ? 'bg-amber-950/10' : '';
  return (
    <li className={`grid gap-1 px-4 py-2.5 sm:grid-cols-[9.5rem_6.5rem_1fr] sm:gap-3 ${tint}`}>
      <span className="font-mono text-xs text-slate-400" title={new Date(entry.time).toString()}>
        <span className="text-slate-500">{date} </span>{time}
      </span>
      <span><LevelBadge level={entry.level} /></span>
      <span className="min-w-0">
        <span className="block whitespace-pre-wrap break-words font-mono text-xs leading-5 text-slate-200">{entry.message}</span>
        {(entry.file || entry.func) && (
          <span className="mt-0.5 block truncate font-mono text-[11px] text-slate-500">
            {entry.func}{entry.func && entry.file ? ' · ' : ''}{entry.file}
          </span>
        )}
      </span>
    </li>
  );
}

export function LogsPanel() {
  const [levels, setLevels] = useState<LogLevel[]>(['error', 'warning']);
  const [range, setRange] = useState('1h');
  const [customFrom, setCustomFrom] = useState(() => toLocalInput(new Date(Date.now() - 60 * 60_000)));
  const [customTo, setCustomTo] = useState(() => toLocalInput(new Date()));
  const [search, setSearch] = useState('');
  const [query, setQuery] = useState('');
  const [limit, setLimit] = useState(LIMITS[0]);

  // Search as the operator types, without a request per keystroke.
  useEffect(() => {
    const id = window.setTimeout(() => setQuery(search.trim()), 300);
    return () => window.clearTimeout(id);
  }, [search]);

  const toggleLevel = (id: LogLevel) =>
    setLevels((current) => current.includes(id) ? current.filter((l) => l !== id) : [...current, id]);

  const custom = range === 'custom';
  const customError = custom && (!customFrom || !customTo || new Date(customFrom) >= new Date(customTo))
    ? 'The start must be before the end.'
    : '';

  const fetchLogs = useCallback(() => {
    if (levels.length === 0) return Promise.resolve({ entries: [], truncated: false, files: [] });
    let since: Date;
    let until: Date | undefined;
    if (custom) {
      since = new Date(customFrom);
      until = new Date(customTo);
    } else {
      // A preset range ends now, recomputed on every refresh.
      since = new Date(Date.now() - (RANGES.find((r) => r.id === range)?.ms ?? 60 * 60_000));
    }
    return api.getLogs({ levels, since, until, q: query || undefined, limit });
  }, [levels, custom, customFrom, customTo, range, query, limit]);

  // A preset follows the log as it grows; a fixed custom range does not change,
  // so polling it would only re-send the same answer.
  const live = !custom && !customError;
  const { data, error, loading, refetch } = usePolling(fetchLogs, live ? 10_000 : 0);

  const entries = customError ? [] : data?.entries ?? [];
  const counts = entries.reduce<Record<string, number>>((acc, e) => {
    const id = levelMeta(e.level).id;
    acc[id] = (acc[id] ?? 0) + 1;
    return acc;
  }, {});

  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div>
          <h1 className="text-2xl font-semibold text-white">Logs</h1>
          <p className="mt-1 text-sm text-slate-300">This node's own log, newest first.</p>
        </div>
        <button
          type="button"
          onClick={refetch}
          className="inline-flex min-h-10 min-w-10 items-center justify-center rounded-lg border border-slate-700 bg-slate-900 text-slate-300 hover:bg-slate-800 focus:outline-none focus:ring-2 focus:ring-blue-500"
          aria-label="Refresh logs"
        >
          <RefreshCw aria-hidden="true" size={16} className={loading ? 'animate-spin' : ''} />
        </button>
      </div>

      <div className="space-y-3 rounded-xl border border-slate-800 bg-slate-900 p-3 sm:p-4">
        <div className="flex flex-wrap items-center gap-2" role="group" aria-label="Log levels">
          {LEVELS.map((level) => {
            const on = levels.includes(level.id);
            const Icon = level.icon;
            return (
              <button
                key={level.id}
                type="button"
                aria-pressed={on}
                onClick={() => toggleLevel(level.id)}
                className={`inline-flex min-h-9 items-center gap-1.5 rounded-lg border px-3 text-sm focus:outline-none focus:ring-2 focus:ring-blue-500 ${on ? level.on : 'border-slate-700 text-slate-400 hover:bg-slate-800'}`}
              >
                <Icon aria-hidden="true" size={14} /> {level.label}
                {on && data && !customError && <span className="ml-0.5 tabular-nums opacity-80">{counts[level.id] ?? 0}</span>}
              </button>
            );
          })}
        </div>

        <div className="flex flex-wrap items-center gap-2">
          <div className="flex flex-wrap gap-1" role="group" aria-label="Time range">
            {RANGES.map((r) => (
              <button
                key={r.id}
                type="button"
                aria-pressed={range === r.id}
                onClick={() => setRange(r.id)}
                className={`min-h-9 rounded-lg border px-3 text-sm focus:outline-none focus:ring-2 focus:ring-blue-500 ${range === r.id ? 'border-blue-500 bg-blue-600 text-white' : 'border-slate-700 text-slate-300 hover:bg-slate-800'}`}
              >
                Last {r.label}
              </button>
            ))}
            <button
              type="button"
              aria-pressed={custom}
              onClick={() => setRange('custom')}
              className={`min-h-9 rounded-lg border px-3 text-sm focus:outline-none focus:ring-2 focus:ring-blue-500 ${custom ? 'border-blue-500 bg-blue-600 text-white' : 'border-slate-700 text-slate-300 hover:bg-slate-800'}`}
            >
              Custom
            </button>
          </div>
          {custom && (
            <div className="flex w-full flex-wrap items-center gap-2 sm:w-auto">
              <label className="sr-only" htmlFor="logs-from">From</label>
              <input id="logs-from" type="datetime-local" value={customFrom} onChange={(e) => setCustomFrom(e.target.value)} className="min-h-9 min-w-0 flex-1 rounded-lg border border-slate-700 bg-slate-950 px-2 text-sm text-slate-200 [color-scheme:dark] focus:border-blue-500 focus:outline-none sm:flex-none" />
              <span className="text-sm text-slate-500">to</span>
              <label className="sr-only" htmlFor="logs-to">To</label>
              <input id="logs-to" type="datetime-local" value={customTo} onChange={(e) => setCustomTo(e.target.value)} className="min-h-9 min-w-0 flex-1 rounded-lg border border-slate-700 bg-slate-950 px-2 text-sm text-slate-200 [color-scheme:dark] focus:border-blue-500 focus:outline-none sm:flex-none" />
            </div>
          )}
        </div>

        <div className="flex flex-wrap items-center gap-2">
          <div className="relative min-w-0 flex-1">
            <Search aria-hidden="true" size={15} className="pointer-events-none absolute left-3 top-1/2 -translate-y-1/2 text-slate-500" />
            <label className="sr-only" htmlFor="logs-search">Search log messages</label>
            <input
              id="logs-search"
              type="search"
              value={search}
              onChange={(e) => setSearch(e.target.value)}
              placeholder="Search messages, e.g. a model ID or request ID"
              className="min-h-9 w-full rounded-lg border border-slate-700 bg-slate-950 pl-9 pr-3 text-sm text-slate-200 placeholder:text-slate-500 focus:border-blue-500 focus:outline-none focus:ring-2 focus:ring-blue-500/20"
            />
          </div>
          <label htmlFor="logs-limit" className="text-xs text-slate-400">Show</label>
          <select id="logs-limit" value={limit} onChange={(e) => setLimit(Number(e.target.value))} className="min-h-9 rounded-lg border border-slate-700 bg-slate-950 px-2 text-sm text-slate-200 focus:border-blue-500 focus:outline-none">
            {LIMITS.map((n) => <option key={n} value={n}>{n}</option>)}
          </select>
        </div>
      </div>

      <div className="overflow-hidden rounded-xl border border-slate-800 bg-slate-900">
        {customError ? (
          <p className="px-4 py-10 text-center text-sm text-amber-300">{customError}</p>
        ) : levels.length === 0 ? (
          <p className="px-4 py-10 text-center text-sm text-slate-400">Choose at least one level.</p>
        ) : loading && !data ? (
          <div className="animate-pulse space-y-2 p-4" role="status" aria-label="Loading logs">
            {[...Array(8)].map((_, i) => <div key={i} className="h-8 rounded bg-slate-800" />)}
          </div>
        ) : error && !data ? (
          <div className="px-4 py-10 text-center">
            <XCircle aria-hidden="true" size={28} className="mx-auto mb-2 text-red-400" />
            <p className="font-medium text-red-200">Logs are unavailable</p>
            <p className="mt-1 text-sm text-slate-400">{error.message}</p>
          </div>
        ) : entries.length === 0 ? (
          <div className="px-4 py-10 text-center text-slate-400">
            <ScrollText aria-hidden="true" size={28} className="mx-auto mb-2 text-slate-600" />
            <p className="font-medium text-slate-300">Nothing logged</p>
            <p className="mt-1 text-sm">No {levels.map((l) => levelMeta(l).label.toLowerCase()).join(', ')} lines{query ? ` matching "${query}"` : ''} in this range.</p>
          </div>
        ) : (
          <ol className="divide-y divide-slate-800/80">
            {entries.map((entry, i) => <LogRow key={`${entry.time}-${i}`} entry={entry} />)}
          </ol>
        )}
      </div>

      {!customError && data && (
        <div className="space-y-1 text-xs text-slate-400" aria-live="polite">
          <p>
            {entries.length.toLocaleString()} {entries.length === 1 ? 'line' : 'lines'}
            {data.truncated ? ` shown; there are more in this range${data.partial ? ` (${data.partial})` : '. Narrow the range or raise the limit to see them'}.` : '.'}
            {' '}{live ? 'Refreshes every 10 seconds.' : 'Fixed range; not refreshing.'}
          </p>
          {data.files.length > 0 && <p className="break-all font-mono text-[11px] text-slate-500">Read from {data.files.join(', ')}</p>}
        </div>
      )}
    </div>
  );
}
