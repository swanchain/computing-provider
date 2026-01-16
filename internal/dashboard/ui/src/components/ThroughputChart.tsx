import { useEffect, useState } from 'react';
import { AreaChart, Area, XAxis, YAxis, Tooltip, ResponsiveContainer, CartesianGrid } from 'recharts';
import type { InferenceMetrics } from '../types';

interface ThroughputChartProps {
  metrics: InferenceMetrics | null;
}

interface DataPoint {
  time: string;
  requests: number;
  tokensPerSec: number;
}

const MAX_POINTS = 30;

export function ThroughputChart({ metrics }: ThroughputChartProps) {
  const [data, setData] = useState<DataPoint[]>([]);
  const [lastTotal, setLastTotal] = useState(0);

  useEffect(() => {
    if (!metrics) return;

    const now = new Date();
    const timeStr = `${now.getMinutes().toString().padStart(2, '0')}:${now.getSeconds().toString().padStart(2, '0')}`;

    setData((prev) => {
      // Calculate requests per interval (5 seconds)
      const requestsDelta = lastTotal > 0 ? metrics.total_requests - lastTotal : 0;
      const reqPerSec = requestsDelta / 5; // 5 second interval

      const newPoint: DataPoint = {
        time: timeStr,
        requests: Math.max(0, reqPerSec),
        tokensPerSec: metrics.tokens_per_second,
      };
      const updated = [...prev, newPoint];
      return updated.slice(-MAX_POINTS);
    });

    setLastTotal(metrics.total_requests);
  }, [metrics, lastTotal]);

  if (data.length < 2) {
    return (
      <div className="bg-slate-800 rounded-lg p-4 border border-slate-700">
        <h3 className="text-lg font-semibold text-slate-200 mb-4">Throughput</h3>
        <div className="h-48 flex items-center justify-center text-slate-500">
          Collecting data...
        </div>
      </div>
    );
  }

  return (
    <div className="bg-slate-800 rounded-lg p-4 border border-slate-700">
      <h3 className="text-lg font-semibold text-slate-200 mb-4">Throughput</h3>
      <div className="h-48">
        <ResponsiveContainer width="100%" height="100%">
          <AreaChart data={data}>
            <defs>
              <linearGradient id="colorReqs" x1="0" y1="0" x2="0" y2="1">
                <stop offset="5%" stopColor="#3b82f6" stopOpacity={0.3} />
                <stop offset="95%" stopColor="#3b82f6" stopOpacity={0} />
              </linearGradient>
            </defs>
            <CartesianGrid strokeDasharray="3 3" stroke="#334155" />
            <XAxis
              dataKey="time"
              stroke="#64748b"
              fontSize={10}
              tickLine={false}
            />
            <YAxis
              stroke="#64748b"
              fontSize={10}
              tickLine={false}
            />
            <Tooltip
              contentStyle={{
                backgroundColor: '#1e293b',
                border: '1px solid #334155',
                borderRadius: '6px',
                fontSize: '12px',
              }}
              labelStyle={{ color: '#94a3b8' }}
              formatter={(value, name) => [
                typeof value === 'number' ? value.toFixed(1) : value,
                name === 'requests' ? 'Req/s' : 'Tok/s'
              ]}
            />
            <Area
              type="monotone"
              dataKey="requests"
              stroke="#3b82f6"
              fillOpacity={1}
              fill="url(#colorReqs)"
              name="requests"
            />
          </AreaChart>
        </ResponsiveContainer>
      </div>
      <div className="flex justify-center gap-4 mt-2 text-xs">
        <div className="flex items-center gap-1">
          <div className="w-3 h-3 bg-blue-500 rounded"></div>
          <span className="text-slate-400">Requests/sec</span>
        </div>
      </div>
    </div>
  );
}
