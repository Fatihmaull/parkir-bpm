// Package ids menghasilkan UUIDv7 untuk primary key yang dibuat di Edge (PRD §10.3/§11.2).
// UUIDv7 time-ordered → indeks PostgreSQL tetap efisien, dan aman untuk sync idempoten
// (INSERT ... ON CONFLICT (id) DO NOTHING) karena unik lintas node.
package ids

import "github.com/google/uuid"

// NewV7 mengembalikan UUIDv7 baru sebagai string. Panik hanya bila sumber acak OS gagal total.
func NewV7() string {
	u, err := uuid.NewV7()
	if err != nil {
		panic("ids: gagal membuat UUIDv7: " + err.Error())
	}
	return u.String()
}

// Parse memvalidasi & menormalkan string UUID.
func Parse(s string) (string, bool) {
	u, err := uuid.Parse(s)
	if err != nil {
		return "", false
	}
	return u.String(), true
}
