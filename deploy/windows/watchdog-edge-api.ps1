# Watchdog eksternal untuk edge-api di Windows (task 3.1).
#
# Windows tak punya padanan sd_notify/WatchdogSec, jadi proses yang MEMBEKU — deadlock,
# goroutine terkunci — tetap terbaca "Running" oleh Service Control Manager selamanya.
# NSSM tak menolong: ia hanya menyalakan ulang proses yang mati. Skrip ini menutup celah
# itu dari luar.
#
# Pasang sebagai Scheduled Task yang berjalan tiap menit, sebagai SYSTEM:
#
#   $a = New-ScheduledTaskAction -Execute "powershell.exe" `
#          -Argument "-NoProfile -ExecutionPolicy Bypass -File C:\parkir\watchdog-edge-api.ps1"
#   $t = New-ScheduledTaskTrigger -Once -At (Get-Date) `
#          -RepetitionInterval (New-TimeSpan -Minutes 1)
#   $p = New-ScheduledTaskPrincipal -UserId "SYSTEM" -LogonType ServiceAccount -RunLevel Highest
#   Register-ScheduledTask -TaskName "edge-api-watchdog" -Action $a -Trigger $t -Principal $p
#
# YANG DIUJI: apakah proses masih MENJAWAB. Bukan apakah gerbangnya sehat.
#
# Perbedaan itu menentukan. Gerbang yang controller-nya tercabut akan melaporkan
# gates_status "down" — dan itu memang benar — tetapi menyalakan ulang edge-api TIDAK
# menyambungkan kabel, sementara ia MENJATUHKAN gerbang-gerbang lain di lahan yang masih
# melayani dengan baik (P8). Karena itu skrip ini sengaja tidak melihat isi jawaban sama
# sekali; yang penting hanya jawabannya datang. Lihat K26 di docs/CATATAN_KEPUTUSAN.md.

[CmdletBinding()]
param(
    [string]$HealthUrl   = "http://localhost:8080/api/v1/health",
    [string]$ServiceName = "edge-api",
    [int]$TimeoutSeconds = 5,
    [int]$Percobaan      = 3,
    [string]$LogPath     = "C:\parkir\logs\watchdog.log"
)

$ErrorActionPreference = "Stop"

function Catat($pesan) {
    $baris = "{0} {1}" -f (Get-Date -Format "yyyy-MM-ddTHH:mm:ssK"), $pesan
    try {
        $dir = Split-Path -Parent $LogPath
        if ($dir -and -not (Test-Path $dir)) { New-Item -ItemType Directory -Force -Path $dir | Out-Null }
        Add-Content -Path $LogPath -Value $baris
    } catch { }
    Write-Host $baris
}

# Service yang memang sengaja dihentikan operator TIDAK boleh dinyalakan paksa oleh
# watchdog — itu merampas kendali dari orang yang sedang bekerja di panel.
$svc = Get-Service -Name $ServiceName -ErrorAction SilentlyContinue
if ($null -eq $svc) {
    Catat "service '$ServiceName' tidak terpasang — watchdog berhenti"
    exit 0
}
if ($svc.Status -ne "Running") {
    Catat "service '$ServiceName' berstatus $($svc.Status) — dibiarkan (kemungkinan dihentikan sengaja)"
    exit 0
}

# Beberapa percobaan, bukan satu. Satu permintaan yang gagal karena GC atau lonjakan
# beban sesaat bukan bukti proses beku, dan watchdog yang gugup lebih merusak daripada
# tak ada sama sekali.
$menjawab = $false
for ($i = 1; $i -le $Percobaan; $i++) {
    try {
        $r = Invoke-WebRequest -Uri $HealthUrl -TimeoutSec $TimeoutSeconds -UseBasicParsing
        if ($r.StatusCode -eq 200) { $menjawab = $true; break }
        Catat "percobaan $i/$Percobaan: status HTTP $($r.StatusCode)"
    } catch {
        Catat "percobaan $i/$Percobaan: tak menjawab — $($_.Exception.Message)"
    }
    if ($i -lt $Percobaan) { Start-Sleep -Seconds 2 }
}

if ($menjawab) { exit 0 }

Catat "edge-api tidak menjawab setelah $Percobaan percobaan — service dinyalakan ulang"
try {
    Restart-Service -Name $ServiceName -Force
    Catat "service '$ServiceName' dinyalakan ulang"
} catch {
    Catat "GAGAL menyalakan ulang '$ServiceName': $($_.Exception.Message)"
    exit 1
}
