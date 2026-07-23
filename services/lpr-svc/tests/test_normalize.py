import pathlib
import sys

sys.path.insert(0, str(pathlib.Path(__file__).resolve().parents[1]))

from lpr_svc.normalize import normalize, verdict  # noqa: E402


def test_basic_plates():
    assert normalize("b 1234 cd") == ("B1234CD", True)
    assert normalize("  D 5678 XY ") == ("D5678XY", True)
    assert normalize("AB12") == ("AB12", True)


def test_ambiguous_letter_to_digit_in_number():
    # 'O' dan 'I' di segmen angka dikoreksi ke 0 dan 1.
    assert normalize("B12O4") == ("B1204", True)
    assert normalize("D5I") == ("D51", True)


def test_ambiguous_digit_to_letter_in_suffix():
    # '0' di segmen sufiks dikoreksi ke 'O'.
    assert normalize("B1234C0") == ("B1234CO", True)


def test_invalid():
    assert normalize("@@@")[1] is False
    assert normalize("")[1] is False


def test_verdict():
    assert verdict(0.95, True) == "AUTO_ACCEPT"
    assert verdict(0.70, True) == "NEEDS_REVIEW"
    assert verdict(0.50, True) == "UNREAD"
    assert verdict(0.95, False) == "UNREAD"        # pola tidak valid
    assert verdict(0.95, True, timed_out=True) == "UNREAD"
