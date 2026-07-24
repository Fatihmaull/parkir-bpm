import { useFetch } from "../api";

interface Txn { vehicle_type: string; entry_time: string; }

const COLORS: Record<string, string> = { mobil: "#3b82f6", motor: "#22c55e", truk: "#f59e0b", bus: "#a855f7" };

export function Volume({ tick }: { tick: number }) {
  const data = useFetch<{ transactions: Txn[] }>("/api/v1/transactions", tick);
  const txns = data?.transactions ?? [];

  const byType: Record<string, number> = {};
  for (const t of txns) byType[t.vehicle_type] = (byType[t.vehicle_type] || 0) + 1;
  const max = Math.max(1, ...Object.values(byType));

  const byHour = new Array(24).fill(0);
  for (const t of txns) byHour[new Date(t.entry_time).getHours()]++;
  const maxHour = Math.max(1, ...byHour);

  return (
    <div className="grid" style={{ gap: 18 }}>
      <div className="panel">
        <h3>Distribusi Jenis Kendaraan</h3>
        {Object.keys(byType).length === 0 && <div className="muted">Belum ada data.</div>}
        {Object.entries(byType).map(([t, n]) => (
          <div key={t} style={{ margin: "10px 0" }}>
            <div style={{ display: "flex", justifyContent: "space-between", fontSize: 13, marginBottom: 4 }}>
              <span>{t}</span><span className="muted">{n}</span>
            </div>
            <div style={{ height: 12, background: "var(--panel-2)", borderRadius: 6 }}>
              <div style={{ width: `${(n / max) * 100}%`, height: "100%", background: COLORS[t] || "#3b82f6", borderRadius: 6 }} />
            </div>
          </div>
        ))}
      </div>
      <div className="panel">
        <h3>Volume per Jam (kalibrasi peak_windows §12.3)</h3>
        <div style={{ display: "flex", alignItems: "flex-end", gap: 3, height: 160 }}>
          {byHour.map((n, h) => (
            <div key={h} style={{ flex: 1, display: "flex", flexDirection: "column", alignItems: "center", gap: 3 }}>
              <div title={`${h}:00 — ${n}`} style={{ width: "100%", height: `${(n / maxHour) * 130}px`, minHeight: 2, background: n ? "#3b82f6" : "var(--panel-2)", borderRadius: 3 }} />
              <span className="muted" style={{ fontSize: 9 }}>{h}</span>
            </div>
          ))}
        </div>
      </div>
    </div>
  );
}
