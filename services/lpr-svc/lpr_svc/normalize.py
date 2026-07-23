"""Normalisasi plat nomor Indonesia (PRD §7.1).

Pipeline: uppercase, hapus spasi/strip, segmentasi [huruf][angka][huruf],
koreksi karakter ambigu berdasar posisi, lalu validasi pola.

Pola plat Indonesia: ^[A-Z]{1,2}[0-9]{1,4}[A-Z]{0,3}$
  - Kode wilayah (1-2 huruf) · nomor urut (1-4 angka) · seri (0-3 huruf).

Catatan: koreksi ambigu bersifat heuristik. Kasus batas (mis. huruf yang sah tepat di
perbatasan segmen) diserahkan ke penyetelan Fase 2 dengan data lapangan (PRD §17 item 3).
"""
from __future__ import annotations

import re

PLATE_RE = re.compile(r"^[A-Z]{1,2}[0-9]{1,4}[A-Z]{0,3}$")

# Koreksi ambigu di segmen ANGKA: huruf yang sering salah baca → digit.
_LETTER_TO_DIGIT = {"O": "0", "I": "1", "S": "5", "B": "8", "Z": "2", "G": "6"}
# Koreksi ambigu di segmen HURUF: digit yang sering salah baca → huruf.
_DIGIT_TO_LETTER = {"0": "O", "1": "I", "5": "S", "8": "B", "2": "Z", "6": "G"}


def _strip(raw: str) -> str:
    return re.sub(r"[^A-Z0-9]", "", raw.upper())


def normalize(raw: str) -> tuple[str, bool]:
    """Kembalikan (normalized_plate, is_valid)."""
    s = _strip(raw)
    if not s:
        return "", False

    i, n = 0, len(s)

    # Prefiks: huruf di depan (maks 2). Berhenti pada digit — digit di awal tidak dikoreksi
    # menjadi huruf karena akan rancu dengan nomor urut.
    prefix = []
    while i < n and s[i].isalpha() and len(prefix) < 2:
        prefix.append(s[i])
        i += 1

    # Angka tengah (maks 4): terima digit + huruf-ambigu yang dikoreksi menjadi digit.
    digits = []
    while i < n and len(digits) < 4 and (s[i].isdigit() or s[i] in _LETTER_TO_DIGIT):
        digits.append(_LETTER_TO_DIGIT[s[i]] if s[i].isalpha() else s[i])
        i += 1

    # Sufiks (maks 3): sisanya sebagai huruf; digit-ambigu dikoreksi menjadi huruf.
    suffix = []
    while i < n and len(suffix) < 3:
        suffix.append(_DIGIT_TO_LETTER.get(s[i], s[i]) if s[i].isdigit() else s[i])
        i += 1

    plate = "".join(prefix) + "".join(digits) + "".join(suffix)
    return plate, bool(PLATE_RE.match(plate))


def verdict(confidence: float, valid: bool, timed_out: bool = False) -> str:
    """Tentukan verdict OCR (PRD §7.2)."""
    if timed_out or confidence < 0.60 or not valid:
        return "UNREAD"
    if confidence >= 0.85:
        return "AUTO_ACCEPT"
    return "NEEDS_REVIEW"
