import {
  LayoutDashboard, Coins, BarChart3, LayoutGrid, Building2, ClipboardCheck,
  Bell, MonitorPlay, ReceiptText, Contact, Cpu, KeyRound, type LucideIcon,
} from "lucide-react";
import type { PageId } from "../types";
import type { Role } from "../lib/auth";
import { canAccess } from "../lib/rbac";
import { cn } from "../lib/cn";

interface Item { id: PageId; label: string; icon: LucideIcon; }
interface Group { group: string; items: Item[]; }

const NAV: Group[] = [
  { group: "Ringkasan", items: [{ id: "overview", label: "Dashboard Overview", icon: LayoutDashboard }] },
  {
    group: "Keuangan & Laporan",
    items: [
      { id: "finance", label: "Catatan Keuangan", icon: Coins },
      { id: "volume", label: "Volume Kendaraan", icon: BarChart3 },
      { id: "shift", label: "Rekonsiliasi Shift", icon: ClipboardCheck },
    ],
  },
  {
    group: "Operasional",
    items: [
      { id: "field", label: "Field Monitor", icon: MonitorPlay },
      { id: "pos", label: "Exit POS Kasir", icon: ReceiptText },
      { id: "alerts", label: "Notifikasi & Alerts", icon: Bell },
      { id: "members", label: "RFID Memberships", icon: Contact },
    ],
  },
  {
    group: "Konfigurasi",
    items: [
      { id: "slots", label: "Mapping Slot", icon: LayoutGrid },
      { id: "location", label: "Konfigurasi Lahan", icon: Building2 },
      { id: "hardware", label: "Hardware Config", icon: Cpu },
      { id: "audit", label: "Audit Ledger", icon: KeyRound },
    ],
  },
];

export function Sidebar({ page, role, onNav }: { page: PageId; role: Role; onNav: (p: PageId) => void }) {
  return (
    <aside className="flex w-64 shrink-0 flex-col border-r border-slate-200 bg-white dark:border-slate-800 dark:bg-slate-900">
      <div className="flex items-center gap-3 px-5 py-4">
        <div className="grid h-9 w-9 place-items-center rounded-xl bg-gradient-to-br from-indigo-500 to-sky-500 text-lg text-white shadow">🅿</div>
        <div className="text-sm font-extrabold text-slate-900 dark:text-white">Smart Gate</div>
      </div>

      <nav className="flex-1 space-y-4 overflow-y-auto px-3 pb-6">
        {NAV.map((g) => {
          const items = g.items.filter((it) => canAccess(role, it.id));
          if (items.length === 0) return null;
          return (
            <div key={g.group}>
              <div className="px-3 pb-1 pt-2 text-[10px] font-bold uppercase tracking-widest text-slate-400">{g.group}</div>
              {items.map((it) => {
                const Icon = it.icon;
                const active = page === it.id;
                return (
                  <button
                    key={it.id}
                    onClick={() => onNav(it.id)}
                    className={cn(
                      "flex w-full items-center gap-3 rounded-xl px-3 py-2.5 text-sm font-medium transition",
                      active
                        ? "bg-indigo-600 text-white shadow"
                        : "text-slate-500 hover:bg-slate-100 hover:text-slate-900 dark:text-slate-400 dark:hover:bg-slate-800 dark:hover:text-white"
                    )}
                  >
                    <Icon className="h-4 w-4 shrink-0" />
                    <span>{it.label}</span>
                  </button>
                );
              })}
            </div>
          );
        })}
      </nav>
    </aside>
  );
}
