import { useCallback, useEffect, useRef, useState } from "react";
import type { Health, WsEvent } from "./types";

// POST ke endpoint simulator/API edge-api. Kembalikan JSON (atau {} bila kosong).
export async function post(path: string, body?: unknown): Promise<any> {
  const res = await fetch(path, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: body ? JSON.stringify(body) : undefined,
  });
  if (!res.ok) throw new Error(`${path} → ${res.status}`);
  return res.json().catch(() => ({}));
}

export async function getHealth(): Promise<Health> {
  const res = await fetch("/api/v1/health");
  return res.json();
}

export async function getJSON<T = any>(path: string): Promise<T> {
  const res = await fetch(path);
  if (!res.ok) throw new Error(`${path} → ${res.status}`);
  return res.json();
}

// useFetch — GET JSON dengan refresh saat `dep` berubah + interval opsional.
export function useFetch<T = any>(path: string, dep: unknown = null, ms = 0): T | null {
  const [data, setData] = useState<T | null>(null);
  useEffect(() => {
    let alive = true;
    const tick = () => getJSON<T>(path).then((d) => alive && setData(d)).catch(() => {});
    tick();
    if (ms > 0) {
      const id = setInterval(tick, ms);
      return () => { alive = false; clearInterval(id); };
    }
    return () => { alive = false; };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [path, dep, ms]);
  return data;
}

// useHealth — polling /health tiap `ms`.
export function useHealth(ms = 3000): Health | null {
  const [h, setH] = useState<Health | null>(null);
  useEffect(() => {
    let alive = true;
    const tick = () => getHealth().then((x) => alive && setH(x)).catch(() => {});
    tick();
    const id = setInterval(tick, ms);
    return () => {
      alive = false;
      clearInterval(id);
    };
  }, [ms]);
  return h;
}

export type ConnState = "connecting" | "open" | "closed";

// useStream — koneksi WebSocket ke /api/v1/stream, auto-reconnect. Menyimpan log event.
export function useStream(max = 200): {
  events: (WsEvent & { t: number })[];
  conn: ConnState;
  last: Record<string, WsEvent & { t: number }>;
} {
  const [events, setEvents] = useState<(WsEvent & { t: number })[]>([]);
  const [conn, setConn] = useState<ConnState>("connecting");
  const [last, setLast] = useState<Record<string, WsEvent & { t: number }>>({});
  const wsRef = useRef<WebSocket | null>(null);

  const connect = useCallback(() => {
    const proto = location.protocol === "https:" ? "wss" : "ws";
    const ws = new WebSocket(`${proto}://${location.host}/api/v1/stream`);
    wsRef.current = ws;
    setConn("connecting");
    ws.onopen = () => setConn("open");
    ws.onclose = () => {
      setConn("closed");
      setTimeout(connect, 1500);
    };
    ws.onmessage = (m) => {
      try {
        const ev = JSON.parse(m.data) as WsEvent;
        const withT = { ...ev, t: Date.now() };
        setEvents((prev) => [withT, ...prev].slice(0, max));
        setLast((prev) => ({ ...prev, [ev.event]: withT }));
      } catch {
        /* ignore */
      }
    };
  }, [max]);

  useEffect(() => {
    connect();
    return () => wsRef.current?.close();
  }, [connect]);

  return { events, conn, last };
}
