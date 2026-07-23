import { useState } from "react";
import { post } from "../api";
import type { WsEvent } from "../types";

type Ev = WsEvent & { t: number };

function gateState(events: Ev[], gate: string, fallback?: string): string {
  const e = events.find((x) => x.event === "gate.state_changed" && (x.data as any)?.gate === gate);
  return ((e?.data as any)?.to as string) ?? fallback ?? "—";
}

const GREEN = ["OPENING", "OPEN", "CLEARING"];
const FAULT = ["FAULT", "REJECTED", "LOCKED_NO_PAPER", "DISPUTE_HOLD"];

function Lamps({ state }: { state: string }) {
  const green = GREEN.includes(state);
  const fault = FAULT.includes(state);
  return (
    <div className="lamp-row">
      <div className={"lamp " + (!green ? "on-red" : "")}>MERAH{fault ? " ⚠" : ""}</div>
      <div className={"lamp " + (green ? "on-green" : "")}>HIJAU</div>
    </div>
  );
}

export function FieldMonitor({ events, entry, exit }: { events: Ev[]; entry: string; exit: string }) {
  const [uid, setUid] = useState("04AABBCC");
  const [busy, setBusy] = useState(false);
  const entryState = gateState(events, "entry", entry);
  const exitState = gateState(events, "exit", exit);

  const call = async (path: string, body?: unknown) => {
    setBusy(true);
    try {
      await post(path, body);
    } catch (e) {
      console.error(e);
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className="grid" style={{ gap: 18 }}>
      <div className="row-2">
        {/* ── Gerbang Masuk ── */}
        <div className="panel gate">
          <h3>Gerbang Masuk · GATE-IN-01</h3>
          <div className={"state-chip state-" + entryState}>{entryState}</div>
          <Lamps state={entryState} />
          <div className="muted" style={{ fontSize: 12 }}>Kontrol simulator perangkat (§12.8)</div>
          <div className="btn-row">
            <button className="btn" disabled={busy} onClick={() => call("/api/v1/sim/entry/loop", { loop: "pre", high: true })}>① LD1 ↑ (kendaraan tiba)</button>
            <button className="btn primary" disabled={busy} onClick={() => call("/api/v1/sim/entry/button")}>② Tekan Tombol Tiket</button>
            <button className="btn ok" disabled={busy} onClick={() => call("/api/v1/sim/entry/ticket-taken")}>③ Ambil Tiket → buka</button>
            <button className="btn" disabled={busy} onClick={() => call("/api/v1/sim/entry/loop", { loop: "pre", high: false })}>④ LD1 ↓</button>
            <button className="btn" disabled={busy} onClick={() => call("/api/v1/sim/entry/loop", { loop: "post", high: true })}>⑤ LD2 ↑ (lewat)</button>
            <button className="btn" disabled={busy} onClick={() => call("/api/v1/sim/entry/loop", { loop: "post", high: false })}>⑥ LD2 ↓ → tutup</button>
          </div>
          <div className="btn-row" style={{ marginTop: 4 }}>
            <input className="select" value={uid} onChange={(e) => setUid(e.target.value)} style={{ width: 130 }} />
            <button className="btn" disabled={busy} onClick={() => call("/api/v1/sim/entry/rfid", { uid })}>Tap RFID Member</button>
          </div>
        </div>

        {/* ── Gerbang Keluar (ringkas) ── */}
        <div className="panel gate">
          <h3>Gerbang Keluar · GATE-OUT-01</h3>
          <div className={"state-chip state-" + exitState}>{exitState}</div>
          <Lamps state={exitState} />
          <div className="muted" style={{ fontSize: 12 }}>Alur transaksi keluar lengkap ada di halaman <b>Exit POS Kasir</b>.</div>
          <div className="btn-row">
            <button className="btn" disabled={busy} onClick={() => call("/api/v1/sim/exit/loop", { loop: "pre", high: true })}>LD3 ↑</button>
            <button className="btn" disabled={busy} onClick={() => call("/api/v1/sim/exit/loop", { loop: "post", high: true })}>LD4 ↑</button>
            <button className="btn" disabled={busy} onClick={() => call("/api/v1/sim/exit/loop", { loop: "post", high: false })}>LD4 ↓</button>
          </div>
        </div>
      </div>

      {/* ── Event stream ── */}
      <div className="panel">
        <h3>Aliran Event Real-time (WebSocket)</h3>
        <EventLog events={events} />
      </div>
    </div>
  );
}

export function EventLog({ events }: { events: Ev[] }) {
  return (
    <div className="log">
      {events.length === 0 && <div className="muted">Belum ada event. Gerakkan gerbang untuk melihat aliran real-time.</div>}
      {events.map((e, i) => (
        <div className="row" key={i}>
          <span className="ts">{new Date(e.t).toLocaleTimeString("id-ID")}</span>
          <span className="ev">{e.event}</span>
          <span className="dt">{e.data ? JSON.stringify(e.data) : ""}</span>
        </div>
      ))}
    </div>
  );
}
