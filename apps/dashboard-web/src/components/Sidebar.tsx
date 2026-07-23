import type { PageId } from "../types";

interface NavItem { id: PageId; label: string; ic: string; live?: boolean; }
interface NavGroup { group: string; items: NavItem[]; }

// 12 halaman (PRD §12), 4 kategori. `live` = sudah tersambung backend.
const NAV: NavGroup[] = [
  { group: "Ringkasan", items: [{ id: "overview", label: "Dashboard Overview", ic: "▦", live: true }] },
  {
    group: "Keuangan & Laporan",
    items: [
      { id: "finance", label: "Catatan Keuangan", ic: "₨" },
      { id: "volume", label: "Volume Kendaraan", ic: "◔" },
      { id: "shift", label: "Rekonsiliasi Shift", ic: "⇆" },
    ],
  },
  {
    group: "Operasional",
    items: [
      { id: "field", label: "Field Monitor", ic: "◉", live: true },
      { id: "pos", label: "Exit POS Kasir", ic: "▤", live: true },
      { id: "alerts", label: "Notifikasi & Alerts", ic: "!" },
      { id: "members", label: "RFID Memberships", ic: "▭" },
    ],
  },
  {
    group: "Konfigurasi",
    items: [
      { id: "slots", label: "Mapping Slot", ic: "▣" },
      { id: "location", label: "Konfigurasi Lahan", ic: "⌂" },
      { id: "hardware", label: "Hardware Config", ic: "⚙" },
      { id: "audit", label: "Audit Ledger", ic: "⛓", live: true },
    ],
  },
];

export function Sidebar({ page, onNav }: { page: PageId; onNav: (p: PageId) => void }) {
  return (
    <aside className="sidebar">
      <div className="brand">
        <div className="logo">🅿</div>
        <span>Smart Gate</span>
      </div>
      {NAV.map((g) => (
        <div key={g.group}>
          <div className="nav-group">{g.group}</div>
          {g.items.map((it) => (
            <div
              key={it.id}
              className={"nav-item" + (page === it.id ? " active" : "")}
              onClick={() => onNav(it.id)}
            >
              <span className="ic">{it.ic}</span>
              <span className="label">{it.label}</span>
              {!it.live && <span className="soon">segera</span>}
            </div>
          ))}
        </div>
      ))}
    </aside>
  );
}
