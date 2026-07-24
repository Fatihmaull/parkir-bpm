import { useFetch } from "../api";

interface Slot { vehicle_type: string; capacity: number; occupied: number; }

export function Slots({ tick }: { tick: number }) {
  const data = useFetch<{ slots: Slot[] }>("/api/v1/slots", tick, 3000);
  const slots = data?.slots ?? [];
  return (
    <div className="grid cards">
      {slots.map((s) => {
        const pct = s.capacity ? Math.round((s.occupied / s.capacity) * 100) : 0;
        const warn = pct > 90;
        return (
          <div className="card" key={s.vehicle_type}>
            <div className="label">{s.vehicle_type}</div>
            <div className="value">{s.occupied}<span className="muted" style={{ fontSize: 16 }}> / {s.capacity}</span></div>
            <div style={{ height: 10, background: "var(--panel-2)", borderRadius: 6, marginTop: 8 }}>
              <div style={{ width: `${pct}%`, height: "100%", background: warn ? "var(--red)" : "var(--green)", borderRadius: 6 }} />
            </div>
            <div className="sub">{pct}% terisi {warn && <span style={{ color: "var(--red)" }}>· hampir penuh</span>}</div>
          </div>
        );
      })}
      {slots.length === 0 && <div className="muted">Memuat…</div>}
    </div>
  );
}
