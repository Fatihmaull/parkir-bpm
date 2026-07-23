"""lpr-svc — server gRPC. PRD §4.2/§7.1.

Scaffold Minggu 1: server hidup + pipeline placeholder yang memakai normalisasi nyata.
Model YOLOv8n + EasyOCR/Tesseract dipasang pada Minggu 2 (di balik LPR_ENGINE_VERSION).
"boleh mati? Ya — degradasi ke UNREAD" (PRD §4.2).

Untuk mengaktifkan gRPC nyata, hasilkan stub dari proto/lpr.proto:
    python -m grpc_tools.protoc -Iproto --python_out=lpr_svc --grpc_python_out=lpr_svc proto/lpr.proto
"""
from __future__ import annotations

import os
import time

from .normalize import normalize, verdict

ENGINE_VERSION = os.getenv("LPR_ENGINE_VERSION", "yolov8n-easyocr-0.1-stub")


def recognize(image: bytes, gate_id: str, captured_at: int) -> dict:
    """Pipeline LPR (PRD §7.1). Placeholder: belum menjalankan model nyata.

    Mengembalikan dict siap-tulis untuk `ocr_logs` (PRD §7.2). Selalu menulis log —
    sukses maupun gagal (P2 / PRD §7.1 baris terakhir).
    """
    t0 = time.monotonic()

    # TODO(Minggu 2): YOLOv8n deteksi + crop plat → EasyOCR/Tesseract → raw_text, confidence.
    raw_text = ""      # placeholder: tanpa model, tidak ada bacaan
    confidence = 0.0
    vehicle_type = "mobil"

    plate, valid = normalize(raw_text)
    v = verdict(confidence, valid, timed_out=False)
    latency_ms = int((time.monotonic() - t0) * 1000)

    return {
        "raw_text": raw_text,
        "normalized_plate": plate,
        "confidence": confidence,
        "vehicle_type": vehicle_type,
        "verdict": v,
        "latency_ms": latency_ms,
        "engine_version": ENGINE_VERSION,
    }


def main() -> None:
    # TODO(Minggu 2): grpc.server + register LPRServicer + listen LPR_GRPC_ADDR.
    print(f"lpr-svc scaffold siap — engine={ENGINE_VERSION}. "
          "gRPC server dipasang Minggu 2 (lihat docstring).")


if __name__ == "__main__":
    main()
