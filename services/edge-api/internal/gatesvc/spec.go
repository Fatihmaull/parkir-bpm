package gatesvc

import (
	"context"
	"fmt"

	"github.com/jabar-creative/parkir/edge-api/internal/config"
)

// GateKind membedakan gerbang masuk dan keluar (kolom `gates.kind`).
type GateKind string

const (
	KindEntry GateKind = "ENTRY"
	KindExit  GateKind = "EXIT"
)

// Valid melaporkan apakah jenis gerbang dikenal.
func (k GateKind) Valid() bool { return k == KindEntry || k == KindExit }

// Transport yang didukung untuk satu gerbang.
const (
	TransportSim = "sim"
	TransportTCP = "tcp"
)

// GateSpec mendeskripsikan satu gerbang di satu lahan.
//
// Code adalah identitas operasional gerbang ("GATE-IN-01") dan menjadi label setiap
// event yang dipancarkannya (task 2.2). Sebelumnya Edge hanya mengenal "entry" dan
// "exit" tunggal, sehingga lahan dengan dua gerbang masuk tak punya cara membedakan
// event keduanya.
type GateSpec struct {
	Code      string   `json:"code"`
	Kind      GateKind `json:"kind"`
	Transport string   `json:"transport"` // sim|tcp
	Endpoint  string   `json:"endpoint"`  // "ip:port" untuk transport tcp
}

// Validate memeriksa kewarasan satu spesifikasi gerbang.
func (s GateSpec) Validate() error {
	if s.Code == "" {
		return fmt.Errorf("gatesvc: gerbang tanpa code")
	}
	if !s.Kind.Valid() {
		return fmt.Errorf("gatesvc: gerbang %s punya kind %q (harus ENTRY|EXIT)", s.Code, s.Kind)
	}
	switch s.Transport {
	case TransportSim:
	case TransportTCP:
		if s.Endpoint == "" {
			return fmt.Errorf("gatesvc: gerbang %s bertransport tcp tanpa endpoint", s.Code)
		}
	default:
		return fmt.Errorf("gatesvc: gerbang %s punya transport %q (harus sim|tcp)", s.Code, s.Transport)
	}
	return nil
}

// GateSource memuat daftar gerbang satu lahan.
//
// Sumbernya kelak adalah tabel `gates` lewat repository pgx (task 5.1). Selama
// repository itu belum ada — edge-api saat ini berjalan tanpa lapisan basis data sama
// sekali — daftar gerbang datang dari konfigurasi. Antarmuka ini memisahkan keduanya,
// sehingga penambahan sumber pgx nanti tidak menyentuh Service lagi.
type GateSource interface {
	LoadGates(ctx context.Context) ([]GateSpec, error)
}

// StaticSource adalah daftar gerbang tetap.
type StaticSource []GateSpec

// LoadGates memenuhi GateSource.
func (s StaticSource) LoadGates(context.Context) ([]GateSpec, error) {
	return append([]GateSpec(nil), s...), nil
}

// SpecsFromConfig menurunkan daftar gerbang dari konfigurasi environment.
//
// Bentuk `.env` sekarang hanya mengenal satu gerbang masuk dan satu keluar, jadi
// hasilnya dua gerbang. Yang berubah adalah mekanismenya: Service tak lagi menganggap
// jumlah itu pasti, sehingga sumber lain (pgx, berkas konfigurasi) dapat memberi
// sebanyak apa pun tanpa perubahan lebih lanjut.
func SpecsFromConfig(cfg *config.Config) StaticSource {
	if cfg == nil {
		return DefaultSpecs()
	}
	return StaticSource{
		{Code: "GATE-IN-01", Kind: KindEntry, Transport: normalTransport(cfg.GateIn.Transport), Endpoint: cfg.GateIn.Endpoint},
		{Code: "GATE-OUT-01", Kind: KindExit, Transport: normalTransport(cfg.GateOut.Transport), Endpoint: cfg.GateOut.Endpoint},
	}
}

// DefaultSpecs adalah satu gerbang masuk dan satu keluar di atas simulator — dipakai
// bila tak ada sumber lain (demo, uji).
func DefaultSpecs() StaticSource {
	return StaticSource{
		{Code: "GATE-IN-01", Kind: KindEntry, Transport: TransportSim},
		{Code: "GATE-OUT-01", Kind: KindExit, Transport: TransportSim},
	}
}

func normalTransport(t string) string {
	if t == TransportTCP {
		return TransportTCP
	}
	return TransportSim
}

// ValidateSpecs memeriksa keseluruhan daftar gerbang.
//
// Code yang kembar ditolak: ia adalah kunci gerbang di seluruh sistem — event,
// endpoint, dan pencarian runner semuanya memakainya. Dua gerbang bercode sama berarti
// perintah untuk yang satu diam-diam mengenai yang lain.
func ValidateSpecs(specs []GateSpec) error {
	if len(specs) == 0 {
		return fmt.Errorf("gatesvc: daftar gerbang kosong")
	}
	lihat := make(map[string]struct{}, len(specs))
	for _, s := range specs {
		if err := s.Validate(); err != nil {
			return err
		}
		if _, kembar := lihat[s.Code]; kembar {
			return fmt.Errorf("gatesvc: code gerbang %q muncul lebih dari sekali", s.Code)
		}
		lihat[s.Code] = struct{}{}
	}
	return nil
}
