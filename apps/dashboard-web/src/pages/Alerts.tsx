import { useEffect, useState } from "react";
import type { WsEvent } from "../types";

type Ev = WsEvent & { t: number };
interface Alert { id: string; t: number; type: string; severity: string; message: string; acked: boolean; }

// Alerts (§12.7) — dari event WS alert.raised, dengan acknowledge (client-side untuk demo).
export function Alerts({ events }: { events: Ev[] }) {
  const [acked, setAcked] = useState<Record<string, boolean>>({});
  const [items, setItems] = useState<Alert[]>([]);

  useEffect(() => {
    const raised = events
      .filter((e) => e.event === "alert.raised")
      .map((e, i): Alert => ({
        id: `${e.t}-${i}`, t: e.t,
        type: String((e.data as any)?.type ?? "ALERT"),
        severity: String((e.data as any)?.severity ?? "warning"),
        message: String((e.data as any)?.message ?? ""),
        acked: false,
      }));
    setItems(raised);
  }, [events]);

  const sev = (s: string) => (s === "critical" ? "red" : s === "warning" ? "amber" : "gray");

  return (
    <div className="panel">
      <h3>Alert Aktif ({items.filter((a) => !acked[a.id]).length})</h3>
      <table>
        <thead><tr><th>Waktu</th><th>Severity</th><th>Tipe</th><th>Pesan</th><th></th></tr></thead>
        <tbody>
          {items.length === 0 && <tr><td colSpan={5} className="muted">Tidak ada alert. Coba tembus palang (LD2 tanpa otorisasi) di Field Monitor.</td></tr>}
          {items.map((a) => (
            <tr key={a.id} style={{ opacity: acked[a.id] ? 0.45 : 1 }}>
              <td className="muted">{new Date(a.t).toLocaleTimeString("id-ID")}</td>
              <td><span className={"badge " + sev(a.severity)}><span className="dot" />{a.severity}</span></td>
              <td className="mono">{a.type}</td>
              <td>{a.message || "—"}</td>
              <td>{acked[a.id] ? <span className="muted">di-ack</span> : <button className="btn" onClick={() => setAcked((p) => ({ ...p, [a.id]: true }))}>ack</button>}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}
