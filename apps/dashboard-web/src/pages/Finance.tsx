import { useFetch } from "../api";

const rupiah = (n: number) => "Rp" + (n || 0).toLocaleString("id-ID");

interface Txn { id: string; ticket: string; vehicle_type: string; plate_in: string; entry_time: string; amount: number; status: string; flags: string[]; }
interface Pay { method: string; amount: number; status: string; }

export function Finance({ tick }: { tick: number }) {
  const data = useFetch<{ transactions: Txn[]; payments: Pay[] }>("/api/v1/transactions", tick);
  const txns = data?.transactions ?? [];
  const pays = (data?.payments ?? []).filter((p) => p.status === "SETTLED");

  const byMethod: Record<string, number> = {};
  for (const p of pays) byMethod[p.method] = (byMethod[p.method] || 0) + p.amount;
  const total = pays.reduce((s, p) => s + p.amount, 0);

  return (
    <div className="grid" style={{ gap: 18 }}>
      <div className="grid cards">
        <div className="card"><div className="label">Total Pendapatan</div><div className="value">{rupiah(total)}</div><div className="sub">{pays.length} pembayaran settled</div></div>
        {Object.entries(byMethod).map(([m, v]) => (
          <div className="card" key={m}><div className="label">{m}</div><div className="value" style={{ fontSize: 20 }}>{rupiah(v)}</div></div>
        ))}
      </div>
      <div className="panel">
        <h3>Transaksi</h3>
        <div style={{ overflowX: "auto" }}>
          <table>
            <thead><tr><th>Tiket</th><th>Jenis</th><th>Plat</th><th>Masuk</th><th>Tarif</th><th>Status</th><th>Flag</th></tr></thead>
            <tbody>
              {txns.length === 0 && <tr><td colSpan={7} className="muted">Belum ada transaksi. Jalankan siklus masuk/keluar.</td></tr>}
              {txns.map((t) => (
                <tr key={t.id}>
                  <td className="mono">{t.ticket || "—"}</td>
                  <td>{t.vehicle_type}</td>
                  <td className="mono">{t.plate_in || "—"}</td>
                  <td className="muted">{new Date(t.entry_time).toLocaleString("id-ID")}</td>
                  <td>{rupiah(t.amount)}</td>
                  <td><span className={"badge " + (t.status === "COMPLETED" ? "green" : t.status === "IN_PREMISES" ? "amber" : "gray")}>{t.status}</span></td>
                  <td className="muted mono" style={{ fontSize: 11 }}>{(t.flags || []).join(", ")}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      </div>
    </div>
  );
}
