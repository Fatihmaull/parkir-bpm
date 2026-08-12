package gatesvc

import (
	"testing"
	"time"

	"github.com/jabar-creative/parkir/edge-api/internal/audit"
	"github.com/jabar-creative/parkir/edge-api/internal/gate"
	"github.com/jabar-creative/parkir/edge-api/internal/memstore"
	"github.com/jabar-creative/parkir/edge-api/internal/wsbus"
)

func newSvc(t *testing.T) (*Service, *wsbus.Hub, *memstore.Store) {
	t.Helper()
	hub := wsbus.NewHub()
	store := memstore.New("node-test", time.Now)
	store.SetRate("mobil", gate.RateCard{BaseRate: 5000})
	svc, err := New(Config{
		NodeID: "node-test", TenantID: "t1", SiteID: "s1",
		Site: gate.SiteConfig{GraceMinutes: 15, MaxDailyRate: 30000},
	}, hub, store)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	svc.Start()
	t.Cleanup(svc.Close)
	return svc, hub, store
}

// Siklus masuk penuh lewat Service (controller + memstore + timer nyata + hub).
func TestServiceEntryCycleAndAuditChain(t *testing.T) {
	svc, hub, store := newSvc(t)
	ch, un := hub.Subscribe()
	defer un()

	_ = svc.EntryGate().FireEntry(gate.Event{Kind: gate.EvLD1, High: true})
	if gate.State(svc.EntryGate().State()) != gate.StateVehiclePresent {
		t.Fatalf("state=%s", gate.State(svc.EntryGate().State()))
	}
	_ = svc.EntryGate().FireEntry(gate.Event{Kind: gate.EvButton})
	_ = svc.EntryGate().FireEntry(gate.Event{Kind: gate.EvTicketTaken})
	if gate.State(svc.EntryGate().State()) != gate.StateOpen {
		t.Fatalf("harus OPEN, state=%s", gate.State(svc.EntryGate().State()))
	}
	_ = svc.EntryGate().FireEntry(gate.Event{Kind: gate.EvLD1, High: false})
	_ = svc.EntryGate().FireEntry(gate.Event{Kind: gate.EvLD2, High: true})
	_ = svc.EntryGate().FireEntry(gate.Event{Kind: gate.EvLD2, High: false})
	if gate.State(svc.EntryGate().State()) != gate.StateIdle {
		t.Fatalf("harus IDLE, state=%s", gate.State(svc.EntryGate().State()))
	}

	// Rantai audit nyata harus terverifikasi.
	entries := store.AuditEntries()
	if len(entries) == 0 {
		t.Fatal("harus ada entri audit")
	}
	if broken, ok := audit.Verify(entries); !ok {
		t.Fatalf("rantai audit rusak di seq %d", broken)
	}

	// Hub harus menerima event gate.state_changed.
	got := drain(ch, 200*time.Millisecond)
	if !contains(got, "gate.state_changed") || !contains(got, "vehicle.entered") {
		t.Fatalf("event WS kurang: %v", got)
	}
}

// Event hardware tak-diminta (sim) diteruskan ke controller oleh goroutine forwarder.
func TestServiceForwardsHardwareEvents(t *testing.T) {
	svc, _, _ := newSvc(t)
	svc.EntryGate().Sim().LoopPre.Drive(true) // simulasikan kendaraan tiba di LD1
	// Tunggu forwarder menggerakkan state (async).
	if !waitState(func() bool { return gate.State(svc.EntryGate().State()) == gate.StateVehiclePresent }, time.Second) {
		t.Fatalf("forwarder tidak menggerakkan state, state=%s", gate.State(svc.EntryGate().State()))
	}
}

// Anti-passback lewat memstore: member masuk lalu tap masuk lagi → ditolak.
func TestServiceMemberAntipassback(t *testing.T) {
	svc, _, store := newSvc(t)
	store.AddMember("04AABBCC", []string{"B1XYZ"}, "mobil", time.Now().Add(24*time.Hour))

	_ = svc.EntryGate().FireEntry(gate.Event{Kind: gate.EvLD1, High: true})
	_ = svc.EntryGate().FireEntry(gate.Event{Kind: gate.EvRFID, UID: "04AABBCC"})
	if gate.State(svc.EntryGate().State()) != gate.StateOpen {
		t.Fatalf("member pertama harus diterima (OPEN), state=%s", gate.State(svc.EntryGate().State()))
	}
	// Selesaikan siklus.
	_ = svc.EntryGate().FireEntry(gate.Event{Kind: gate.EvLD1, High: false})
	_ = svc.EntryGate().FireEntry(gate.Event{Kind: gate.EvLD2, High: true})
	_ = svc.EntryGate().FireEntry(gate.Event{Kind: gate.EvLD2, High: false})

	// Tap masuk lagi tanpa keluar → anti-passback menolak.
	_ = svc.EntryGate().FireEntry(gate.Event{Kind: gate.EvLD1, High: true})
	_ = svc.EntryGate().FireEntry(gate.Event{Kind: gate.EvRFID, UID: "04AABBCC"})
	if gate.State(svc.EntryGate().State()) != gate.StateRejected {
		t.Fatalf("tap kedua harus ditolak (REJECTED), state=%s", gate.State(svc.EntryGate().State()))
	}
}

// ── helpers ──

func drain(ch <-chan wsbus.Event, d time.Duration) []string {
	var names []string
	deadline := time.After(d)
	for {
		select {
		case e := <-ch:
			names = append(names, e.Name)
		case <-deadline:
			return names
		}
	}
}

func contains(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}

func waitState(pred func() bool, d time.Duration) bool {
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if pred() {
			return true
		}
		time.Sleep(5 * time.Millisecond)
	}
	return pred()
}
