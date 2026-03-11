package auth

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"litedns/internal/security"
)

var (
	ErrInvalidCredential = errors.New("invalid credential")
	ErrSessionInvalid    = errors.New("session invalid")
	ErrWeakPassword      = errors.New("password must be at least 8 characters")
)

type AdminInfo struct {
	ID                 int64      `json:"id"`
	Username           string     `json:"username"`
	MustChangePassword bool       `json:"must_change_password"`
	LastLoginAt        *time.Time `json:"last_login_at"`
}

type LoginResult struct {
	Token              string    `json:"token"`
	ExpiresAt          time.Time `json:"expires_at"`
	MustChangePassword bool      `json:"must_change_password"`
}

type Service struct {
	db                *sql.DB
	sessionTTL        time.Duration
	forceChangeOnInit bool
}

func NewService(db *sql.DB, sessionTTLSeconds int, forceChangeOnInit bool) *Service {
	if sessionTTLSeconds <= 0 {
		sessionTTLSeconds = 43200
	}
	return &Service{
		db:                db,
		sessionTTL:        time.Duration(sessionTTLSeconds) * time.Second,
		forceChangeOnInit: forceChangeOnInit,
	}
}

func (s *Service) EnsureBootstrapAdmin(ctx context.Context) (string, string, bool, error) {
	var count int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(1) FROM admins`).Scan(&count); err != nil {
		return "", "", false, fmt.Errorf("count admins: %w", err)
	}
	if count > 0 {
		return "", "", false, nil
	}

	now := time.Now().UTC()
	password, err := security.GenerateRandomSecret(18)
	if err != nil {
		return "", "", false, fmt.Errorf("generate admin password: %w", err)
	}
	hash, err := security.HashPassword(password)
	if err != nil {
		return "", "", false, fmt.Errorf("hash admin password: %w", err)
	}

	mustChange := 0
	if s.forceChangeOnInit {
		mustChange = 1
	}

	_, err = s.db.ExecContext(ctx, `
		INSERT INTO admins(username, password_hash, must_change_password, created_at, updated_at)
		VALUES(?, ?, ?, ?, ?)
	`, "admin", hash, mustChange, now, now)
	if err != nil {
		return "", "", false, fmt.Errorf("insert bootstrap admin: %w", err)
	}

	return "admin", password, true, nil
}

func (s *Service) Login(ctx context.Context, username, password string) (LoginResult, error) {
	var (
		id           int64
		passwordHash string
		mustChange   int
	)
	if err := s.db.QueryRowContext(ctx, `
		SELECT id, password_hash, must_change_password
		FROM admins
		WHERE username = ?
	`, username).Scan(&id, &passwordHash, &mustChange); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return LoginResult{}, ErrInvalidCredential
		}
		return LoginResult{}, fmt.Errorf("query admin for login: %w", err)
	}

	if !security.VerifyPassword(passwordHash, password) {
		return LoginResult{}, ErrInvalidCredential
	}

	now := time.Now().UTC()
	expiresAt := now.Add(s.sessionTTL)
	token, err := security.GenerateRandomSecret(32)
	if err != nil {
		return LoginResult{}, fmt.Errorf("generate session token: %w", err)
	}
	tokenHash := security.HashToken(token)

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return LoginResult{}, fmt.Errorf("begin login tx: %w", err)
	}

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO sessions(admin_id, token_hash, expires_at, created_at)
		VALUES(?, ?, ?, ?)
	`, id, tokenHash, expiresAt, now); err != nil {
		_ = tx.Rollback()
		return LoginResult{}, fmt.Errorf("insert session: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE admins SET last_login_at = ?, updated_at = ? WHERE id = ?
	`, now, now, id); err != nil {
		_ = tx.Rollback()
		return LoginResult{}, fmt.Errorf("update admin login time: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return LoginResult{}, fmt.Errorf("commit login tx: %w", err)
	}

	return LoginResult{
		Token:              token,
		ExpiresAt:          expiresAt,
		MustChangePassword: mustChange == 1,
	}, nil
}

func (s *Service) Logout(ctx context.Context, token string) error {
	tokenHash := security.HashToken(token)
	if _, err := s.db.ExecContext(ctx, `
		UPDATE sessions SET revoked_at = ? WHERE token_hash = ? AND revoked_at IS NULL
	`, time.Now().UTC(), tokenHash); err != nil {
		return fmt.Errorf("revoke session: %w", err)
	}
	return nil
}

func (s *Service) Authenticate(ctx context.Context, token string) (AdminInfo, error) {
	tokenHash := security.HashToken(token)
	var (
		info       AdminInfo
		mustChange int
	)
	now := time.Now().UTC()
	if err := s.db.QueryRowContext(ctx, `
		SELECT a.id, a.username, a.must_change_password, a.last_login_at
		FROM sessions s
		JOIN admins a ON a.id = s.admin_id
		WHERE s.token_hash = ? AND s.revoked_at IS NULL AND s.expires_at > ?
	`, tokenHash, now).Scan(&info.ID, &info.Username, &mustChange, &info.LastLoginAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return AdminInfo{}, ErrSessionInvalid
		}
		return AdminInfo{}, fmt.Errorf("authenticate session: %w", err)
	}
	info.MustChangePassword = mustChange == 1
	return info, nil
}

func (s *Service) ChangePassword(ctx context.Context, adminID int64, oldPassword, newPassword string) error {
	if len(newPassword) < 8 {
		return ErrWeakPassword
	}

	var oldHash string
	if err := s.db.QueryRowContext(ctx, `SELECT password_hash FROM admins WHERE id = ?`, adminID).Scan(&oldHash); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrInvalidCredential
		}
		return fmt.Errorf("query old password hash: %w", err)
	}
	if !security.VerifyPassword(oldHash, oldPassword) {
		return ErrInvalidCredential
	}

	newHash, err := security.HashPassword(newPassword)
	if err != nil {
		return fmt.Errorf("hash new password: %w", err)
	}

	now := time.Now().UTC()
	if _, err := s.db.ExecContext(ctx, `
		UPDATE admins
		SET password_hash = ?, must_change_password = 0, updated_at = ?
		WHERE id = ?
	`, newHash, now, adminID); err != nil {
		return fmt.Errorf("update password: %w", err)
	}
	return nil
}

func (s *Service) Me(ctx context.Context, adminID int64) (AdminInfo, error) {
	var (
		info       AdminInfo
		mustChange int
	)
	if err := s.db.QueryRowContext(ctx, `
		SELECT id, username, must_change_password, last_login_at
		FROM admins
		WHERE id = ?
	`, adminID).Scan(&info.ID, &info.Username, &mustChange, &info.LastLoginAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return AdminInfo{}, ErrSessionInvalid
		}
		return AdminInfo{}, fmt.Errorf("query me: %w", err)
	}
	info.MustChangePassword = mustChange == 1
	return info, nil
}
