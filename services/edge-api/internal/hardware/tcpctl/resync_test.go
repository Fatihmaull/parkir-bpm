package tcpctl

import (
	"testing"
	"time"
)

// Seed mengisi kanal yang belum punya nilai diakui — inilah keadaan tepat setelah
// Reset dipanggil karena koneksi putus.
func TestSeedMengisiKanalYangBelumDiketahui(t *testing.T) {
	d := NewDebouncer(10*time.Millisecond, nil)
	t.Cleanup(d.Stop)

	if high, diketahui := d.Stable(2); diketahui || high {
		t.Fatalf("sebelum seed: Stable(2) = (%v,%v), want (false,false)", high, diketahui)
	}
	if !d.Seed(2, true) {
		t.Fatal("Seed(2,true) = false, want true — kanal belum diketahui")
	}
	high, diketahui := d.Stable(2)
	if !diketahui || !high {
		t.Fatalf("setelah seed: Stable(2) = (%v,%v), want (true,true)", high, diketahui)
	}
}

// Potret STAT tidak boleh menimpa nilai yang sudah dipelajari dari event nyata:
// balasan STAT dapat tiba setelah event yang lebih baru sudah masuk.
func TestSeedTidakMenimpaNilaiYangSudahDiakui(t *testing.T) {
	p := penampung()
	d := NewDebouncer(20*time.Millisecond, p.emit)
	t.Cleanup(d.Stop)

	d.Observe(1, true)
	p.tunggu(t, time.Second)

	if d.Seed(1, false) {
		t.Fatal("Seed pada kanal yang sudah diakui = true, want false")
	}
	if high, _ := d.Stable(1); !high {
		t.Fatal("potret STAT yang lebih tua menimpa nilai dari event nyata")
	}
}

// Hitungan mundur dari pembacaan nyata harus tetap menang atas nilai seed, karena ia
// berasal dari kejadian yang lebih baru daripada potret.
func TestSeedTidakMembatalkanHitunganMundurNyata(t *testing.T) {
	p := penampung()
	d := NewDebouncer(60*time.Millisecond, p.emit)
	t.Cleanup(d.Stop)

	d.Observe(3, true) // pembacaan nyata mulai dihitung mundur
	if !d.Seed(3, false) {
		t.Fatal("Seed(3,false) = false, want true — kanal belum punya nilai diakui")
	}
	if high, diketahui := d.Stable(3); !diketahui || high {
		t.Fatalf("tepat setelah seed: Stable(3) = (%v,%v), want (false,true)", high, diketahui)
	}

	// Saat jendela habis, pembacaan nyata menimpa nilai seed.
	if ev := p.tunggu(t, time.Second); !ev.High || ev.LoopID != 3 {
		t.Fatalf("event = %+v, want kanal 3 High=true", ev)
	}
	if high, _ := d.Stable(3); !high {
		t.Fatal("pembacaan nyata tidak menimpa nilai seed setelah jendela habis")
	}
}

// Seed sendiri tidak pernah memancarkan event: yang mengumumkan kanal HIGH hasil
// potret adalah Device.tanamkanStat, supaya keputusan "hanya HIGH yang dikabarkan"
// terkumpul di satu tempat.
func TestSeedTidakMemancarkanEvent(t *testing.T) {
	p := penampung()
	d := NewDebouncer(20*time.Millisecond, p.emit)
	t.Cleanup(d.Stop)

	d.Seed(1, true)
	d.Seed(2, false)
	p.takAdaEvent(t, 60*time.Millisecond)
}

// Seed yang kebetulan bernilai sama dengan pembacaan nyata yang sedang dihitung mundur
// tidak boleh menghasilkan tepi kedua untuk satu kendaraan: Device sudah mengumumkan
// nilai seed-nya, jadi stabilkan harus menahan diri saat nilainya tak berubah.
func TestSeedTidakMenggandakanTepi(t *testing.T) {
	p := penampung()
	d := NewDebouncer(20*time.Millisecond, p.emit)
	t.Cleanup(d.Stop)

	d.Observe(4, true) // pembacaan nyata sedang dihitung mundur
	d.Seed(4, true)    // potret STAT menyusul dengan nilai yang sama

	// Jendela habis tanpa emit susulan — kalau tidak, state machine menerima dua tepi
	// naik untuk satu kendaraan yang sama.
	p.takAdaEvent(t, 80*time.Millisecond)

	if high, diketahui := d.Stable(4); !diketahui || !high {
		t.Fatalf("Stable(4) = (%v,%v), want (true,true)", high, diketahui)
	}
}

// Seed pada penyaring yang sudah berhenti tidak boleh menghidupkan kembali state.
func TestSeedDiabaikanSetelahStop(t *testing.T) {
	d := NewDebouncer(10*time.Millisecond, nil)
	d.Stop()

	if d.Seed(1, true) {
		t.Fatal("Seed setelah Stop = true, want false")
	}
}
