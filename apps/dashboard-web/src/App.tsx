import { useEffect, useMemo, useState } from "react";
import { ShieldAlert } from "lucide-react";
import { Sidebar } from "./components/Sidebar";
import { TopBar } from "./components/TopBar";
import { Login } from "./components/Login";
import { useHealth, useStream } from "./api";
import type { PageId } from "./types";
import { currentUser, logout, type AuthUser } from "./lib/auth";
import { allowedPages } from "./lib/rbac";
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

const ALL: PageId[] = [
  "overview", "finance", "volume", "shift", "field", "pos",
  "alerts", "members", "slots", "location", "hardware", "audit",
];

function useTheme(): ["light" | "dark", () => void] {
  const [theme, setTheme] = useState<"light" | "dark">(
    () => (localStorage.getItem("parkir.theme") as "light" | "dark") ||
      (window.matchMedia?.("(prefers-color-scheme: dark)").matches ? "dark" : "light")
  );
  useEffect(() => {
    document.documentElement.classList.toggle("dark", theme === "dark");
    localStorage.setItem("parkir.theme", theme);
  }, [theme]);
  return [theme, () => setTheme((t) => (t === "dark" ? "light" : "dark"))];
}

export default function App() {
  const [user, setUser] = useState<AuthUser | null>(() => currentUser());
  const [theme, toggleTheme] = useTheme();
  const health = useHealth();
  const { events, conn } = useStream();
  const tick = events.length;

  const allowed = useMemo(() => (user ? allowedPages(user.role, ALL) : []), [user]);
  const [page, setPage] = useState<PageId>("overview");

  // Saat login / peran berubah, pastikan halaman aktif diizinkan.
  useEffect(() => {
    if (allowed.length && !allowed.includes(page)) setPage(allowed[0]);
  }, [allowed, page]);

  if (!user) return <Login onLogin={setUser} />;

  const online = true;
  const entry = health?.gates.entry ?? "—";
  const exit = health?.gates.exit ?? "—";
  const tampered = health?.chain.verified === false;

  return (
    <div className="flex h-screen overflow-hidden">
      <Sidebar page={page} role={user.role} onNav={setPage} />
      <div className="flex min-w-0 flex-1 flex-col">
        <TopBar
          page={page} conn={conn} health={health} user={user}
          theme={theme} onToggleTheme={toggleTheme}
          onLogout={() => { logout(); setUser(null); }}
        />

        {tampered && (
          <div className="flex items-center gap-2 bg-rose-600 px-6 py-2.5 text-xs font-bold uppercase tracking-wider text-white">
            <ShieldAlert className="h-4 w-4 animate-bounce" />
            Rantai audit RUSAK — data dimodifikasi di luar aplikasi (§9). Periksa Audit Ledger.
          </div>
        )}

        <main className="flex-1 overflow-y-auto p-6">
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
        </main>
      </div>
    </div>
  );
}
