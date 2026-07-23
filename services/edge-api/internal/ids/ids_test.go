package ids

import "testing"

func TestNewV7UniqueAndParseable(t *testing.T) {
	seen := make(map[string]bool)
	for i := 0; i < 1000; i++ {
		id := NewV7()
		if seen[id] {
			t.Fatalf("UUIDv7 duplikat: %s", id)
		}
		seen[id] = true
		if _, ok := Parse(id); !ok {
			t.Fatalf("UUIDv7 tidak dapat di-parse: %s", id)
		}
		// Karakter versi (posisi 14) harus '7'.
		if id[14] != '7' {
			t.Fatalf("bukan versi 7: %s", id)
		}
	}
}

func TestParseRejectsGarbage(t *testing.T) {
	if _, ok := Parse("bukan-uuid"); ok {
		t.Fatal("string sembarang seharusnya ditolak")
	}
}
