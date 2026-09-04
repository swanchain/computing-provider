import { Wifi, WifiOff, AlertTriangle } from 'lucide-react';
import type { ConnectionStatus as ConnectionStatusType } from '../types';

interface ConnectionStatusProps {
  status: ConnectionStatusType | null;
  loading: boolean;
  error?: Error | null;
  lastUpdated?: Date | null;
}

function updatedLabel(updated: Date | null | undefined) {
  if (!updated) return '';
  return `Updated ${updated.toLocaleTimeString(undefined, { hour: 'numeric', minute: '2-digit', second: '2-digit' })}`;
}

export function ConnectionStatus({ status, loading, error, lastUpdated }: ConnectionStatusProps) {
  if (loading) {
    return (
      <div className="flex min-h-10 items-center gap-2 rounded-lg bg-slate-700/50 px-3 py-2">
        <div className="w-3 h-3 bg-slate-600 rounded-full animate-pulse"></div>
        <span className="text-sm text-slate-300">Connecting…</span>
      </div>
    );
  }

  if (!status && error) {
    return (
      <div className="flex min-h-10 items-center gap-2 rounded-lg border border-amber-800 bg-amber-900/20 px-3 py-2">
        <AlertTriangle aria-hidden="true" size={16} className="text-amber-300" />
        <span className="text-sm text-amber-200">API unavailable</span>
      </div>
    );
  }

  if (!status) {
    return (
      <div className="flex min-h-10 items-center gap-2 rounded-lg bg-slate-700/50 px-3 py-2">
        <WifiOff aria-hidden="true" size={16} className="text-slate-300" />
        <span className="text-sm text-slate-300">No data</span>
      </div>
    );
  }

  return (
    <div
      title={error?.message}
      className={`flex min-h-10 items-center gap-2 rounded-lg border px-2.5 py-2 sm:gap-3 sm:px-3 ${
        error
          ? 'border-amber-800 bg-amber-900/20'
          : status.connected
            ? 'border-green-800 bg-green-900/20'
            : 'border-red-800 bg-red-900/20'
      }`}
    >
      <div className="flex items-center gap-2">
        {error ? (
          <>
            <AlertTriangle aria-hidden="true" size={16} className="text-amber-300" />
            <span className="text-sm font-medium text-amber-200">Stale</span>
          </>
        ) : status.connected ? (
          <>
            <Wifi aria-hidden="true" size={16} className="text-green-400" />
            <span className="text-sm font-medium text-green-300">Connected</span>
          </>
        ) : (
          <>
            <WifiOff aria-hidden="true" size={16} className="text-red-400" />
            <span className="text-sm font-medium text-red-300">Disconnected</span>
          </>
        )}
      </div>

      {!error && (
        <div className="ml-auto hidden text-xs text-slate-300 md:block">
          {updatedLabel(lastUpdated)}
          {status.active_models?.length > 0 && ` · ${status.active_models.length} model${status.active_models.length > 1 ? 's' : ''}`}
        </div>
      )}
    </div>
  );
}
