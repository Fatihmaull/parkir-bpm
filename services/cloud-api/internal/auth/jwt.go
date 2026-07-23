package auth

import (
	"errors"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// Claims JWT (PRD §14). Access token 15 menit, refresh 7 hari (rotasi).
type Claims struct {
	UserID    string   `json:"uid"`
	TenantID  string   `json:"tid"`
	Role      Role     `json:"role"`
	SiteScope []string `json:"scope"` // kosong = semua site tenant
	jwt.RegisteredClaims
}

type TokenKind string

const (
	AccessToken  TokenKind = "access"
	RefreshToken TokenKind = "refresh"
)

var ErrInvalidToken = errors.New("auth: token tidak valid")

// Issuer menerbitkan & memverifikasi JWT. Secret terpisah untuk access vs refresh.
type Issuer struct {
	accessSecret  []byte
	refreshSecret []byte
	accessTTL     time.Duration
	refreshTTL    time.Duration
}

func NewIssuer(accessSecret, refreshSecret string, accessTTL, refreshTTL time.Duration) *Issuer {
	return &Issuer{
		accessSecret:  []byte(accessSecret),
		refreshSecret: []byte(refreshSecret),
		accessTTL:     accessTTL,
		refreshTTL:    refreshTTL,
	}
}

func (i *Issuer) Issue(kind TokenKind, c Claims) (string, error) {
	now := time.Now()
	ttl := i.accessTTL
	secret := i.accessSecret
	if kind == RefreshToken {
		ttl = i.refreshTTL
		secret = i.refreshSecret
	}
	c.RegisteredClaims = jwt.RegisteredClaims{
		IssuedAt:  jwt.NewNumericDate(now),
		ExpiresAt: jwt.NewNumericDate(now.Add(ttl)),
		Subject:   c.UserID,
	}
	return jwt.NewWithClaims(jwt.SigningMethodHS256, c).SignedString(secret)
}

func (i *Issuer) Verify(kind TokenKind, token string) (*Claims, error) {
	secret := i.accessSecret
	if kind == RefreshToken {
		secret = i.refreshSecret
	}
	claims := &Claims{}
	_, err := jwt.ParseWithClaims(token, claims, func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, ErrInvalidToken // tolak alg selain HMAC (cegah alg-confusion)
		}
		return secret, nil
	})
	if err != nil {
		return nil, err
	}
	return claims, nil
}
