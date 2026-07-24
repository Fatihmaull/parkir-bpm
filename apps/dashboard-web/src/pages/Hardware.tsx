import { post, useFetch } from "../api";
import type { Health } from "../types";

interface Gate { code: string; kind: string; transport: string; endpoint: string; state: string; }

export function Hardware({ health, tick }: { health: Health | null; tick: number }) {
  const data = useFetch<{ gates: Gate[] }>("/api/v1/devices", tick, 2000);
  const gates = data?.gates ?? [];
  const test = (which: "entry" | "exit") => post(`/api/v1/sim/${which}/loop`, { loop: "pre", high: true }).catch(console.error);

  return (
    <div className="grid" style={{ gap: 18 }}>
      <div className="panel">
        <h3>Peta Port & Controller (§5.1)</h3>
        <div style={{ overflowX: "auto" }}>
          <table>
            <thead><tr><th>Gerbang</th><th>Jenis</th><th>Transport</th><th>Endpoint</th><th>State</th><th>Uji</th></tr></thead>
            <tbody>
              {gates.map((g) => (
                <tr key={g.code}>
                  <td className="mono">{g.code}</td>
                  <td>{g.kind}</td>
                  <td><span className="badge gray">{g.transport}</span></td>
                  <td className="mono">{g.endpoint}</td>
                  <td><span className={"badge " + (g.state === "IDLE" ? "gray" : "amber")}><span className="dot" />{g.state}</span></td>
                  <td><button className="btn" onClick={() => test(g.kind === "ENTRY" ? "entry" : "exit")}>tes loop</button></td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      </div>
      <div className="row-2">
        <div className="panel">
          <h3>Status Sistem</h3>
          <table><tbody>
            <tr><td className="muted">Sinkronisasi</td><td>{health?.sync.state} · {health?.sync.pending ?? 0} pending</td></tr>
            <tr><td className="muted">Rantai audit</td><td><span className={"badge " + (health?.chain.verified ? "green" : "red")}>{health?.chain.verified ? "OK" : "RUSAK"} · {health?.chain.entries ?? 0}</span></td></tr>
            <tr><td className="muted">Subscriber WS</td><td>{health?.ws_subscribers ?? 0}</td></tr>
          </tbody></table>
        </div>
        <div className="panel">
          <h3>Kontrak Protokol §5.3</h3>
          <p className="muted" style={{ fontSize: 12 }}>
            Frame STX·ADDR·CMD·LEN·PAYLOAD·CRC16·ETX (CRC-16/MODBUS). Heartbeat 1 dtk, timeout 3× →
            OFFLINE. Interlock keselamatan & fail-safe wajib di firmware. Telemetry TX/RX hex live
            menyusul saat controller fisik tersambung (Fase 1b).
          </p>
        </div>
      </div>
    </div>
  );
}
