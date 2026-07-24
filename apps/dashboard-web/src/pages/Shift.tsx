import { useState } from "react";
import { useFetch } from "../api";

const rupiah = (n: number) => "Rp" + (n || 0).toLocaleString("id-ID");
interface Pay { method: string; amount: number; status: string; }

// Rekonsiliasi Shift (§12.6/§6.4) — buka/tutup shift, hitung selisih (demo client-side).
export function Shift({ tick }: { tick: number }) {
  const data = useFetch<{ payments: Pay[] }>("/api/v1/transactions", tick, 3000);
  const settled = (data?.payments ?? []).filter((p) => p.status === "SETTLED");

  const [openFloat, setOpenFloat] = useState<number | null>(null);
  const [declared, setDeclared] = useState(0);

  const sum = (m: string) => settled.filter((p) => p.method === m).reduce((s, p) => s + p.amount, 0);
  const cash = sum("CASH");
  const edc = settled.filter((p) => p.method.startsWith("EDC")).reduce((s, p) => s + p.amount, 0);
  const qris = sum("QRIS") + sum("EWALLET");
  const expected = (openFloat ?? 0) + cash;
  const variance = declared - expected;

  if (openFloat === null) {
    return (
      <div className="panel" style={{ maxWidth: 420 }}>
        <h3>Buka Shift</h3>
        <div className="muted" style={{ fontSize: 13, marginBottom: 10 }}>Masukkan kas awal (float).</div>
        <div className="btn-row">
          <input className="select" type="number" defaultValue={100000} id="float" style={{ width: 160 }} />
          <button className="btn primary" onClick={() => setOpenFloat(Number((document.getElementById("float") as HTMLInputElement).value))}>Buka Shift</button>
        </div>
      </div>
    );
  }

  return (
    <div className="row-2">
      <div className="panel">
        <h3>Shift Berjalan</h3>
        <table><tbody>
          <tr><td className="muted">Kas awal (float)</td><td>{rupiah(openFloat)}</td></tr>
          <tr><td className="muted">Tunai sistem</td><td>{rupiah(cash)}</td></tr>
          <tr><td className="muted">EDC sistem</td><td>{rupiah(edc)}</td></tr>
          <tr><td className="muted">QRIS / e-wallet</td><td>{rupiah(qris)}</td></tr>
          <tr><td className="muted">Kas seharusnya</td><td><b>{rupiah(expected)}</b></td></tr>
        </tbody></table>
      </div>
      <div className="panel">
        <h3>Tutup Shift</h3>
        <div className="muted" style={{ fontSize: 13 }}>Kas fisik dilaporkan:</div>
        <div className="btn-row" style={{ margin: "8px 0" }}>
          <input className="select" type="number" value={declared} onChange={(e) => setDeclared(Number(e.target.value))} style={{ width: 160 }} />
        </div>
        <div className={"state-chip state-" + (variance === 0 ? "OPEN" : "REJECTED")} style={{ fontSize: 16 }}>
          Selisih: {rupiah(variance)}
        </div>
        {variance !== 0 && <p className="muted" style={{ fontSize: 12 }}>Selisih ≠ 0 → shift ditandai VARIANCE, audit `warning`, wajib catatan (§6.4).</p>}
        <button className="btn" onClick={() => setOpenFloat(null)}>Tutup & Shift Baru</button>
      </div>
    </div>
  );
}
