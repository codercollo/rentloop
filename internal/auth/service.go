// Package auth handles admin authentication for the RentLoop admin panel.
package auth

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/bcrypt"

	"github.com/codercollo/rentloop/internal/models"
)

// AuthRepository is the persistence interface.
type AuthRepository interface {
	CreateAdmin(ctx context.Context, email, hashedPassword, token string, expires time.Time) (*models.AdminUser, error)
	GetAdminByEmail(ctx context.Context, email string) (*models.AdminUser, error)
	ActivateAdmin(ctx context.Context, token string) error
	GetAdminByID(ctx context.Context, id string) (*models.AdminUser, error)
}

// Service handles bcrypt hashing, JWT signing, and activation tokens.
type Service struct {
	repo             AuthRepository
	jwtSecret        []byte
	activationSecret []byte
	tokenExpiry      time.Duration
}

// NewService wires all dependencies.
func NewService(repo AuthRepository, jwtSecret, activationSecret string) *Service {
	return &Service{
		repo:             repo,
		jwtSecret:        []byte(jwtSecret),
		activationSecret: []byte(activationSecret),
		tokenExpiry:      24 * time.Hour,
	}
}

// Claims is the JWT payload.
type Claims struct {
	AdminID string `json:"admin_id"`
	Email   string `json:"email"`
	jwt.RegisteredClaims
}

// Register creates a new admin account and returns the activation token.
func (s *Service) Register(ctx context.Context, email, password string) (string, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return "", fmt.Errorf("hash password: %w", err)
	}

	token := s.generateActivationToken(email)
	expires := time.Now().Add(48 * time.Hour)

	if _, err := s.repo.CreateAdmin(ctx, email, string(hash), token, expires); err != nil {
		return "", fmt.Errorf("create admin: %w", err)
	}

	return token, nil
}

// Login verifies credentials and returns a signed JWT.
func (s *Service) Login(ctx context.Context, email, password string) (string, error) {
	admin, err := s.repo.GetAdminByEmail(ctx, email)
	if err != nil {
		if errors.Is(err, models.ErrNotFound) {
			return "", models.ErrUnauthorised
		}
		return "", fmt.Errorf("get admin: %w", err)
	}

	if !admin.Activated {
		return "", fmt.Errorf("account not activated — check your email")
	}

	if err := bcrypt.CompareHashAndPassword([]byte(admin.Password), []byte(password)); err != nil {
		return "", models.ErrUnauthorised
	}

	return s.signJWT(admin.ID, admin.Email)
}

// Activate marks an admin account as active using the token from the email link.
func (s *Service) Activate(ctx context.Context, token string) error {
	if err := s.repo.ActivateAdmin(ctx, token); err != nil {
		return fmt.Errorf("activate: %w", err)
	}
	return nil
}

// VerifyJWT parses and validates a JWT, returning the claims.
func (s *Service) VerifyJWT(tokenStr string) (*Claims, error) {
	token, err := jwt.ParseWithClaims(tokenStr, &Claims{}, func(t *jwt.Token) (interface{}, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
		}
		return s.jwtSecret, nil
	})
	if err != nil {
		return nil, fmt.Errorf("parse jwt: %w", err)
	}

	claims, ok := token.Claims.(*Claims)
	if !ok || !token.Valid {
		return nil, models.ErrUnauthorised
	}

	return claims, nil
}

func (s *Service) signJWT(adminID, email string) (string, error) {
	claims := Claims{
		AdminID: adminID,
		Email:   email,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(s.tokenExpiry)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString(s.jwtSecret)
}

// generateActivationToken creates an HMAC-SHA256 token from email + timestamp.
func (s *Service) generateActivationToken(email string) string {
	h := hmac.New(sha256.New, s.activationSecret)
	h.Write([]byte(email + fmt.Sprintf("%d", time.Now().UnixNano())))
	return hex.EncodeToString(h.Sum(nil))
}
