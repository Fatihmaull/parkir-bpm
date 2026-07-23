import type { Health, WsEvent } from "../types";
import { EventLog } from "./FieldMonitor";

type Ev = WsEvent & { t: number };

const rupiah = (n: number) => "Rp" + n.toLocaleString("id-ID");

function Card({ label, value, sub }: { label: string; value: string; sub?: string }) {
  return (
    <div className="card">
      <div className="label">{label}</div>
      <div className="value">{value}</div>
      {sub && <div className="sub">{sub}</div>}
    </div>
  );
}

export function Overview({ health, events }: { health: Health | null; events: Ev[] }) {
  const entered = events.filter((e) => e.event === "vehicle.entered").length;
  const exited = events.filter((e) => e.event === "vehicle.exited").length;
  const settled = events.filter((e) => e.event === "payment.settled");
  const revenue = settled.reduce((s, e) => s + Number((e.data as any)?.amount ?? 0), 0);
  const inside = Math.max(0, entered - exited);

  return (
    <div className="grid" style={{ gap: 18 }}>
      <div className="grid cards">
        <Card label="Kendaraan di Dalam" value={String(inside)} sub="masuk − keluar (sesi ini)" />
        <Card label="Transaksi Hari Ini" value={String(settled.length)} sub="pembayaran ter-settle" />
        <Card label="Pendapatan Hari Ini" value={rupiah(revenue)} sub="akumulasi settlement" />
        <Card label="Kendaraan Masuk" value={String(entered)} sub="event masuk" />
      </div>

      <div className="row-2">
        <div className="panel">
          <h3>Status Sistem</h3>
          <table>
            <tbody>
              <tr><td className="muted">Gerbang Masuk</td><td><span className={"badge " + (health?.gates.entry === "IDLE" ? "gray" : "amber")}><span className="dot" />{health?.gates.entry ?? "—"}</span></td></tr>
              <tr><td className="muted">Gerbang Keluar</td><td><span className={"badge " + (health?.gates.exit === "IDLE" ? "gray" : "amber")}><span className="dot" />{health?.gates.exit ?? "—"}</span></td></tr>
              <tr><td className="muted">Rantai Audit</td><td><span className={"badge " + (health?.chain.verified ? "green" : "red")}><span className="dot" />{health?.chain.verified ? "Terverifikasi" : "Rusak"} · {health?.chain.entries ?? 0} entri</span></td></tr>
              <tr><td className="muted">Sinkronisasi</td><td><span className="badge gray"><span className="dot" />{health?.sync.state ?? "—"} · {health?.sync.pending ?? 0} pending</span></td></tr>
              <tr><td className="muted">Node</td><td className="mono">{health?.node_id ?? "—"} ({health?.env})</td></tr>
              <tr><td className="muted">Subscriber WS</td><td>{health?.ws_subscribers ?? 0}</td></tr>
            </tbody>
          </table>
        </div>
        <div className="panel">
          <h3>Aktivitas Terbaru</h3>
          <EventLog events={events.slice(0, 40)} />
        </div>
      </div>
    </div>
  );
}
