#!/usr/bin/env bash
# Ukur waktu pemulihan edge-api (NFR-2.3: < 15 dtk) — task 3.5.
#
# Yang diukur adalah SIKLUS PENUH yang dialami lahan saat Edge direstart:
#
#   SIGTERM → proses berhenti bersih → proses baru dijalankan → READY=1
#
# Bukan sekadar "berapa lama biner naik". Waktu berhenti ikut dihitung karena selama itu
# gerbang juga tidak melayani, dan systemd baru menjalankan ulang setelah proses lama
# benar-benar keluar (ditambah RestartSec).
#
# READY=1 dipakai sebagai garis akhir, bukan "proses ada di daftar proses": ia dikirim
# hanya setelah gerbang dirangkai DAN port HTTP benar-benar terikat (lihat main.go), jadi
# ia menandai saat lahan benar-benar dapat melayani lagi.
#
#   ./deploy/ukur-pemulihan.sh [jumlah_putaran]
#
# Butuh: bash, python3, Go toolchain. Jalankan dari akar repo.

set -euo pipefail

PUTARAN="${1:-5}"
BATAS_DETIK="${BATAS_DETIK:-15}"
RESTART_SEC="${RESTART_SEC:-2}"   # samakan dengan RestartSec= di unit systemd

AKAR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
KERJA="$(mktemp -d)"
trap 'rm -rf "$KERJA"' EXIT

echo "Membangun edge-api..."
(cd "$AKAR/services/edge-api" && go build -o "$KERJA/edge-api" ./cmd/edge-api)

python3 - "$KERJA/edge-api" "$PUTARAN" "$BATAS_DETIK" "$RESTART_SEC" <<'PY'
import os, signal, socket, statistics, subprocess, sys, time

biner, putaran, batas, restart_sec = sys.argv[1], int(sys.argv[2]), float(sys.argv[3]), float(sys.argv[4])

sock_path = os.path.join(os.path.dirname(biner), "notify.sock")
if os.path.exists(sock_path):
    os.unlink(sock_path)
srv = socket.socket(socket.AF_UNIX, socket.SOCK_DGRAM)
srv.bind(sock_path)
srv.settimeout(batas + 10)

env = dict(
    os.environ,
    NOTIFY_SOCKET=sock_path,
    EDGE_API_PORT="18099",
    EDGE_ENV="development",
    NODE_ID="edge-ukur",
    TENANT_CODE="t1",
    SITE_CODE="s1",
    GATE_IN_TRANSPORT="sim",
    GATE_OUT_TRANSPORT="sim",
    SYNC_CLOUD_ENDPOINT="",
)


def tunggu_ready():
    """Menunggu READY=1; pesan lain (STATUS, WATCHDOG) dilewati."""
    while True:
        if srv.recv(256).decode() == "READY=1":
            return


def jalankan():
    p = subprocess.Popen([biner], env=env,
                         stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
    tunggu_ready()
    return p


print(f"Mengukur {putaran} putaran (batas NFR-2.3: {batas:.0f} dtk, "
      f"RestartSec disimulasikan: {restart_sec:.0f} dtk)\n")

proc = jalankan()
hasil = []

for i in range(1, putaran + 1):
    mulai = time.monotonic()

    proc.send_signal(signal.SIGTERM)
    proc.wait(timeout=60)
    berhenti = time.monotonic() - mulai

    # systemd menunggu RestartSec sebelum menjalankan ulang; ikut dihitung karena
    # lahan juga tidak melayani selama jeda itu.
    time.sleep(restart_sec)

    t_nyala = time.monotonic()
    proc = jalankan()
    nyala = time.monotonic() - t_nyala
    total = time.monotonic() - mulai

    hasil.append(total)
    tanda = "OK " if total < batas else "GAGAL"
    print(f"  putaran {i}: berhenti {berhenti:5.2f}s + jeda {restart_sec:.0f}s + "
          f"nyala {nyala:5.2f}s = {total:5.2f}s  [{tanda}]")

proc.send_signal(signal.SIGTERM)
proc.wait(timeout=60)

terburuk = max(hasil)
print(f"\n  terbaik {min(hasil):.2f}s · median {statistics.median(hasil):.2f}s · "
      f"TERBURUK {terburuk:.2f}s")
print(f"  NFR-2.3 (< {batas:.0f} dtk): {'TERPENUHI' if terburuk < batas else 'TIDAK TERPENUHI'}")
print("\n  Catatan: yang diukur adalah kesiapan MELAYANI, bukan pemulihan DATA.")
print("  Selama edge-api memakai memstore, seluruh kendaraan di dalam lahan hilang")
print("  saat restart — lihat docs/CATATAN_KEPUTUSAN.md K35.")

sys.exit(0 if terburuk < batas else 1)
PY
