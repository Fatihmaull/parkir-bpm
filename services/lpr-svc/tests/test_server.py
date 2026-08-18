"""Uji server gRPC (task 6.1) — bukan mock, klien gRPC sungguhan lewat loopback terhadap
server sungguhan yang dijalankan in-process pada port efemeral (OS yang memilih, port 0)
supaya aman dijalankan paralel/berulang di CI tanpa bentrok port."""
import pathlib
import sys
from concurrent import futures

sys.path.insert(0, str(pathlib.Path(__file__).resolve().parents[1]))

import grpc

from lpr_svc import lpr_pb2, lpr_pb2_grpc, server


def test_recognize_over_real_grpc():
    srv = grpc.server(futures.ThreadPoolExecutor(max_workers=2))
    lpr_pb2_grpc.add_LPRServicer_to_server(server.LPRServicer(), srv)
    port = srv.add_insecure_port("127.0.0.1:0")
    srv.start()
    try:
        with grpc.insecure_channel(f"127.0.0.1:{port}") as channel:
            stub = lpr_pb2_grpc.LPRStub(channel)
            resp = stub.Recognize(
                lpr_pb2.RecognizeRequest(image=b"frame-palsu", gate_id="GATE-IN-01", captured_at=0),
                timeout=2,
            )
            # Placeholder (task 6.2 belum jalan): selalu UNREAD, confidence 0 — yang
            # dibuktikan di sini TRANSPORT gRPC-nya, bukan akurasi model.
            assert resp.verdict == "UNREAD"
            assert resp.confidence == 0.0
            assert resp.engine_version  # bukan string kosong — benar-benar dari server

            health = stub.HealthCheck(lpr_pb2.HealthRequest(), timeout=2)
            assert health.ok is True
    finally:
        srv.stop(grace=0)
