import { useState } from "react";
import { Sidebar } from "./components/Sidebar";
import { TopBar } from "./components/TopBar";
import { useHealth, useStream } from "./api";
import type { PageId } from "./types";
import { Overview } from "./pages/Overview";
import { FieldMonitor } from "./pages/FieldMonitor";
import { POS } from "./pages/POS";
import { AuditLedger } from "./pages/AuditLedger";
import { Finance } from "./pages/Finance";
import { Volume } from "./pages/Volume";
import { Slots } from "./pages/Slots";
import { Location } from "./pages/Location";
import { Hardware } from "./pages/Hardware";
import { Alerts } from "./pages/Alerts";
import { Shift } from "./pages/Shift";
import { Members } from "./pages/Members";

export default function App() {
  const [page, setPage] = useState<PageId>("field");
  const health = useHealth();
  const { events, conn } = useStream();
  const online = true;
  // tick bertambah tiap event WS → memicu refresh data halaman (transaksi, dsb.).
  const tick = events.length;

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
          {page === "finance" && <Finance tick={tick} />}
          {page === "volume" && <Volume tick={tick} />}
          {page === "shift" && <Shift tick={tick} />}
          {page === "alerts" && <Alerts events={events} />}
          {page === "members" && <Members />}
          {page === "slots" && <Slots tick={tick} />}
          {page === "location" && <Location />}
          {page === "hardware" && <Hardware health={health} tick={tick} />}
        </div>
      </div>
    </div>
  );
}
