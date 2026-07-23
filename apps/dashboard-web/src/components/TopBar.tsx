import type { ConnState } from "../api";
import type { Health } from "../types";

const TITLES: Record<string, string> = {
  overview: "Dashboard Overview",
  finance: "Catatan Keuangan",
  volume: "Volume & Jenis Kendaraan",
  shift: "Rekonsiliasi Shift",
  field: "Field Monitor",
  pos: "Exit POS Kasir",
  alerts: "Notifikasi & Alerts",
  members: "RFID Memberships",
  slots: "Mapping Slot Parkir",
  location: "Konfigurasi Lahan",
  hardware: "Hardware Config (COM)",
  audit: "Audit Ledger Chain",
};

export function TopBar({ page, conn, health }: { page: string; conn: ConnState; health: Health | null }) {
  const connBadge =
    conn === "open" ? "green" : conn === "connecting" ? "amber" : "red";
  const connText = conn === "open" ? "Terhubung" : conn === "connecting" ? "Menghubungkan…" : "Terputus";
  return (
    <header className="topbar">
      <h1>{TITLES[page] ?? "Dashboard"}</h1>
      <div className="spacer" />
      <select className="select" defaultValue="all">
        <option value="all">Semua Site</option>
        <option value="mall_jabar">Mall Jabar</option>
      </select>
      <span className={"badge " + connBadge}>
        <span className="dot" /> {connText}
      </span>
      <span className="badge gray mono" title="Node ID">
        {health?.node_id ?? "—"}
      </span>
    </header>
  );
}
