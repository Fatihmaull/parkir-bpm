export function Stub({ title }: { title: string }) {
  return (
    <div className="stub">
      <div className="big">🚧</div>
      <div style={{ fontSize: 18, color: "var(--text)" }}>{title}</div>
      <div>Halaman ini tersedia pada iterasi berikutnya.</div>
      <div style={{ fontSize: 12 }}>
        Fondasi data & API sudah dirancang di PRD §12 — tinggal migrasi UI dari prototype.
      </div>
    </div>
  );
}
