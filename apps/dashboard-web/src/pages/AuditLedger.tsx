import type { Health, WsEvent } from "../types";

type Ev = WsEvent & { t: number };

// Audit Ledger (§12.12). Rantai penuh tersedia via cloud-api saat sync aktif; di mode Edge
// halaman ini menampilkan status verifikasi rantai + aliran peristiwa audit-relevan real-time.
const AUDIT_EVENTS = new Set([
  "vehicle.entered", "vehicle.exited", "payment.settled", "alert.raised",
]);

export function AuditLedger({ health, events }: { health: Health | null; events: Ev[] }) {
  const audit = events.filter((e) => AUDIT_EVENTS.has(e.event));
  return (
    <div className="grid" style={{ gap: 18 }}>
      <div className="panel">
        <h3>Status Rantai Hash (§9)</h3>
        <div className="btn-row" style={{ alignItems: "center" }}>
          <span className={"badge " + (health?.chain.verified ? "green" : "red")}>
            <span className="dot" /> {health?.chain.verified ? "Rantai Terverifikasi" : "RANTAI RUSAK"}
          </span>
          <span className="muted">{health?.chain.entries ?? 0} entri pada node <span className="mono">{health?.node_id}</span></span>
        </div>
        <p className="muted" style={{ fontSize: 12, marginBottom: 0 }}>
          Rantai per-node (append-only). Verifikasi penuh & ekspor CSV tersedia lewat cloud-api saat
          sinkronisasi aktif. Setiap entri di-hash SHA-256 berantai; modifikasi terdeteksi otomatis.
        </p>
      </div>
      <div className="panel">
        <h3>Peristiwa Audit-Relevan (real-time)</h3>
        <table>
          <thead><tr><th>Waktu</th><th>Peristiwa</th><th>Detail</th></tr></thead>
          <tbody>
            {audit.length === 0 && <tr><td colSpan={3} className="muted">Belum ada peristiwa.</td></tr>}
            {audit.map((e, i) => (
              <tr key={i}>
                <td className="muted">{new Date(e.t).toLocaleTimeString("id-ID")}</td>
                <td className="ev mono">{e.event}</td>
                <td className="mono muted">{e.data ? JSON.stringify(e.data) : ""}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </div>
  );
}
