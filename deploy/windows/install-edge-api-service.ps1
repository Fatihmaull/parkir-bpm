# Pasang edge-api sebagai Windows Service (task 3.1).
#
# Edge di lahan klien adalah "PC Admin" berbasis Windows, jadi setara systemd di sini
# adalah Service Control Manager. NSSM dipakai sebagai pembungkus karena ia menangani
# restart otomatis, pengalihan log, dan direktori kerja tanpa menuntut biner kita
# mengandung kode khusus Windows yang tidak dapat kita uji di CI.
#
#   .\install-edge-api-service.ps1 -BinaryPath C:\parkir\edge-api.exe `
#                                  -EnvFile    C:\parkir\edge-api.env
#
# Jalankan sebagai Administrator.
#
# BATASAN YANG HARUS DIKETAHUI:
# NSSM menyalakan ulang proses yang MATI. Ia TIDAK mendeteksi proses yang hidup tetapi
# MEMBEKU — tak ada padanan sd_notify/WatchdogSec di Windows. Celah itu ditutup terpisah
# oleh watchdog-edge-api.ps1 yang menguji endpoint kesehatan dari luar. Pasang keduanya;
# yang satu tanpa yang lain meninggalkan mode kegagalan paling senyap tetap terbuka.

[CmdletBinding()]
param(
    [Parameter(Mandatory = $true)][string]$BinaryPath,
    [Parameter(Mandatory = $true)][string]$EnvFile,
    [string]$ServiceName = "edge-api",
    [string]$NssmPath    = "nssm.exe",
    [string]$WorkingDir  = "",
    [string]$LogDir      = "C:\parkir\logs"
)

$ErrorActionPreference = "Stop"

function Wajib-Ada($path, $apa) {
    if (-not (Test-Path $path)) { throw "$apa tidak ditemukan: $path" }
}

Wajib-Ada $BinaryPath "Biner edge-api"
Wajib-Ada $EnvFile    "Berkas konfigurasi"

if (-not (Get-Command $NssmPath -ErrorAction SilentlyContinue)) {
    throw "nssm tidak ditemukan di PATH. Unduh dari https://nssm.cc lalu ulangi, atau beri -NssmPath."
}

if ([string]::IsNullOrWhiteSpace($WorkingDir)) {
    $WorkingDir = Split-Path -Parent $BinaryPath
}
New-Item -ItemType Directory -Force -Path $LogDir | Out-Null

# Konfigurasi dibaca di sini lalu ditanam sebagai environment service. Sengaja TIDAK
# ditaruh di baris perintah: baris perintah service terbaca oleh siapa pun yang menjalankan
# `sc qc`, dan berkas ini memuat JWT_ACCESS_SECRET (NFR-3).
$pasangan = @()
foreach ($baris in Get-Content $EnvFile) {
    $t = $baris.Trim()
    if ($t -eq "" -or $t.StartsWith("#")) { continue }
    $i = $t.IndexOf("=")
    if ($i -lt 1) { continue }
    $pasangan += $t
}
if ($pasangan.Count -eq 0) { throw "Tak satu pun variabel terbaca dari $EnvFile" }

if (Get-Service -Name $ServiceName -ErrorAction SilentlyContinue) {
    Write-Host "Service '$ServiceName' sudah ada — dihentikan dan dikonfigurasi ulang."
    & $NssmPath stop $ServiceName confirm | Out-Null
} else {
    & $NssmPath install $ServiceName $BinaryPath | Out-Null
}

& $NssmPath set $ServiceName Application       $BinaryPath        | Out-Null
& $NssmPath set $ServiceName AppDirectory      $WorkingDir        | Out-Null
& $NssmPath set $ServiceName DisplayName       "parkir edge-api"  | Out-Null
& $NssmPath set $ServiceName Description       "Edge Node lahan parkir — gerbang, fare engine, audit chain" | Out-Null
& $NssmPath set $ServiceName Start             SERVICE_AUTO_START | Out-Null
& $NssmPath set $ServiceName AppEnvironmentExtra $pasangan        | Out-Null

# ── Restart ──
# Selalu nyalakan ulang, apa pun kode keluarnya. edge-api keluar dengan status bukan-nol
# untuk kegagalan fatal (lihat main.go), dan lahan tidak boleh berhenti melayani hanya
# karena satu kegagalan itu sempat terjadi.
& $NssmPath set $ServiceName AppExit Default Restart | Out-Null
& $NssmPath set $ServiceName AppRestartDelay 2000    | Out-Null
# AppThrottle 5 dtk: proses yang mati lebih cepat dari ini dianggap crash-loop dan
# dijedanya diperpanjang NSSM, supaya log tak terkubur banjir restart.
& $NssmPath set $ServiceName AppThrottle 5000        | Out-Null

# ── Shutdown ──
# NSSM mengirim Ctrl+C dulu; edge-api menanganinya seperti SIGTERM dan menutup gerbang
# serta controller dengan rapi. 20 dtk sejalan dengan TimeoutStopSec di unit systemd.
& $NssmPath set $ServiceName AppStopMethodConsole 20000 | Out-Null
& $NssmPath set $ServiceName AppStopMethodWindow  5000  | Out-Null
& $NssmPath set $ServiceName AppStopMethodThreads 5000  | Out-Null

# ── Log ──
& $NssmPath set $ServiceName AppStdout       (Join-Path $LogDir "edge-api.log")     | Out-Null
& $NssmPath set $ServiceName AppStderr       (Join-Path $LogDir "edge-api.err.log") | Out-Null
& $NssmPath set $ServiceName AppRotateFiles  1        | Out-Null
& $NssmPath set $ServiceName AppRotateOnline 1        | Out-Null
& $NssmPath set $ServiceName AppRotateBytes  10485760 | Out-Null

& $NssmPath start $ServiceName | Out-Null

Write-Host ""
Write-Host "Service '$ServiceName' terpasang dan berjalan."
Write-Host "  Status : sc query $ServiceName"
Write-Host "  Log    : $LogDir"
Write-Host "  Sehat  : curl http://localhost:8080/api/v1/health"
Write-Host ""
Write-Host "LANGKAH BERIKUTNYA — pasang watchdog-edge-api.ps1 sebagai Scheduled Task."
Write-Host "Tanpa itu, edge-api yang MEMBEKU (bukan mati) tidak akan pernah dinyalakan ulang."
