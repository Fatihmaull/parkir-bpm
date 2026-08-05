import { useState } from "react";
import { ShieldCheck, LogIn, Loader2 } from "lucide-react";
import { login, type AuthUser } from "../lib/auth";

export function Login({ onLogin }: { onLogin: (u: AuthUser) => void }) {
  const [email, setEmail] = useState("admin@parkir.local");
  const [password, setPassword] = useState("admin12345");
  const [err, setErr] = useState("");
  const [busy, setBusy] = useState(false);

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    setBusy(true);
    setErr("");
    try {
      onLogin(await login(email, password));
    } catch (e: any) {
      setErr(e.message || "Gagal masuk");
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="grid min-h-screen place-items-center bg-gradient-to-br from-slate-100 to-slate-200 p-4 dark:from-slate-950 dark:to-slate-900">
      <div className="w-full max-w-sm">
        <div className="mb-6 flex items-center gap-3">
          <div className="grid h-11 w-11 place-items-center rounded-xl bg-gradient-to-br from-indigo-500 to-sky-500 text-xl text-white shadow-lg">
            🅿
          </div>
          <div>
            <h1 className="text-lg font-extrabold text-slate-900 dark:text-white">Smart Gate & Parking</h1>
            <p className="text-xs text-slate-500">Dashboard Manajemen · Multi-tenant</p>
          </div>
        </div>

        <form onSubmit={submit} className="panel space-y-4">
          <div>
            <label className="mb-1 block text-xs font-semibold text-slate-500">Email</label>
            <input className="select w-full" value={email} onChange={(e) => setEmail(e.target.value)} type="email" autoFocus />
          </div>
          <div>
            <label className="mb-1 block text-xs font-semibold text-slate-500">Password</label>
            <input className="select w-full" value={password} onChange={(e) => setPassword(e.target.value)} type="password" />
          </div>
          {err && (
            <div className="rounded-lg border border-rose-300 bg-rose-50 px-3 py-2 text-xs text-rose-700 dark:border-rose-800 dark:bg-rose-950/40 dark:text-rose-300">
              {err}
            </div>
          )}
          <button type="submit" disabled={busy} className="btn primary flex w-full items-center justify-center gap-2">
            {busy ? <Loader2 className="h-4 w-4 animate-spin" /> : <LogIn className="h-4 w-4" />}
            Masuk
          </button>
          <div className="flex items-center gap-2 text-[11px] text-slate-400">
            <ShieldCheck className="h-3.5 w-3.5" /> Autentikasi Argon2id + JWT ke cloud-api (§14)
          </div>
          <p className="text-center text-[11px] text-slate-400">Demo: admin@parkir.local / admin12345</p>
        </form>
      </div>
    </div>
  );
}
