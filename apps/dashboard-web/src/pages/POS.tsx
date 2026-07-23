import { useEffect, useState } from "react";
import { post } from "../api";
import type { WsEvent } from "../types";

type Ev = WsEvent & { t: number };

interface Active { id: string; ticket: string; vehicle_type: string; plate: string; entry_time: string; is_member: boolean; }

const rupiah = (n: number) => "Rp" + n.toLocaleString("id-ID");

function exitState(events: Ev[], fallback: string) {
  const e = events.find((x) => x.event === "gate.state_changed" && (x.data as any)?.gate === "exit");
  return ((e?.data as any)?.to as string) ?? fallback;
}
function lastAmount(events: Ev[]): number | null {
  const e = events.find((x) => x.event === "exit.transaction_found" || x.event === "exit.fare_calculated");
  const a = (e?.data as any)?.amount;
  return a == null ? null : Number(a);
}

const METHODS = ["CASH", "EDC_EMONEY", "EDC_DEBIT", "QRIS", "EWALLET"];
const OFFLINE_DISABLED = ["QRIS", "EWALLET"];

export function POS({ events, exit, online }: { events: Ev[]; exit: string; online: boolean }) {
  const [active, setActive] = useState<Active[]>([]);
  const [ticket, setTicket] = useState("");
  const [tendered, setTendered] = useState(20000);
  const state = exitState(events, exit);
  const amount = lastAmount(events);

  const refresh = () => fetch("/api/v1/active").then((r) => r.json()).then((d) => setActive(d.active ?? [])).catch(() => {});
  useEffect(() => { refresh(); }, [events.length]);

  const call = (path: string, body?: unknown) => post(path, body).catch(console.error);

  return (
    <div className="row-2">
      <div className="panel gate">
        <h3>Panel Kasir — Gerbang Keluar</h3>
        <div className={"state-chip state-" + state}>{state}</div>
        {amount != null && (
          <div className="card" style={{ padding: "12px 16px" }}>
            <div className="label">Tarif</div>
            <div className="value">{rupiah(amount)}</div>
          </div>
        )}

        <div className="muted" style={{ fontSize: 12 }}>Langkah alur keluar (§5.5 / §12.9)</div>
        <div className="btn-row">
          <button className="btn" onClick={() => call("/api/v1/sim/exit/loop", { loop: "pre", high: true })}>① LD3 ↑ (kendaraan tiba)</button>
        </div>

        <div className="muted" style={{ fontSize: 12, marginTop: 6 }}>② Identifikasi via tiket</div>
        <div className="btn-row">
          <input className="select" placeholder="TKT-000001" value={ticket} onChange={(e) => setTicket(e.target.value)} style={{ width: 150 }} />
          <button className="btn primary" onClick={() => call("/api/v1/sim/exit/identify", { ticket })}>Cari Transaksi</button>
        </div>

        <div className="muted" style={{ fontSize: 12, marginTop: 6 }}>③ Komparasi foto (masuk vs live)</div>
        <div className="btn-row">
          <button className="btn ok" onClick={() => call("/api/v1/sim/exit/photo", { match: true })}>✓ Foto Cocok</button>
          <button className="btn danger" onClick={() => call("/api/v1/sim/exit/photo", { match: false })}>✗ Tidak Cocok</button>
        </div>

        <div className="muted" style={{ fontSize: 12, marginTop: 6 }}>④ Metode pembayaran {online ? "" : "(offline: QRIS/e-wallet nonaktif)"}</div>
        <div className="btn-row">
          {METHODS.map((m) => {
            const disabled = !online && OFFLINE_DISABLED.includes(m);
            return (
              <button key={m} className="btn" disabled={disabled}
                title={disabled ? "Tidak tersedia — mode offline" : ""}
                onClick={() => call("/api/v1/sim/exit/pay", { method: m, tendered: m === "CASH" ? tendered : 0 })}>
                {m}
              </button>
            );
          })}
        </div>
        <div className="btn-row">
          <span className="muted" style={{ fontSize: 12, alignSelf: "center" }}>Tunai diterima:</span>
          <input className="select" type="number" value={tendered} onChange={(e) => setTendered(Number(e.target.value))} style={{ width: 120 }} />
        </div>

        <div className="muted" style={{ fontSize: 12, marginTop: 6 }}>⑤ Kendaraan lewat</div>
        <div className="btn-row">
          <button className="btn" onClick={() => call("/api/v1/sim/exit/loop", { loop: "pre", high: false })}>LD3 ↓</button>
          <button className="btn" onClick={() => call("/api/v1/sim/exit/loop", { loop: "post", high: true })}>LD4 ↑</button>
          <button className="btn" onClick={() => call("/api/v1/sim/exit/loop", { loop: "post", high: false })}>LD4 ↓ → tutup</button>
        </div>
      </div>

      <div className="panel">
        <h3>Kendaraan di Dalam ({active.length})</h3>
        <table>
          <thead><tr><th>Tiket</th><th>Jenis</th><th>Masuk</th><th></th></tr></thead>
          <tbody>
            {active.length === 0 && <tr><td colSpan={4} className="muted">Belum ada kendaraan. Jalankan siklus masuk di Field Monitor.</td></tr>}
            {active.map((v) => (
              <tr key={v.id}>
                <td className="mono">{v.ticket || (v.is_member ? "MEMBER" : "—")}</td>
                <td>{v.vehicle_type}</td>
                <td className="muted">{new Date(v.entry_time).toLocaleTimeString("id-ID")}</td>
                <td><button className="btn" onClick={() => setTicket(v.ticket)}>pilih</button></td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </div>
  );
}
