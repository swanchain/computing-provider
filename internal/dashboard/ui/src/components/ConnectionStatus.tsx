import { Wifi, WifiOff, RefreshCcw } from 'lucide-react';
import type { ConnectionStatus as ConnectionStatusType } from '../types';

interface ConnectionStatusProps {
  status: ConnectionStatusType | null;
  loading: boolean;
}

export function ConnectionStatus({ status, loading }: ConnectionStatusProps) {
  if (loading || !status) {
    return (
      <div className="flex items-center gap-2 px-3 py-2 bg-slate-700/50 rounded-lg">
        <div className="w-3 h-3 bg-slate-600 rounded-full animate-pulse"></div>
        <span className="text-sm text-slate-400">Connecting...</span>
      </div>
    );
  }

  return (
    <div className={`flex items-center gap-3 px-3 py-2 rounded-lg ${
      status.connected ? 'bg-green-900/20 border border-green-800' : 'bg-red-900/20 border border-red-800'
    }`}>
      <div className="flex items-center gap-2">
        {status.connected ? (
          <>
            <Wifi size={16} className="text-green-400" />
            <span className="text-sm text-green-400">Connected</span>
          </>
        ) : (
          <>
            <WifiOff size={16} className="text-red-400" />
            <span className="text-sm text-red-400">Disconnected</span>
          </>
        )}
      </div>

      {status.reconnect_attempts > 0 && !status.connected && (
        <div className="flex items-center gap-1 text-xs text-yellow-400">
          <RefreshCcw size={12} className="animate-spin" />
          <span>Retry {status.reconnect_attempts}</span>
        </div>
      )}

      <div className="text-xs text-slate-500 ml-auto">
        {status.node_id.substring(0, 8)}...
      </div>
    </div>
  );
}
