package auth

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/moodiness/rivune/server/internal/password"
)

func TestCorrectPasswordIgnoresAndClearsLegacyAccountLock(t *testing.T) {
	databaseURL := os.Getenv("RIVUNE_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("set RIVUNE_TEST_DATABASE_URL to run the login admission regression test")
	}
	pool, err := pgxpool.New(context.Background(), databaseURL)
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	t.Cleanup(pool.Close)

	passwordHash, err := password.Hash("correct-login-password")
	if err != nil {
		t.Fatalf("hash test password: %v", err)
	}
	username := "admission_" + time.Now().UTC().Format("20060102150405.000000000")
	var userID string
	if err := pool.QueryRow(context.Background(), `
		INSERT INTO users (username, password_hash, role, failed_login_count, locked_until)
		VALUES ($1, $2, 'member', 4, NULL)
		RETURNING id::text
	`, username, passwordHash).Scan(&userID); err != nil {
		t.Fatalf("insert legacy-locked user: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), "DELETE FROM users WHERE id = $1::uuid", userID)
	})

	service := &Service{pool: pool, accessTTL: time.Minute, refreshTTL: time.Hour, timezone: "UTC"}
	if _, err := service.Login(context.Background(), LoginInput{
		Username: username, Password: "wrong-login-password", DeviceName: "Regression browser", Platform: "web",
	}); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("wrong password error = %v, want ErrInvalidCredentials", err)
	}
	var failures int
	var lockedUntil *time.Time
	if err := pool.QueryRow(context.Background(), "SELECT failed_login_count, locked_until FROM users WHERE id = $1::uuid", userID).Scan(&failures, &lockedUntil); err != nil {
		t.Fatalf("read failed login state: %v", err)
	}
	if failures != 4 || lockedUntil != nil {
		t.Fatalf("unauthenticated failure changed account-global lock state: failures=%d lockedUntil=%v", failures, lockedUntil)
	}
	if _, err := pool.Exec(context.Background(), "UPDATE users SET locked_until = now() + interval '15 minutes' WHERE id = $1::uuid", userID); err != nil {
		t.Fatalf("set legacy account lock: %v", err)
	}

	if _, err := service.Login(context.Background(), LoginInput{
		Username: username, Password: "correct-login-password", DeviceName: "Regression browser", Platform: "web",
	}); err != nil {
		t.Fatalf("correct password was denied by legacy account lock: %v", err)
	}
	if err := pool.QueryRow(context.Background(), "SELECT failed_login_count, locked_until FROM users WHERE id = $1::uuid", userID).Scan(&failures, &lockedUntil); err != nil {
		t.Fatalf("read cleared login state: %v", err)
	}
	if failures != 0 || lockedUntil != nil {
		t.Fatalf("legacy login lock not cleared: failures=%d lockedUntil=%v", failures, lockedUntil)
	}
}
