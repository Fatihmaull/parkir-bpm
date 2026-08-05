import { Sun, Moon, LogOut, Building2, Wifi, WifiOff } from "lucide-react";
import type { ConnState } from "../api";
import type { Health, PageId } from "../types";
import type { AuthUser } from "../lib/auth";

const TITLES: Record<PageId, string> = {
  overview: "Dashboard Overview", finance: "Catatan Keuangan", volume: "Volume & Jenis Kendaraan",
  slots: "Mapping Slot Parkir", location: "Konfigurasi Lahan", shift: "Rekonsiliasi Shift",
  alerts: "Notifikasi & Alerts", field: "Field Monitor", pos: "Exit POS Kasir",
  members: "RFID Memberships", hardware: "Hardware Config (COM)", audit: "Audit Ledger Chain",
};

export function TopBar({
  page, conn, health, user, theme, onToggleTheme, onLogout,
}: {
  page: PageId; conn: ConnState; health: Health | null; user: AuthUser;
  theme: "light" | "dark"; onToggleTheme: () => void; onLogout: () => void;
}) {
  const online = conn === "open";
  return (
    <header className="flex items-center gap-3 border-b border-slate-200 bg-white/80 px-6 py-3 backdrop-blur dark:border-slate-800 dark:bg-slate-900/80">
      <h1 className="text-base font-bold text-slate-900 dark:text-white">{TITLES[page]}</h1>
      <div className="flex-1" />

      {user.role !== "Kasir" && (
        <div className="flex items-center gap-1.5 rounded-xl border border-slate-200 px-2.5 py-1.5 dark:border-slate-700">
          <Building2 className="h-3.5 w-3.5 text-indigo-500" />
          <select className="bg-transparent text-xs font-semibold text-slate-600 outline-none dark:text-slate-300" defaultValue="all">
            <option value="all">Semua Site</option>
            <option value="mall_jabar">Mall Jabar</option>
          </select>
        </div>
      )}

      <span className={"badge " + (online ? "green" : conn === "connecting" ? "amber" : "red")}>
        {online ? <Wifi className="h-3.5 w-3.5" /> : <WifiOff className="h-3.5 w-3.5" />}
        {online ? "Terhubung" : conn === "connecting" ? "Menghubungkan…" : "Terputus"}
      </span>

      <span className="badge gray mono" title="Node">{health?.node_id ?? "—"}</span>

      <button className="btn" onClick={onToggleTheme} title="Ganti tema">
        {theme === "dark" ? <Sun className="h-4 w-4" /> : <Moon className="h-4 w-4" />}
      </button>

      <div className="flex items-center gap-2 rounded-xl border border-slate-200 py-1 pl-3 pr-1 dark:border-slate-700">
        <div className="text-right leading-tight">
          <div className="text-xs font-bold text-slate-800 dark:text-slate-100">{user.name}</div>
          <div className="text-[10px] font-semibold uppercase tracking-wider text-indigo-500">{user.role}</div>
        </div>
        <button className="btn" onClick={onLogout} title="Keluar">
          <LogOut className="h-4 w-4" />
        </button>
      </div>
    </header>
  );
}
