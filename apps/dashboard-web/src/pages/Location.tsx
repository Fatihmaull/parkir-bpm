import { useFetch } from "../api";

const rupiah = (n: number) => "Rp" + (n || 0).toLocaleString("id-ID");

interface Config {
  node_id: string; timezone: string; grace_minutes: number; max_daily_rate: number;
  lost_ticket_penalty: number; antipassback_reset_hours: number;
  tariffs: { vehicle_type: string; base_rate: number }[];
}

export function Location() {
  const c = useFetch<Config>("/api/v1/config");
  if (!c) return <div className="muted">Memuat…</div>;
  return (
    <div className="row-2">
      <div className="panel">
        <h3>Parameter Lahan</h3>
        <table><tbody>
          <tr><td className="muted">Node</td><td className="mono">{c.node_id}</td></tr>
          <tr><td className="muted">Timezone</td><td>{c.timezone}</td></tr>
          <tr><td className="muted">Grace period</td><td>{c.grace_minutes} menit</td></tr>
          <tr><td className="muted">Tarif maks. harian</td><td>{rupiah(c.max_daily_rate)}</td></tr>
          <tr><td className="muted">Denda tiket hilang</td><td>{rupiah(c.lost_ticket_penalty)}</td></tr>
          <tr><td className="muted">Reset anti-passback</td><td>{c.antipassback_reset_hours} jam</td></tr>
        </tbody></table>
      </div>
      <div className="panel">
        <h3>Tarif (versioned §12.5)</h3>
        <table>
          <thead><tr><th>Jenis</th><th>Tarif dasar / jam</th></tr></thead>
          <tbody>{c.tariffs.map((t) => <tr key={t.vehicle_type}><td>{t.vehicle_type}</td><td>{rupiah(t.base_rate)}</td></tr>)}</tbody>
        </table>
        <p className="muted" style={{ fontSize: 12 }}>Perubahan tarif membuat baris versi baru (effective_from) — transaksi lama tetap dapat direkalkulasi (D5). Edit tarif ter-audit `warning`.</p>
      </div>
    </div>
  );
}
