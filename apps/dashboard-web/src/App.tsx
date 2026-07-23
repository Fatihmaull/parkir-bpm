import { useState } from "react";
import { Sidebar } from "./components/Sidebar";
import { TopBar } from "./components/TopBar";
import { useHealth, useStream } from "./api";
import type { PageId } from "./types";
import { Overview } from "./pages/Overview";
import { FieldMonitor } from "./pages/FieldMonitor";
import { POS } from "./pages/POS";
import { AuditLedger } from "./pages/AuditLedger";
import { Stub } from "./pages/Stub";

const STUB_TITLES: Partial<Record<PageId, string>> = {
  finance: "Catatan Keuangan",
  volume: "Volume & Jenis Kendaraan",
  shift: "Rekonsiliasi Shift",
  alerts: "Notifikasi & Alerts",
  members: "RFID Memberships",
  slots: "Mapping Slot Parkir",
  location: "Konfigurasi Lahan",
  hardware: "Hardware Config (COM)",
};

export default function App() {
  const [page, setPage] = useState<PageId>("field");
  const health = useHealth();
  const { events, conn } = useStream();
  const online = true;

  const entry = health?.gates.entry ?? "—";
  const exit = health?.gates.exit ?? "—";

  return (
    <div className="app">
      <Sidebar page={page} onNav={setPage} />
      <div className="main">
        <TopBar page={page} conn={conn} health={health} />
        <div className="content">
          {page === "overview" && <Overview health={health} events={events} />}
          {page === "field" && <FieldMonitor events={events} entry={entry} exit={exit} />}
          {page === "pos" && <POS events={events} exit={exit} online={online} />}
          {page === "audit" && <AuditLedger health={health} events={events} />}
          {STUB_TITLES[page] && <Stub title={STUB_TITLES[page]!} />}
        </div>
      </div>
    </div>
  );
}
