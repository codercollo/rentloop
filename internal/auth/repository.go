package auth

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/codercollo/rentloop/internal/models"
)

// Repository handles admin_users SQL.
type Repository struct {
	db *pgxpool.Pool
}

// NewRepository returns an auth repository.
func NewRepository(db *pgxpool.Pool) *Repository {
	return &Repository{db: db}
}

// CreateAdmin inserts a new unactivated admin user.
func (r *Repository) CreateAdmin(ctx context.Context, email, hashedPassword, token string, expires time.Time) (*models.AdminUser, error) {
	var u models.AdminUser
	err := r.db.QueryRow(ctx, `
		INSERT INTO admin_users (email, password, activation_token, token_expires_at)
		VALUES ($1,$2,$3,$4)
		RETURNING id, email, password, activated, activation_token, token_expires_at, created_at
	`, email, hashedPassword, token, expires).Scan(
		&u.ID, &u.Email, &u.Password,
		&u.Activated, &u.ActivationToken, &u.TokenExpiresAt, &u.CreatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("create admin: %w", err)
	}
	return &u, nil
}

// GetAdminByEmail fetches an admin by email.
func (r *Repository) GetAdminByEmail(ctx context.Context, email string) (*models.AdminUser, error) {
	var u models.AdminUser
	err := r.db.QueryRow(ctx, `
		SELECT id, email, password, activated, activation_token, token_expires_at, created_at
		FROM   admin_users WHERE email = $1 LIMIT 1
	`, email).Scan(
		&u.ID, &u.Email, &u.Password,
		&u.Activated, &u.ActivationToken, &u.TokenExpiresAt, &u.CreatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, models.ErrNotFound
		}
		return nil, fmt.Errorf("get admin by email: %w", err)
	}
	return &u, nil
}

// ActivateAdmin marks an account as activated by token.
func (r *Repository) ActivateAdmin(ctx context.Context, token string) error {
	tag, err := r.db.Exec(ctx, `
		UPDATE admin_users
		SET    activated = TRUE
		WHERE  activation_token = $1
		  AND  token_expires_at > NOW()
		  AND  activated = FALSE
	`, token)
	if err != nil {
		return fmt.Errorf("activate admin: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("token invalid or expired")
	}
	return nil
}

// GetAdminByID fetches an admin by ID (used by JWT middleware).
func (r *Repository) GetAdminByID(ctx context.Context, id string) (*models.AdminUser, error) {
	var u models.AdminUser
	err := r.db.QueryRow(ctx, `
		SELECT id, email, password, activated, activation_token, token_expires_at, created_at
		FROM   admin_users WHERE id = $1 LIMIT 1
	`, id).Scan(
		&u.ID, &u.Email, &u.Password,
		&u.Activated, &u.ActivationToken, &u.TokenExpiresAt, &u.CreatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, models.ErrNotFound
		}
		return nil, fmt.Errorf("get admin by id: %w", err)
	}
	return &u, nil
}
