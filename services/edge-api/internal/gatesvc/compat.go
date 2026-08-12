package gatesvc

// Penunjuk gerbang "pertama" untuk pemakai yang masih menganggap ada tepat satu gerbang
// masuk dan satu gerbang keluar.
//
// Di lahan dengan lebih dari satu gerbang masuk, jalan pintas ini menyembunyikan gerbang
// selebihnya. Ia ada supaya endpoint lama tetap hidup selama dashboard bermigrasi ke API
// beralamat code — bukan sebagai cara pakai yang benar. Pemakaian yang benar:
// Service.Gate(code).
//
// Pembungkus tipis semacam Service.EntryState() sengaja TIDAK disediakan: memanggil
// r := svc.EntryGate() lalu r.State() membuat pemanggil melihat bahwa ia sedang memilih
// satu gerbang tertentu, alih-alih mengira Edge hanya punya satu.

// EntryGate mengembalikan gerbang masuk pertama, atau nil bila lahan tak punya.
func (s *Service) EntryGate() *Runner { return s.pertama(KindEntry) }

// ExitGate mengembalikan gerbang keluar pertama, atau nil bila lahan tak punya.
func (s *Service) ExitGate() *Runner { return s.pertama(KindExit) }

func (s *Service) pertama(kind GateKind) *Runner {
	for _, code := range s.order {
		if r := s.gates[code]; r.spec.Kind == kind {
			return r
		}
	}
	return nil
}
