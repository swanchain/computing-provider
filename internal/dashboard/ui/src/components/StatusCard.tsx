import type { ReactNode } from 'react';

interface StatusCardProps {
  title: string;
  value: string | number;
  subtitle?: string;
  icon?: ReactNode;
  trend?: 'up' | 'down' | 'neutral';
  color?: 'green' | 'red' | 'yellow' | 'blue' | 'gray';
}

const colorClasses = {
  green: 'text-green-400',
  red: 'text-red-400',
  yellow: 'text-yellow-400',
  blue: 'text-blue-400',
  gray: 'text-gray-400',
};

export function StatusCard({ title, value, subtitle, icon, color = 'blue' }: StatusCardProps) {
  return (
    <div className="min-w-0 rounded-xl border border-slate-700 bg-slate-900 p-3 sm:p-4">
      <div className="mb-2 flex items-center justify-between gap-2">
        <span className="truncate text-xs text-slate-400 sm:text-sm">{title}</span>
        {icon && <span className={colorClasses[color]}>{icon}</span>}
      </div>
      <div className={`truncate text-lg font-bold sm:text-2xl ${colorClasses[color]}`} title={String(value)}>{value}</div>
      {subtitle && <div className="mt-1 truncate text-[11px] text-slate-500 sm:text-xs" title={subtitle}>{subtitle}</div>}
    </div>
  );
}
