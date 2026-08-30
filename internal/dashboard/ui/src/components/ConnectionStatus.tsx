import { Wifi, WifiOff, AlertTriangle } from 'lucide-react';
import type { ConnectionStatus as ConnectionStatusType } from '../types';

interface ConnectionStatusProps {
  status: ConnectionStatusType | null;
  loading: boolean;
  error?: Error | null;
}

export function ConnectionStatus({ status, loading, error }: ConnectionStatusProps) {
  if (loading) {
    return (
      <div className="flex min-h-10 items-center gap-2 rounded-lg bg-slate-700/50 px-3 py-2">
        <div className="w-3 h-3 bg-slate-600 rounded-full animate-pulse"></div>
        <span className="hidden text-sm text-slate-400 sm:inline">Connecting…</span>
      </div>
    );
  }

  if (!status && error) {
    return (
      <div className="flex min-h-10 items-center gap-2 rounded-lg border border-amber-800 bg-amber-900/20 px-3 py-2">
        <AlertTriangle size={16} className="text-amber-400" />
        <span className="hidden text-sm text-amber-400 sm:inline">API unreachable</span>
      </div>
    );
  }

  if (!status) {
    return (
      <div className="flex min-h-10 items-center gap-2 rounded-lg bg-slate-700/50 px-3 py-2">
        <WifiOff size={16} className="text-slate-400" />
        <span className="hidden text-sm text-slate-400 sm:inline">No data</span>
      </div>
    );
  }

  return (
    <div className={`flex min-h-10 items-center gap-2 rounded-lg px-2.5 py-2 sm:gap-3 sm:px-3 ${
      status.connected ? 'bg-green-900/20 border border-green-800' : 'bg-red-900/20 border border-red-800'
    }`}>
      <div className="flex items-center gap-2">
        {status.connected ? (
          <>
            <Wifi size={16} className="text-green-400" />
            <span className="hidden text-sm text-green-400 sm:inline">Connected</span>
          </>
        ) : (
          <>
            <WifiOff size={16} className="text-red-400" />
            <span className="hidden text-sm text-red-400 sm:inline">Disconnected</span>
          </>
        )}
      </div>

      {status.active_models && status.active_models.length > 0 && (
        <div className="ml-auto hidden text-xs text-slate-400 md:block">
          {status.active_models.length} model{status.active_models.length > 1 ? 's' : ''}
        </div>
      )}
    </div>
  );
}
