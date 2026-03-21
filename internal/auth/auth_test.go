package auth_test

import (
	"context"
	"testing"
	"time"

	"github.com/codercollo/rentloop/internal/auth"
	"github.com/codercollo/rentloop/internal/models"
)

type mockRepo struct {
	admin *models.AdminUser
}

func (m *mockRepo) CreateAdmin(_ context.Context, email, pw, token string, exp time.Time) (*models.AdminUser, error) {
	m.admin = &models.AdminUser{
		ID: "admin-1", Email: email, Password: pw,
		Activated: false, ActivationToken: token, TokenExpiresAt: &exp,
	}
	return m.admin, nil
}
func (m *mockRepo) GetAdminByEmail(_ context.Context, _ string) (*models.AdminUser, error) {
	if m.admin == nil {
		return nil, models.ErrNotFound
	}
	return m.admin, nil
}
func (m *mockRepo) ActivateAdmin(_ context.Context, _ string) error {
	if m.admin != nil {
		m.admin.Activated = true
	}
	return nil
}
func (m *mockRepo) GetAdminByID(_ context.Context, _ string) (*models.AdminUser, error) {
	if m.admin == nil {
		return nil, models.ErrNotFound
	}
	return m.admin, nil
}

const (
	testJWTSecret        = "testsecrettestsecrettestsecretXX"
	testActivationSecret = "activationsecretactivationsecre1"
)

func newSvc() *auth.Service {
	return auth.NewService(&mockRepo{}, testJWTSecret, testActivationSecret)
}

func TestRegister_CreatesToken(t *testing.T) {
	repo := &mockRepo{}
	svc := auth.NewService(repo, testJWTSecret, testActivationSecret)

	token, err := svc.Register(context.Background(), "test@rentloop.co.ke", "password123")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if token == "" {
		t.Error("expected activation token, got empty string")
	}
}

func TestLogin_WrongPassword_ReturnsUnauthorised(t *testing.T) {
	repo := &mockRepo{}
	svc := auth.NewService(repo, testJWTSecret, testActivationSecret)

	// Register first
	_, _ = svc.Register(context.Background(), "test@rentloop.co.ke", "correctpassword")
	repo.admin.Activated = true

	_, err := svc.Login(context.Background(), "test@rentloop.co.ke", "wrongpassword")
	if err == nil {
		t.Error("expected error for wrong password")
	}
}

func TestLogin_NotActivated_ReturnsError(t *testing.T) {
	repo := &mockRepo{}
	svc := auth.NewService(repo, testJWTSecret, testActivationSecret)

	_, _ = svc.Register(context.Background(), "test@rentloop.co.ke", "password123")
	// repo.admin.Activated is false by default

	_, err := svc.Login(context.Background(), "test@rentloop.co.ke", "password123")
	if err == nil {
		t.Error("expected error for unactivated account")
	}
}

func TestVerifyJWT_ValidToken_ReturnsClaims(t *testing.T) {
	repo := &mockRepo{}
	svc := auth.NewService(repo, testJWTSecret, testActivationSecret)

	_, _ = svc.Register(context.Background(), "admin@test.com", "password123")
	repo.admin.Activated = true

	jwtToken, err := svc.Login(context.Background(), "admin@test.com", "password123")
	if err != nil {
		t.Fatalf("login failed: %v", err)
	}

	claims, err := svc.VerifyJWT(jwtToken)
	if err != nil {
		t.Fatalf("verify failed: %v", err)
	}
	if claims.Email != "admin@test.com" {
		t.Errorf("expected admin@test.com, got %s", claims.Email)
	}
}

func TestVerifyJWT_TamperedToken_ReturnsError(t *testing.T) {
	svc := newSvc()
	_, err := svc.VerifyJWT("eyJhbGciOiJIUzI1NiJ9.tampered.signature")
	if err == nil {
		t.Error("expected error for tampered token")
	}
}
