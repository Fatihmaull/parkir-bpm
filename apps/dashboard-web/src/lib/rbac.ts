import type { PageId } from "../types";
import type { Role } from "./auth";

// Hak akses halaman per peran (PRD §12.0). Di-enforce di menu (UX); backend meng-enforce ulang.
const ACCESS: Record<Role, PageId[] | "all"> = {
  SuperAdmin: "all",
  Auditor: ["overview", "finance", "volume", "alerts", "members", "audit"],
  Kasir: ["pos", "shift"],
};

export function allowedPages(role: Role, all: PageId[]): PageId[] {
  const a = ACCESS[role];
  return a === "all" ? all : all.filter((p) => a.includes(p));
}

export function canAccess(role: Role, page: PageId): boolean {
  const a = ACCESS[role];
  return a === "all" || a.includes(page);
}
