package auth

import (
	"errors"
	"testing"
	"time"
)

func TestPasswordHashVerifyRoundTrip(t *testing.T) {
	// Params ringan untuk kecepatan test.
	p := Argon2Params{Memory: 8 * 1024, Iterations: 1, Parallelism: 1, SaltLen: 16, KeyLen: 32}
	hash, err := HashPassword("s3cret-parkir", p)
	if err != nil {
		t.Fatal(err)
	}
	if err := VerifyPassword("s3cret-parkir", hash); err != nil {
		t.Fatalf("password benar seharusnya lolos: %v", err)
	}
	if err := VerifyPassword("salah", hash); !errors.Is(err, ErrMismatchedPassword) {
		t.Fatalf("password salah: want ErrMismatchedPassword, got %v", err)
	}
}

func TestPasswordHashesAreSalted(t *testing.T) {
	p := Argon2Params{Memory: 8 * 1024, Iterations: 1, Parallelism: 1, SaltLen: 16, KeyLen: 32}
	h1, _ := HashPassword("sama", p)
	h2, _ := HashPassword("sama", p)
	if h1 == h2 {
		t.Fatal("dua hash password identik → salt tidak acak")
	}
}

func TestJWTIssueVerify(t *testing.T) {
	iss := NewIssuer("access-secret-min-32-chars-aaaaaaaa", "refresh-secret-min-32-chars-bbbbb", time.Minute, time.Hour)
	tok, err := iss.Issue(AccessToken, Claims{UserID: "u1", TenantID: "t1", Role: RoleKasir})
	if err != nil {
		t.Fatal(err)
	}
	claims, err := iss.Verify(AccessToken, tok)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if claims.UserID != "u1" || claims.Role != RoleKasir || claims.TenantID != "t1" {
		t.Fatalf("claims tidak sesuai: %+v", claims)
	}
}

func TestJWTExpired(t *testing.T) {
	iss := NewIssuer("access-secret-min-32-chars-aaaaaaaa", "refresh-secret-min-32-chars-bbbbb", -time.Minute, time.Hour)
	tok, _ := iss.Issue(AccessToken, Claims{UserID: "u1"})
	if _, err := iss.Verify(AccessToken, tok); err == nil {
		t.Fatal("token kedaluwarsa seharusnya ditolak")
	}
}

func TestJWTWrongSecretRejected(t *testing.T) {
	a := NewIssuer("access-secret-min-32-chars-aaaaaaaa", "refresh-secret-min-32-chars-bbbbb", time.Minute, time.Hour)
	b := NewIssuer("BEDA-access-secret-min-32-chars-x", "refresh-secret-min-32-chars-bbbbb", time.Minute, time.Hour)
	tok, _ := a.Issue(AccessToken, Claims{UserID: "u1"})
	if _, err := b.Verify(AccessToken, tok); err == nil {
		t.Fatal("token dengan secret berbeda seharusnya ditolak")
	}
}

func TestCanAccessSite(t *testing.T) {
	all := &Claims{SiteScope: nil}
	if !all.CanAccessSite("any") {
		t.Fatal("scope kosong harus boleh akses semua site")
	}
	scoped := &Claims{SiteScope: []string{"site-a"}}
	if !scoped.CanAccessSite("site-a") || scoped.CanAccessSite("site-b") {
		t.Fatal("scope terbatas harus menolak site di luar scope")
	}
}
