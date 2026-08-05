// Autentikasi nyata terhadap cloud-api (§14) via proxy /cloud. Token disimpan di localStorage.
export type Role = "SuperAdmin" | "Auditor" | "Kasir";

export interface AuthUser {
  id: string;
  name: string;
  role: Role;
  tenant_id: string;
}

const KEY = "parkir.auth";

interface Stored {
  access_token: string;
  refresh_token?: string;
  user: AuthUser;
}

export async function login(email: string, password: string): Promise<AuthUser> {
  const res = await fetch("/cloud/api/v1/auth/login", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ Email: email, Password: password }),
  });
  if (!res.ok) {
    const p = await res.json().catch(() => ({}));
    throw new Error(p.detail || "Email atau password salah");
  }
  const data = (await res.json()) as Stored;
  localStorage.setItem(KEY, JSON.stringify(data));
  return data.user;
}

export function currentUser(): AuthUser | null {
  try {
    const raw = localStorage.getItem(KEY);
    if (!raw) return null;
    return (JSON.parse(raw) as Stored).user;
  } catch {
    return null;
  }
}

export function token(): string | null {
  try {
    const raw = localStorage.getItem(KEY);
    return raw ? (JSON.parse(raw) as Stored).access_token : null;
  } catch {
    return null;
  }
}

export function logout() {
  localStorage.removeItem(KEY);
}
