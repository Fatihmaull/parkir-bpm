// Package store — penyimpanan agregat Cloud in-memory (pengganti Managed PostgreSQL saat demo).
// Menegakkan isolasi multi-tenant DI LAPISAN REPOSITORY (PRD §12.14/P6): setiap kueri operasional
// WAJIB menyertakan tenantID; pemanggil tidak dapat menembus tenant lain lewat manipulasi ID.
package store

import (
	"fmt"
	"sync"

	"github.com/jabar-creative/parkir/cloud-api/internal/auth"
)

type User struct {
	ID           string
	TenantID     string
	Email        string
	PasswordHash string
	FullName     string
	Role         auth.Role
	SiteScope    []string
}

type Site struct {
	ID       string `json:"id"`
	TenantID string `json:"tenant_id"`
	Code     string `json:"code"`
	Name     string `json:"name"`
	City     string `json:"city"`
}

// Txn — record hasil sinkronisasi dari Edge (vehicles_log dsb.), disimpan idempoten.
type Txn struct {
	AggregateID string         `json:"aggregate_id"`
	Aggregate   string         `json:"aggregate"`
	TenantID    string         `json:"tenant_id"`
	Payload     map[string]any `json:"payload"`
}

// Order — order pembayaran QRIS/e-wallet (§6.3).
type Order struct {
	OrderID  string `json:"order_id"`
	Provider string `json:"provider"`
	TenantID string `json:"tenant_id"`
	Amount   int64  `json:"amount"`
	Status   string `json:"status"` // PENDING|SETTLED
	QRString string `json:"qr_string,omitempty"`
}

type Store struct {
	mu     sync.RWMutex
	users  map[string]*User // key: email
	sites  []Site
	txns   map[string]*Txn   // key: aggregate_id (idempotensi §10.3)
	orders map[string]*Order // key: order_id (provider_ref, idempotensi webhook §6.3)
	seq    int

	// audit — jalur sync audit_logs TERPISAH (task 8.5), lock sendiri (lihat internal/store/audit.go).
	audit *auditStore
}

func New() *Store {
	return &Store{
		users: make(map[string]*User), txns: make(map[string]*Txn), orders: make(map[string]*Order),
		audit: newAuditStore(),
	}
}

// CreateOrder membuat order PENDING dan mengembalikannya (order_id sebagai provider_ref).
func (s *Store) CreateOrder(provider, tenantID string, amount int64, qr string) *Order {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.seq++
	o := &Order{
		OrderID:  fmt.Sprintf("ORD-%s-%06d", provider, s.seq),
		Provider: provider, TenantID: tenantID, Amount: amount, Status: "PENDING", QRString: qr,
	}
	s.orders[o.OrderID] = o
	return o
}

// SettleOrder menyelesaikan order secara idempoten. Kembalikan (found, alreadySettled).
// Webhook duplikat (PG sering kirim ulang) tidak boleh membuat pembayaran ganda (§6.3).
func (s *Store) SettleOrder(orderID string) (found, alreadySettled bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	o, ok := s.orders[orderID]
	if !ok {
		return false, false
	}
	if o.Status == "SETTLED" {
		return true, true
	}
	o.Status = "SETTLED"
	return true, false
}

func (s *Store) Order(orderID string) (*Order, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	o, ok := s.orders[orderID]
	return o, ok
}

// ── seed ──

func (s *Store) AddUser(u *User) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.users[u.Email] = u
}

func (s *Store) AddSite(si Site) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sites = append(s.sites, si)
}

// ── auth ──

func (s *Store) UserByEmail(email string) (*User, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	u, ok := s.users[email]
	return u, ok
}

// ── multi-tenant scoped reads (§12.14) ──

// Sites mengembalikan HANYA site milik tenantID; bila scope tidak kosong, dibatasi ke scope.
func (s *Store) Sites(tenantID string, scope []string) []Site {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []Site
	for _, si := range s.sites {
		if si.TenantID != tenantID {
			continue // isolasi tenant — tak pernah bocor lintas tenant
		}
		if len(scope) > 0 && !contains(scope, si.ID) {
			continue
		}
		out = append(out, si)
	}
	return out
}

// Transactions mengembalikan HANYA record milik tenantID.
func (s *Store) Transactions(tenantID string) []Txn {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []Txn
	for _, t := range s.txns {
		if t.TenantID == tenantID {
			out = append(out, *t)
		}
	}
	return out
}

// ── sync receiver (idempoten §10.3) ──

// ApplyBatch menyimpan record; duplikat (aggregate_id sama) dilewati → aman untuk pengiriman ulang.
// Kembalikan (applied, skipped).
func (s *Store) ApplyBatch(tenantID string, items []Txn) (applied, skipped int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, it := range items {
		if _, exists := s.txns[it.AggregateID]; exists {
			skipped++
			continue
		}
		cp := it
		cp.TenantID = tenantID
		s.txns[it.AggregateID] = &cp
		applied++
	}
	return
}

func contains(xs []string, v string) bool {
	for _, x := range xs {
		if x == v {
			return true
		}
	}
	return false
}
