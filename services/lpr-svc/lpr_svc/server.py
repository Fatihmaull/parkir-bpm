"""lpr-svc — server gRPC. PRD §4.2/§7.1.

Task 6.1: server gRPC NYATA (bukan lagi placeholder yang cuma print pesan) — transport
sungguhan, "kecerdasan" di baliknya masih placeholder sampai model dipasang (task 6.2,
YOLOv8n + EasyOCR/Tesseract). "Boleh mati? Ya — degradasi ke UNREAD" (PRD §4.2): kalau proses
ini mati, edge-api TETAP melayani gerbang, cuma hasil OCR-nya UNREAD (lihat lpr.Degraded
di sisi Go) — server ini bukan dependensi keras.

Stub (lpr_pb2.py, lpr_pb2_grpc.py) SENGAJA tak di-commit (.gitignore) — generate dulu:
    ./gen_proto.sh
sebelum menjalankan server ini atau test yang menyentuhnya (lihat komentar di skrip itu
untuk kenapa perlu penambalan sed, bukan cuma protoc polos).
"""
from __future__ import annotations

import os
import time
from concurrent import futures

import grpc

from . import lpr_pb2, lpr_pb2_grpc
from .normalize import normalize, verdict

ENGINE_VERSION = os.getenv("LPR_ENGINE_VERSION", "yolov8n-easyocr-0.1-stub")
DEFAULT_ADDR = os.getenv("LPR_GRPC_ADDR", "0.0.0.0:50051")


def recognize(image: bytes, gate_id: str, captured_at: int) -> dict:
    """Pipeline LPR (PRD §7.1). Placeholder: belum menjalankan model nyata.

    Mengembalikan dict siap-tulis untuk `ocr_logs` (PRD §7.2). Selalu menulis log —
    sukses maupun gagal (P2 / PRD §7.1 baris terakhir).
    """
    t0 = time.monotonic()

    # TODO(task 6.2): YOLOv8n deteksi + crop plat → EasyOCR/Tesseract → raw_text, confidence.
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


class LPRServicer(lpr_pb2_grpc.LPRServicer):
    def Recognize(self, request, context):
        result = recognize(request.image, request.gate_id, request.captured_at)
        return lpr_pb2.RecognizeResponse(
            raw_text=result["raw_text"],
            normalized_plate=result["normalized_plate"],
            confidence=result["confidence"],
            vehicle_type=result["vehicle_type"],
            verdict=result["verdict"],
            latency_ms=result["latency_ms"],
            engine_version=result["engine_version"],
        )

    def HealthCheck(self, request, context):
        return lpr_pb2.HealthResponse(ok=True, engine_version=ENGINE_VERSION)


def serve(addr: str = DEFAULT_ADDR) -> grpc.Server:
    """Membangun & menjalankan server (non-blocking — pemanggil yang menunggu)."""
    server = grpc.server(futures.ThreadPoolExecutor(max_workers=4))
    lpr_pb2_grpc.add_LPRServicer_to_server(LPRServicer(), server)
    server.add_insecure_port(addr)
    server.start()
    return server


def main() -> None:
    server = serve()
    print(f"lpr-svc gRPC server hidup di {DEFAULT_ADDR} — engine={ENGINE_VERSION}")
    server.wait_for_termination()


if __name__ == "__main__":
    main()
