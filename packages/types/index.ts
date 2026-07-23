// Kontrak data bersama — diselaraskan dengan skema PostgreSQL (PRD §11.2).
// Nilai uang selalu rupiah utuh (integer), tidak pernah float (PRD D6).

export type UUID = string;
export type Rupiah = number; // integer rupiah

export type Role = "SuperAdmin" | "Auditor" | "Kasir";
export type VehicleType = "mobil" | "motor" | "truk" | "bus";
export type Severity = "normal" | "warning" | "critical";

// ── Tenancy & lokasi ──
export interface Tenant {
  id: UUID;
  code: string;
  name: string;
  status: "active" | "inactive";
  created_at: string;
}

export interface Site {
  id: UUID;
  tenant_id: UUID;
  code: string;
  name: string;
  city?: string;
  address?: string;
  timezone: string;
  grace_minutes: number;
  peak_multiplier: number;
  peak_windows: PeakWindow[];
  max_daily_rate?: Rupiah;
  lost_ticket_penalty: Rupiah;
  antipassback_reset_hours: number;
  cash_variance_threshold: Rupiah;
  status: string;
}

export interface PeakWindow {
  days: number[]; // 1=Senin .. 7=Minggu
  from: string;   // "17:00"
  to: string;     // "21:00"
}

export interface Tariff {
  id: UUID;
  site_id: UUID;
  vehicle_type: VehicleType;
  base_rate: Rupiah;
  first_hour_rate?: Rupiah;
  effective_from: string;
  effective_to?: string;
}

// ── Perangkat ──
export type GateKind = "ENTRY" | "EXIT";
export type Transport = "serial" | "tcp" | "sim";

export interface Gate {
  id: UUID;
  site_id: UUID;
  code: string;
  kind: GateKind;
  controller_addr: number;
  transport: Transport;
  endpoint: string;
  status: string;
}

export type DeviceKind =
  | "BARRIER" | "LOOP" | "PRINTER" | "RFID" | "CAMERA" | "EDC" | "LIGHT";

export interface Device {
  id: UUID;
  gate_id?: UUID;
  site_id: UUID;
  kind: DeviceKind;
  label: string;
  status: "online" | "offline" | "fault" | "unknown";
  last_seen_at?: string;
}

// ── Pengguna & member ──
export interface User {
  id: UUID;
  tenant_id: UUID;
  email: string;
  full_name: string;
  role: Role;
  site_scope: UUID[];
  status: string;
}

export interface Membership {
  id: UUID;
  tenant_id: UUID;
  rfid_uid: string;
  holder_name: string;
  unit_label?: string;
  plates: string[];
  vehicle_type: VehicleType;
  site_scope: UUID[];
  valid_from: string;
  valid_until: string;
  status: "active" | "expired" | "blocked";
  presence: "IN" | "OUT";
}

// ── Transaksi ──
export type VehicleLogStatus = "IN_PREMISES" | "COMPLETED" | "VOID" | "DISPUTE";
export type VehicleFlag =
  | "needs_review" | "no_show" | "lost_ticket" | "unregistered_entry"
  | "photo_mismatch" | "manual_override";

export interface VehicleLog {
  id: UUID;
  tenant_id: UUID;
  site_id: UUID;
  ticket_code?: string;
  vehicle_type: VehicleType;
  membership_id?: UUID;
  plate_in?: string;
  plate_out?: string;
  img_in_key?: string;
  img_out_key?: string;
  entry_time: string;
  exit_time?: string;
  duration_min?: number;
  amount: Rupiah;
  status: VehicleLogStatus;
  flags: VehicleFlag[];
}

export type PaymentMethod =
  | "CASH" | "EDC_EMONEY" | "EDC_DEBIT" | "QRIS" | "EWALLET" | "MEMBER";
export type PaymentStatus = "PENDING" | "SETTLED" | "FAILED" | "REFUNDED";

export interface Payment {
  id: UUID;
  vehicles_log_id: UUID;
  method: PaymentMethod;
  amount: Rupiah;
  tendered?: Rupiah;
  change_given?: Rupiah;
  status: PaymentStatus;
  provider?: string;
  provider_ref?: string;
  card_type?: string;
  masked_pan?: string; // HANYA masked
  approval_code?: string;
  batch_no?: string;
  settled_at?: string;
}

export type OcrVerdict = "AUTO_ACCEPT" | "NEEDS_REVIEW" | "UNREAD";

export interface OcrLog {
  id: UUID;
  vehicles_log_id?: UUID;
  gate_id?: UUID;
  captured_at: string;
  raw_text?: string;
  normalized_plate?: string;
  confidence?: number;
  verdict: OcrVerdict;
  corrected_plate?: string;
  latency_ms?: number;
  engine_version?: string;
  image_key?: string;
}

// ── Audit (rantai hash per node, PRD §9) ──
export interface AuditEvent {
  id: UUID;
  tenant_id: UUID;
  site_id?: UUID;
  node_id: string;
  seq: number;
  event_type: string;
  severity: Severity;
  actor_label: string;
  actor_role: Role | "System";
  summary: string;
  payload: Record<string, unknown>;
  previous_hash: string;
  current_hash: string;
  created_at: string;
}

// ── Event WebSocket (PRD §13.1) ──
export type WsEvent =
  | "gate.state_changed" | "loop.changed" | "lpr.result" | "payment.settled"
  | "printer.status" | "device.status" | "alert.raised" | "sync.status"
  | "vehicle.entered" | "vehicle.exited";
