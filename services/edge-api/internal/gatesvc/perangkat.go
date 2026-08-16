package gatesvc

import (
	"context"
	"fmt"
	"time"

	hw "github.com/jabar-creative/parkir/edge-api/internal/hardware"
	"github.com/jabar-creative/parkir/edge-api/internal/hardware/sim"
	"github.com/jabar-creative/parkir/edge-api/internal/hardware/tcpctl"
)

// perangkat menyatukan seluruh perangkat Layer-2 satu gerbang, entah tersimulasi atau
// nyata, sehingga state machine tidak perlu tahu bedanya (PRD v3 §6.1).
type perangkat struct {
	Barrier  hw.Barrier
	Light    hw.IndicatorLight
	Printer  hw.TicketPrinter // nil untuk gerbang keluar
	RFID     hw.RFIDReader
	LoopPre  hw.LoopDetector
	LoopPost hw.LoopDetector

	// simGate terisi hanya bila SELURUH gerbang ini tersimulasi. Injeksi event sinkron
	// (Field Monitor) hanya masuk akal pada mode itu.
	simGate *sim.Gate

	// Terisi bila gerbang memakai controller nyata.
	tcpGate *tcpctl.Gate
	tcpDev  *tcpctl.Device

	// disimulasikan mendaftar perangkat yang MASIH tersimulasi meski gerbangnya nyata.
	// Daftar ini sengaja dibuka ke API & log — perangkat palsu di jalur produksi tidak
	// boleh pernah tersamar sebagai perangkat sungguhan.
	disimulasikan []string
}

// Nyata melaporkan apakah gerbang memakai controller fisik.
func (p *perangkat) Nyata() bool { return p.tcpGate != nil }

// Status melaporkan kondisi koneksi controller; kosong untuk gerbang tersimulasi.
func (p *perangkat) Status() string {
	if p.tcpDev == nil {
		return ""
	}
	return p.tcpDev.Status().String()
}

// tutup menghentikan perangkat nyata bila ada.
func (p *perangkat) tutup() {
	if p.tcpGate != nil {
		_ = p.tcpGate.Close()
	}
	if p.tcpDev != nil {
		_ = p.tcpDev.Close()
	}
}

// buatPerangkatSim merangkai gerbang yang seluruhnya tersimulasi.
func buatPerangkatSim(kind GateKind) *perangkat {
	g := sim.NewGate()
	p := &perangkat{
		Barrier: g.Barrier, Light: g.Light, RFID: g.RFID,
		LoopPre: g.LoopPre, LoopPost: g.LoopPost,
		simGate: g,
	}
	if kind == KindEntry {
		p.Printer = g.Printer
	}
	return p
}

// buatPerangkatTCP merangkai gerbang di atas controller A6/A9 nyata.
//
// Printer tiket TETAP tersimulasi: adapter printer konkret adalah task 4.1 yang masih
// menunggu konfirmasi merek/protokol dari klien (H1). Gerbang masuk tak dapat beroperasi
// tanpa printer, jadi pilihannya antara memakai simulator sementara — dan mengumumkannya
// dengan keras — atau tidak bisa menguji gerbang masuk nyata sama sekali. Yang dipilih
// yang pertama; lihat `disimulasikan`.
func buatPerangkatTCP(ctx context.Context, spec GateSpec, cfg Config) (*perangkat, error) {
	dev := tcpctl.NewDevice(spec.Endpoint)

	// Gerbang dirangkai SEBELUM dev.Start, bukan sesudahnya.
	//
	// NewGate-lah yang memasang penjaga interlock dan rekonsiliator. Dijalankan setelah
	// Start, koneksi pertama bisa terbentuk — beserta resync dan rekonsiliasinya — sebelum
	// keduanya terpasang, sehingga palang yang ditinggalkan terbuka oleh proses sebelumnya
	// luput dari pemeriksaan pada satu-satunya kesempatan yang ada. Merangkai lebih dulu
	// menutup jendela itu: belum ada koneksi, jadi belum ada yang bisa terlewat.
	gcfg := tcpctl.DefaultGateConfig(tcpctl.GateKind(spec.Kind))
	g, err := tcpctl.NewGate(dev, gcfg)
	if err != nil {
		_ = dev.Close()
		return nil, fmt.Errorf("gatesvc: gerbang %s: %w", spec.Code, err)
	}
	dev.Start(ctx)

	p := &perangkat{
		Barrier: g.Barrier, Light: g.Light, RFID: g.RFID,
		LoopPre: g.LoopPre, LoopPost: g.LoopPost,
		tcpGate: g, tcpDev: dev,
	}
	if spec.Kind == KindEntry {
		// Satu-satunya perangkat palsu yang tersisa di jalur nyata.
		p.Printer = sim.NewGate().Printer
		p.disimulasikan = append(p.disimulasikan, "printer")
	}
	_ = cfg // disediakan untuk penyetelan per-gerbang berikutnya (ambang, peta pin dari DB)
	return p, nil
}

// buatPerangkat memilih perangkat sesuai transport gerbang.
func buatPerangkat(spec GateSpec, cfg Config) (*perangkat, error) {
	if spec.Transport == TransportTCP {
		return buatPerangkatTCP(context.Background(), spec, cfg)
	}
	return buatPerangkatSim(spec.Kind), nil
}

// ErrBukanSimulator dikembalikan bila injeksi event simulator dicoba pada gerbang nyata.
var ErrBukanSimulator = fmt.Errorf(
	"gatesvc: gerbang memakai controller nyata — event perangkat datang dari lapangan, tak bisa disuntik")

var _ = time.Second // dipertahankan untuk penyetelan ambang per-gerbang berikutnya
