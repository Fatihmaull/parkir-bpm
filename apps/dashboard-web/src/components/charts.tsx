// Chart SVG ringan (tanpa dependency). Warna via palet indigo/sky/emerald/amber/rose.
const PALETTE = ["#6366f1", "#0ea5e9", "#10b981", "#f59e0b", "#f43f5e", "#8b5cf6"];

export interface Slice { label: string; value: number; }

export function Donut({ data, size = 150 }: { data: Slice[]; size?: number }) {
  const total = data.reduce((s, d) => s + d.value, 0);
  const r = size / 2 - 12;
  const c = size / 2;
  const circ = 2 * Math.PI * r;
  let offset = 0;
  return (
    <div className="flex items-center gap-4">
      <svg width={size} height={size} viewBox={`0 0 ${size} ${size}`}>
        <circle cx={c} cy={c} r={r} fill="none" stroke="currentColor" strokeWidth="16" className="text-slate-200 dark:text-slate-800" />
        {total > 0 && data.map((d, i) => {
          const frac = d.value / total;
          const dash = frac * circ;
          const el = (
            <circle key={i} cx={c} cy={c} r={r} fill="none" stroke={PALETTE[i % PALETTE.length]}
              strokeWidth="16" strokeDasharray={`${dash} ${circ - dash}`}
              strokeDashoffset={-offset} transform={`rotate(-90 ${c} ${c})`} />
          );
          offset += dash;
          return el;
        })}
        <text x={c} y={c} textAnchor="middle" dominantBaseline="central" className="fill-slate-900 dark:fill-white" fontSize="20" fontWeight="800">{total}</text>
      </svg>
      <div className="space-y-1.5">
        {data.map((d, i) => (
          <div key={i} className="flex items-center gap-2 text-xs">
            <span className="h-2.5 w-2.5 rounded-sm" style={{ background: PALETTE[i % PALETTE.length] }} />
            <span className="text-slate-600 dark:text-slate-300">{d.label}</span>
            <span className="ml-auto font-semibold tabular-nums">{d.value}</span>
          </div>
        ))}
        {total === 0 && <div className="text-xs text-slate-400">Belum ada data.</div>}
      </div>
    </div>
  );
}

export function Bars({ data, unit = "" }: { data: Slice[]; unit?: string }) {
  const max = Math.max(1, ...data.map((d) => d.value));
  return (
    <div className="space-y-2.5">
      {data.map((d, i) => (
        <div key={i} className="text-xs">
          <div className="mb-1 flex justify-between">
            <span className="text-slate-600 dark:text-slate-300">{d.label}</span>
            <span className="font-semibold tabular-nums">{d.value.toLocaleString("id-ID")}{unit}</span>
          </div>
          <div className="h-2.5 overflow-hidden rounded-full bg-slate-100 dark:bg-slate-800">
            <div className="h-full rounded-full" style={{ width: `${(d.value / max) * 100}%`, background: PALETTE[i % PALETTE.length] }} />
          </div>
        </div>
      ))}
      {data.length === 0 && <div className="text-xs text-slate-400">Belum ada data.</div>}
    </div>
  );
}
