package auth

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
)

const (
	RoleSuperAdmin  = "super_admin"
	RoleOrgAdmin    = "org_admin"
	RoleTechnician  = "technician"
)

type Claims struct {
	TechnicianID string `json:"technician_id"`
	OrgID        string `json:"org_id,omitempty"`
	Email        string `json:"email"`
	Role         string `json:"role"`
	jwt.RegisteredClaims
}

type Manager struct {
	secret []byte
	ttl    time.Duration
}

func NewManager(secret string, ttl time.Duration) (*Manager, error) {
	if strings.TrimSpace(secret) == "" {
		return nil, errors.New("JWT_SECRET is required")
	}
	if ttl <= 0 {
		ttl = 24 * time.Hour
	}
	return &Manager{secret: []byte(secret), ttl: ttl}, nil
}

func HashPassword(password string) (string, error) {
	if len(password) < 8 {
		return "", errors.New("password must be at least 8 characters")
	}
	b, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func CheckPassword(hash, password string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil
}

func (m *Manager) Issue(technicianID, orgID, email, role string) (string, time.Time, error) {
	exp := time.Now().UTC().Add(m.ttl)
	claims := Claims{
		TechnicianID: technicianID,
		OrgID:        orgID,
		Email:        email,
		Role:         role,
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   technicianID,
			ExpiresAt: jwt.NewNumericDate(exp),
			IssuedAt:  jwt.NewNumericDate(time.Now().UTC()),
			ID:        uuid.NewString(),
		},
	}
	t := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := t.SignedString(m.secret)
	return signed, exp, err
}

func (m *Manager) Parse(token string) (*Claims, error) {
	parsed, err := jwt.ParseWithClaims(token, &Claims{}, func(t *jwt.Token) (any, error) {
		if t.Method != jwt.SigningMethodHS256 {
			return nil, fmt.Errorf("unexpected signing method")
		}
		return m.secret, nil
	})
	if err != nil {
		return nil, err
	}
	claims, ok := parsed.Claims.(*Claims)
	if !ok || !parsed.Valid {
		return nil, errors.New("invalid token")
	}
	return claims, nil
}

func RandomInviteCode() string {
	return strings.ToUpper(strings.ReplaceAll(uuid.NewString(), "-", "")[:10])
}

func BootstrapFromEnv() (email, password, name string, ok bool) {
	email = strings.TrimSpace(os.Getenv("BOOTSTRAP_ADMIN_EMAIL"))
	password = os.Getenv("BOOTSTRAP_ADMIN_PASSWORD")
	name = strings.TrimSpace(os.Getenv("BOOTSTRAP_ADMIN_NAME"))
	if name == "" {
		name = "Super Admin"
	}
	ok = email != "" && password != ""
	return
}
