#!/usr/bin/env bash
# Menghasilkan stub gRPC Python dari proto/lpr.proto (task 6.1).
#
# Sengaja TAK di-commit (.gitignore: *_pb2.py, *_pb2_grpc.py) — dibangkitkan ulang di sini,
# sekali per checkout/CI run, bukan disimpan sebagai artefak repo yang bisa basi terhadap
# proto sumbernya.
#
# Menambal quirk grpc_tools.protoc: plugin Python-nya menulis `import lpr_pb2 as lpr__pb2`
# (absolut), yang gagal begitu lpr_svc dipakai sebagai PAKET (`python -m lpr_svc.server`),
# bukan dijalankan langsung dari direktori ini. Tak ada opsi command-line untuk memintanya
# menulis import relatif — makanya ditambal lewat sed di sini, bukan manual tiap developer.
#
#   services/lpr-svc/gen_proto.sh   # dari mana saja, path relatif terhadap berkas ini

set -euo pipefail
cd "$(dirname "${BASH_SOURCE[0]}")"

python3 -m grpc_tools.protoc -Iproto --python_out=lpr_svc --grpc_python_out=lpr_svc proto/lpr.proto

sed -i.bak 's/^import lpr_pb2 as lpr__pb2/from . import lpr_pb2 as lpr__pb2/' lpr_svc/lpr_pb2_grpc.py
rm -f lpr_svc/lpr_pb2_grpc.py.bak

echo "Stub tergenerasi: lpr_svc/lpr_pb2.py, lpr_svc/lpr_pb2_grpc.py"
