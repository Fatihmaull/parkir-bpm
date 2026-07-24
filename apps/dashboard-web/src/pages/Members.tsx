import { useState } from "react";
import { post, useFetch } from "../api";

interface Member { id: string; rfid_uid: string; plates: string[]; vehicle_type: string; valid_until: string; presence: string; status: string; }
interface PreviewRow { line: number; uid: string; valid: boolean; reason?: string; }

export function Members() {
  const [tick, setTick] = useState(0);
  const data = useFetch<{ members: Member[] }>("/api/v1/members", tick);
  const members = data?.members ?? [];

  const [uid, setUid] = useState("");
  const [plate, setPlate] = useState("");
  const [preview, setPreview] = useState<{ rows: PreviewRow[]; all_valid: boolean; valid: number; total: number } | null>(null);
  const [csvText, setCsvText] = useState("");

  const register = async () => {
    if (!uid) return;
    await post("/api/v1/members", { uid, plates: plate ? [plate] : [], vehicle_type: "mobil" });
    setUid(""); setPlate(""); setTick((t) => t + 1);
  };

  // Bulk CSV 3-langkah (§12.13): unggah → pratinjau(validate) → konfirmasi(apply).
  const doValidate = async () => {
    const res = await fetch("/api/v1/members/import?mode=validate", { method: "POST", body: csvText });
    setPreview(await res.json());
  };
  const doApply = async () => {
    await fetch("/api/v1/members/import?mode=apply", { method: "POST", body: csvText });
    setPreview(null); setCsvText(""); setTick((t) => t + 1);
  };

  return (
    <div className="grid" style={{ gap: 18 }}>
      <div className="row-2">
        <div className="panel">
          <h3>Registrasi Member (tap RFID §8.1)</h3>
          <div className="btn-row">
            <input className="select" placeholder="UID kartu (mis. 04A1B2C3)" value={uid} onChange={(e) => setUid(e.target.value)} style={{ width: 180 }} />
            <input className="select" placeholder="Plat (mis. D1234ABC)" value={plate} onChange={(e) => setPlate(e.target.value)} style={{ width: 150 }} />
            <button className="btn primary" onClick={register}>Daftarkan</button>
          </div>
        </div>
        <div className="panel">
          <h3>Bulk CSV (§12.13)</h3>
          <textarea className="select" style={{ width: "100%", height: 64, fontFamily: "monospace", fontSize: 12 }}
            placeholder="rfid_uid,plates,vehicle_type,valid_until&#10;04FF,D9|D8,mobil,2027-01-01"
            value={csvText} onChange={(e) => setCsvText(e.target.value)} />
          <div className="btn-row" style={{ marginTop: 8 }}>
            <button className="btn" onClick={doValidate} disabled={!csvText}>① Pratinjau</button>
            <button className="btn primary" onClick={doApply} disabled={!preview?.all_valid}>③ Konfirmasi Impor</button>
            <a className="btn" href="/api/v1/members/export">Ekspor CSV</a>
          </div>
          {preview && (
            <div style={{ marginTop: 8, fontSize: 12 }}>
              <span className={"badge " + (preview.all_valid ? "green" : "red")}>{preview.valid}/{preview.total} baris valid</span>
              {!preview.all_valid && <span className="muted"> — impor transaksional: satu gagal → semua batal</span>}
              {preview.rows.filter((r) => !r.valid).map((r) => <div key={r.line} className="muted">baris {r.line}: {r.reason}</div>)}
            </div>
          )}
        </div>
      </div>
      <div className="panel">
        <h3>Member ({members.length})</h3>
        <div style={{ overflowX: "auto" }}>
          <table>
            <thead><tr><th>UID</th><th>Plat</th><th>Jenis</th><th>Berlaku s/d</th><th>Presence</th><th>Status</th></tr></thead>
            <tbody>
              {members.map((m) => (
                <tr key={m.id}>
                  <td className="mono">{m.rfid_uid}</td>
                  <td className="mono">{m.plates.join(", ")}</td>
                  <td>{m.vehicle_type}</td>
                  <td>{m.valid_until}</td>
                  <td><span className={"badge " + (m.presence === "IN" ? "amber" : "gray")}>{m.presence}</span></td>
                  <td><span className={"badge " + (m.status === "active" ? "green" : "red")}>{m.status}</span></td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      </div>
    </div>
  );
}
