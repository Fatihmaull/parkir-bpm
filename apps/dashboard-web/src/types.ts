// Kontrak minimal untuk UI (subset dari packages/types, diselaraskan dengan event edge-api).

export interface WsEvent {
  event: string;
  data?: Record<string, unknown>;
}

export interface Health {
  status: string;
  node_id: string;
  env: string;
  gates: { entry: string; exit: string };
  ws_subscribers: number;
  sync: { state: string; pending: number };
  chain: { verified: boolean; entries: number };
}

export type PageId =
  | "overview"
  | "finance"
  | "volume"
  | "slots"
  | "location"
  | "shift"
  | "alerts"
  | "field"
  | "pos"
  | "members"
  | "hardware"
  | "audit";
