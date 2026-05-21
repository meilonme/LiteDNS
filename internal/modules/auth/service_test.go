package auth

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"litedns/internal/db"
	"litedns/internal/security"
)

func setupAuthService(t *testing.T) (*Service, *sql.DB) {
	t.Helper()

	conn, err := db.Open(":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() {
		_ = conn.Close()
	})
	if err := db.Migrate(context.Background(), conn); err != nil {
		t.Fatalf("migrate db: %v", err)
	}

	return NewService(conn, 43200, true), conn
}

func seedAdmin(t *testing.T, conn *sql.DB, password string, mustChange int) int64 {
	t.Helper()

	hash, err := security.HashPassword(password)
	if err != nil {
		t.Fatalf("hash password: %v", err)
	}
	res, err := conn.Exec(`
		INSERT INTO admins(username, password_hash, must_change_password, created_at, updated_at)
		VALUES('admin', ?, ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
	`, hash, mustChange)
	if err != nil {
		t.Fatalf("insert admin: %v", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("admin id: %v", err)
	}
	return id
}

func TestApplyAdminPasswordFile_MissingFileNoop(t *testing.T) {
	svc, _ := setupAuthService(t)

	result, err := svc.ApplyAdminPasswordFile(context.Background(), filepath.Join(t.TempDir(), "admin-password"))
	if err != nil {
		t.Fatalf("apply password file: %v", err)
	}
	if result.Found || result.Changed || result.DeleteErr != nil {
		t.Fatalf("unexpected result: %#v", result)
	}
}

func TestApplyAdminPasswordFile_SamePasswordDeletesFileAndKeepsSession(t *testing.T) {
	svc, conn := setupAuthService(t)
	seedAdmin(t, conn, "samepass1", 1)

	login, err := svc.Login(context.Background(), "admin", "samepass1")
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	var oldHash string
	if err := conn.QueryRow(`SELECT password_hash FROM admins WHERE username = 'admin'`).Scan(&oldHash); err != nil {
		t.Fatalf("query old hash: %v", err)
	}

	path := filepath.Join(t.TempDir(), "admin-password")
	if err := os.WriteFile(path, []byte("samepass1\n"), 0o600); err != nil {
		t.Fatalf("write password file: %v", err)
	}

	result, err := svc.ApplyAdminPasswordFile(context.Background(), path)
	if err != nil {
		t.Fatalf("apply password file: %v", err)
	}
	if !result.Found || result.Changed || result.DeleteErr != nil {
		t.Fatalf("unexpected result: %#v", result)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected password file to be deleted, stat err=%v", err)
	}

	var newHash string
	if err := conn.QueryRow(`SELECT password_hash FROM admins WHERE username = 'admin'`).Scan(&newHash); err != nil {
		t.Fatalf("query new hash: %v", err)
	}
	if newHash != oldHash {
		t.Fatalf("expected password hash to remain unchanged")
	}
	if _, err := svc.Authenticate(context.Background(), login.Token); err != nil {
		t.Fatalf("expected existing session to remain valid: %v", err)
	}
}

func TestApplyAdminPasswordFile_ChangedPasswordRevokesSessions(t *testing.T) {
	svc, conn := setupAuthService(t)
	adminID := seedAdmin(t, conn, "oldpass1", 1)

	login, err := svc.Login(context.Background(), "admin", "oldpass1")
	if err != nil {
		t.Fatalf("login: %v", err)
	}

	path := filepath.Join(t.TempDir(), "admin-password")
	if err := os.WriteFile(path, []byte("newpass1\n"), 0o600); err != nil {
		t.Fatalf("write password file: %v", err)
	}

	result, err := svc.ApplyAdminPasswordFile(context.Background(), path)
	if err != nil {
		t.Fatalf("apply password file: %v", err)
	}
	if !result.Found || !result.Changed || result.DeleteErr != nil {
		t.Fatalf("unexpected result: %#v", result)
	}

	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected password file to be deleted, stat err=%v", err)
	}
	if _, err := svc.Login(context.Background(), "admin", "oldpass1"); !errors.Is(err, ErrInvalidCredential) {
		t.Fatalf("expected old password to fail, got %v", err)
	}
	newLogin, err := svc.Login(context.Background(), "admin", "newpass1")
	if err != nil {
		t.Fatalf("expected new password login: %v", err)
	}
	if newLogin.MustChangePassword {
		t.Fatalf("expected must_change_password=false after forced reset")
	}
	if _, err := svc.Authenticate(context.Background(), login.Token); !errors.Is(err, ErrSessionInvalid) {
		t.Fatalf("expected old session to be revoked, got %v", err)
	}

	var revokedCount int
	if err := conn.QueryRow(`SELECT COUNT(1) FROM sessions WHERE admin_id = ? AND revoked_at IS NOT NULL`, adminID).Scan(&revokedCount); err != nil {
		t.Fatalf("query revoked sessions: %v", err)
	}
	if revokedCount != 1 {
		t.Fatalf("expected one revoked session, got %d", revokedCount)
	}
}

func TestApplyAdminPasswordFile_WeakPasswordFails(t *testing.T) {
	svc, conn := setupAuthService(t)
	seedAdmin(t, conn, "oldpass1", 1)

	path := filepath.Join(t.TempDir(), "admin-password")
	if err := os.WriteFile(path, []byte("short\n"), 0o600); err != nil {
		t.Fatalf("write password file: %v", err)
	}

	result, err := svc.ApplyAdminPasswordFile(context.Background(), path)
	if !errors.Is(err, ErrWeakPassword) {
		t.Fatalf("expected weak password error, got %v", err)
	}
	if !result.Found || result.Changed {
		t.Fatalf("unexpected result: %#v", result)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("expected password file to remain after failed reset: %v", err)
	}
}

func TestApplyAdminPasswordFile_DeleteFailureIsNonFatal(t *testing.T) {
	svc, conn := setupAuthService(t)
	seedAdmin(t, conn, "samepass1", 1)
	deleteErr := errors.New("readonly mount")

	result, err := svc.applyAdminPasswordFile(
		context.Background(),
		"admin-password",
		func(string) ([]byte, error) {
			return []byte("samepass1\n"), nil
		},
		func(string) error {
			return deleteErr
		},
	)
	if err != nil {
		t.Fatalf("apply password file: %v", err)
	}
	if !result.Found || result.Changed || !errors.Is(result.DeleteErr, deleteErr) {
		t.Fatalf("unexpected result: %#v", result)
	}
}
