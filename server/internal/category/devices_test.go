package category

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"testing"
	"time"
)

func TestDeleteDeviceRequiresGlobalAdministratorAndAtomicallyAuditsCascade(t *testing.T) {
	pool := openCategoryDeleteTestPool(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var tokenHash [32]byte
	if _, err := rand.Read(tokenHash[:]); err != nil {
		t.Fatalf("generate session token hash: %v", err)
	}
	suffix := fmt.Sprintf("%x", tokenHash[:6])
	var categoryID string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM access_categories WHERE is_default`).Scan(&categoryID); err != nil {
		t.Fatalf("select default category: %v", err)
	}
	var userID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO users (username, password_hash, role)
		VALUES ($1, 'device-delete-test-hash', 'admin')
		RETURNING id::text
	`, "device-delete-"+suffix).Scan(&userID); err != nil {
		t.Fatalf("insert device deletion actor: %v", err)
	}
	var deviceID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO devices (user_id, name, platform, category_id, approved_at)
		VALUES ($1::uuid, $2, 'test', $3::uuid, now())
		RETURNING id::text
	`, userID, "Deleted device "+suffix, categoryID).Scan(&deviceID); err != nil {
		t.Fatalf("insert device deletion fixture: %v", err)
	}
	var sessionID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO auth_sessions (
			user_id, device_id, access_token_hash, access_expires_at, refresh_expires_at,
			authorization_scope, category_id
		) VALUES (
			$1::uuid, $2::uuid, $3, now() + interval '1 hour', now() + interval '2 hours',
			'category', $4::uuid
		)
		RETURNING id::text
	`, userID, deviceID, tokenHash[:], categoryID).Scan(&sessionID); err != nil {
		t.Fatalf("insert device session fixture: %v", err)
	}
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cleanupCancel()
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM access_category_audit_events WHERE entity_id = $1::uuid`, deviceID)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM auth_sessions WHERE id = $1::uuid`, sessionID)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM devices WHERE id = $1::uuid`, deviceID)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM users WHERE id = $1::uuid`, userID)
	})

	service := NewService(pool)
	if err := service.DeleteDevice(ctx, Actor{UserID: userID}, deviceID); !errors.Is(err, ErrForbidden) {
		t.Fatalf("scoped administrator deletion error = %v, want ErrForbidden", err)
	}
	var deviceStillExists bool
	if err := pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM devices WHERE id = $1::uuid)`, deviceID).Scan(&deviceStillExists); err != nil {
		t.Fatalf("check device after refused deletion: %v", err)
	}
	if !deviceStillExists {
		t.Fatal("scoped administrator implicitly deleted the device")
	}
	var auditCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM access_category_audit_events WHERE entity_id = $1::uuid`, deviceID).Scan(&auditCount); err != nil {
		t.Fatalf("count audits after refused deletion: %v", err)
	}
	if auditCount != 0 {
		t.Fatalf("scoped administrator deletion wrote %d audit events, want 0", auditCount)
	}

	if err := service.DeleteDevice(ctx, Actor{UserID: userID, GlobalAdministrator: true}, deviceID); err != nil {
		t.Fatalf("delete device: %v", err)
	}
	var deviceExists, sessionExists bool
	if err := pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM devices WHERE id = $1::uuid)`, deviceID).Scan(&deviceExists); err != nil {
		t.Fatalf("check deleted device: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM auth_sessions WHERE id = $1::uuid)`, sessionID).Scan(&sessionExists); err != nil {
		t.Fatalf("check cascaded device session: %v", err)
	}
	if deviceExists || sessionExists {
		t.Fatalf("deletion left device or session behind: device=%t session=%t", deviceExists, sessionExists)
	}
	var action, entityType, actorID, oldCategoryID string
	var newCategoryID *string
	if err := pool.QueryRow(ctx, `
		SELECT action, entity_type, actor_user_id::text, old_category_id::text, new_category_id::text
		FROM access_category_audit_events
		WHERE entity_id = $1::uuid
	`, deviceID).Scan(&action, &entityType, &actorID, &oldCategoryID, &newCategoryID); err != nil {
		t.Fatalf("read device deletion audit: %v", err)
	}
	if action != "device.deleted" || entityType != "device" || actorID != userID || oldCategoryID != categoryID || newCategoryID != nil {
		t.Fatalf("unexpected device deletion audit: action=%q entity=%q actor=%q old=%q new=%v", action, entityType, actorID, oldCategoryID, newCategoryID)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM access_category_audit_events WHERE entity_id = $1::uuid`, deviceID).Scan(&auditCount); err != nil {
		t.Fatalf("count device deletion audits: %v", err)
	}
	if auditCount != 1 {
		t.Fatalf("device deletion wrote %d audit events, want 1", auditCount)
	}
}
