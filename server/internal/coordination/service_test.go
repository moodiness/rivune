package coordination

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/moodiness/rivune/server/internal/auth"
	"github.com/moodiness/rivune/server/internal/database"
)

func TestCommandShapesRejectAmbiguousPayloads(t *testing.T) {
	service := &Service{}
	position := int64(42_000)
	for _, input := range []CommandInput{
		{Command: "play", PositionMilliseconds: &position},
		{Command: "pause", Item: &PlaybackItem{}},
		{Command: "seek"},
		{Command: "stop", PositionMilliseconds: &position},
		{Command: "load", PositionMilliseconds: &position},
		{Command: "unknown"},
	} {
		if _, err := service.normalizeCommand(input); !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("expected invalid command payload %+v, got %v", input, err)
		}
	}
	if command, err := service.normalizeCommand(CommandInput{Command: " PLAY "}); err != nil || command.Command != "play" {
		t.Fatalf("play command was not normalized: command=%+v err=%v", command, err)
	}
	if command, err := service.normalizeCommand(CommandInput{Command: "seek", PositionMilliseconds: &position}); err != nil || command.PositionMilliseconds == nil || *command.PositionMilliseconds != position {
		t.Fatalf("seek command was not accepted: command=%+v err=%v", command, err)
	}
}

func TestDeviceAndRoomTimelinesAreBounded(t *testing.T) {
	item := &PlaybackItem{TitleID: "00000000-0000-4000-8000-000000000001"}
	if !validDeviceState(DeviceState{Status: "idle"}) || validDeviceState(DeviceState{Status: "idle", Item: item}) {
		t.Fatal("idle device invariant changed")
	}
	if !validDeviceState(DeviceState{Status: "playing", Item: item, PositionMilliseconds: 30_000, DurationMilliseconds: 60_000}) {
		t.Fatal("valid active device state was rejected")
	}
	if validDeviceState(DeviceState{Status: "playing", Item: item, PositionMilliseconds: 90_001, DurationMilliseconds: 60_000}) {
		t.Fatal("position beyond the drift allowance was accepted")
	}
	if validTimeline(maximumPositionMillis+1, 0) || validRoomState("buffering") {
		t.Fatal("coordination bounds were not enforced")
	}
}

func TestRoomCodesUseTheDocumentedAlphabetAndHash(t *testing.T) {
	seen := make(map[string]struct{}, 64)
	for range 64 {
		code, digest, err := newRoomCode()
		if err != nil {
			t.Fatalf("generate room code: %v", err)
		}
		if !roomCodePattern.MatchString(code) || digest != sha256.Sum256([]byte(code)) {
			t.Fatalf("invalid room code material: %q", code)
		}
		if _, exists := seen[code]; exists {
			t.Fatalf("duplicate room code generated in bounded sample: %q", code)
		}
		seen[code] = struct{}{}
	}
	if normalized := normalizeRoomCode(" abcd-efgh-jk "); normalized != "ABCDEFGHJK" {
		t.Fatalf("unexpected normalized room code %q", normalized)
	}
}

func TestRevokedTitleCannotBeReadExtendedOrRepublished(t *testing.T) {
	fixture := newCoordinationDatabaseFixture(t)
	room, err := fixture.service.CreateRoom(context.Background(), fixture.host, fixture.roomInput())
	if err != nil {
		t.Fatalf("create room: %v", err)
	}
	if _, err := fixture.service.Heartbeat(context.Background(), fixture.host, DeviceHeartbeatInput{
		State: DeviceState{Status: "playing", Item: &PlaybackItem{TitleID: fixture.titleID}},
	}); err != nil {
		t.Fatalf("publish initial heartbeat: %v", err)
	}
	commandPayload, err := json.Marshal(CommandInput{
		Command: "load", Item: &PlaybackItem{TitleID: fixture.titleID}, PositionMilliseconds: new(int64),
	})
	if err != nil {
		t.Fatalf("encode cached load command: %v", err)
	}
	if _, err := fixture.pool.Exec(context.Background(), `
		INSERT INTO playback_commands (target_session_id, sender_session_id, profile_id, command, payload, expires_at)
		VALUES ($1::uuid, $2::uuid, $3::uuid, 'load', $4::jsonb, now() + interval '2 minutes')
	`, fixture.participant.SessionID, fixture.host.SessionID, fixture.profileID, commandPayload); err != nil {
		t.Fatalf("seed cached load command: %v", err)
	}
	if _, err := fixture.pool.Exec(context.Background(), `DELETE FROM addon_profile_access WHERE addon_id = $1::uuid`, fixture.addonID); err != nil {
		t.Fatalf("revoke coordinated title: %v", err)
	}
	position := int64(0)
	assertNotFound := func(operation string, err error) {
		t.Helper()
		if !errors.Is(err, ErrNotFound) {
			t.Fatalf("%s error = %v, want %v", operation, err, ErrNotFound)
		}
	}
	_, err = fixture.service.Room(context.Background(), fixture.host, room.ID)
	assertNotFound("read revoked room", err)
	_, err = fixture.service.UpdateRoom(context.Background(), fixture.host, room.ID, UpdateRoomInput{State: "playing", ExpectedVersion: room.Version})
	assertNotFound("update revoked room", err)
	_, err = fixture.service.JoinRoom(context.Background(), fixture.participant, room.JoinCode)
	assertNotFound("join revoked room", err)
	devices, err := fixture.service.Devices(context.Background(), fixture.host)
	if err != nil || len(devices.Devices) != 0 {
		t.Fatalf("revoked cached device projection = %+v, error %v", devices, err)
	}
	commands, err := fixture.service.Commands(context.Background(), fixture.participant, 0)
	if err != nil || len(commands.Commands) != 0 {
		t.Fatalf("revoked cached command projection = %+v, error %v", commands, err)
	}
	_, err = fixture.service.Heartbeat(context.Background(), fixture.host, DeviceHeartbeatInput{
		State: DeviceState{Status: "playing", Item: &PlaybackItem{TitleID: fixture.titleID}},
	})
	assertNotFound("heartbeat revoked title", err)
	_, err = fixture.service.SendCommand(context.Background(), fixture.host, fixture.participant.SessionID, CommandInput{
		Command: "load", Item: &PlaybackItem{TitleID: fixture.titleID}, PositionMilliseconds: &position,
	})
	assertNotFound("load revoked title", err)
	var version int64
	var expiresAt time.Time
	if err := fixture.pool.QueryRow(context.Background(), `SELECT version, expires_at FROM playback_rooms WHERE id = $1::uuid`, room.ID).Scan(&version, &expiresAt); err != nil {
		t.Fatalf("read revoked room persistence: %v", err)
	}
	if version != room.Version || !expiresAt.Equal(room.ExpiresAt) {
		t.Fatalf("revoked room mutated: version=%d expiresAt=%s", version, expiresAt)
	}
}

func TestConcurrentRoomCreationNeverExceedsHostLimit(t *testing.T) {
	fixture := newCoordinationDatabaseFixture(t)
	const attempts = 12
	errorsByAttempt := make(chan error, attempts)
	var start sync.WaitGroup
	start.Add(1)
	for range attempts {
		go func() {
			start.Wait()
			_, err := fixture.service.CreateRoom(context.Background(), fixture.host, fixture.roomInput())
			errorsByAttempt <- err
		}()
	}
	start.Done()
	successes := 0
	for range attempts {
		err := <-errorsByAttempt
		switch {
		case err == nil:
			successes++
		case errors.Is(err, ErrCapacity):
		default:
			t.Fatalf("concurrent create error = %v", err)
		}
	}
	var stored int
	if err := fixture.pool.QueryRow(context.Background(), `SELECT count(*) FROM playback_rooms WHERE host_session_id = $1::uuid`, fixture.host.SessionID).Scan(&stored); err != nil {
		t.Fatalf("count concurrent rooms: %v", err)
	}
	if successes != maximumRoomsPerSession || stored != maximumRoomsPerSession {
		t.Fatalf("concurrent rooms successes=%d stored=%d, want %d", successes, stored, maximumRoomsPerSession)
	}
}

func TestStaleHostCannotReadUpdateOrResurrectRoom(t *testing.T) {
	fixture := newCoordinationDatabaseFixture(t)
	room, err := fixture.service.CreateRoom(context.Background(), fixture.host, fixture.roomInput())
	if err != nil {
		t.Fatalf("create room: %v", err)
	}
	staleAt := time.Now().UTC().Add(-presenceTTL - time.Second).Truncate(time.Microsecond)
	if _, err := fixture.pool.Exec(context.Background(), `UPDATE playback_room_members SET last_seen_at = $2 WHERE room_id = $1::uuid AND role = 'host'`, room.ID, staleAt); err != nil {
		t.Fatalf("expire host presence: %v", err)
	}
	if _, err := fixture.service.Room(context.Background(), fixture.host, room.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("stale room read error = %v, want %v", err, ErrNotFound)
	}
	if _, err := fixture.service.UpdateRoom(context.Background(), fixture.host, room.ID, UpdateRoomInput{State: "playing", ExpectedVersion: room.Version}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("stale room update error = %v, want %v", err, ErrNotFound)
	}
	var lastSeenAt, expiresAt time.Time
	var version int64
	if err := fixture.pool.QueryRow(context.Background(), `
		SELECT member.last_seen_at, room.expires_at, room.version
		FROM playback_rooms room JOIN playback_room_members member ON member.room_id = room.id AND member.role = 'host'
		WHERE room.id = $1::uuid
	`, room.ID).Scan(&lastSeenAt, &expiresAt, &version); err != nil {
		t.Fatalf("read stale room persistence: %v", err)
	}
	if !lastSeenAt.Equal(staleAt) || !expiresAt.Equal(room.ExpiresAt) || version != room.Version {
		t.Fatalf("stale room resurrected: seen=%s expires=%s version=%d", lastSeenAt, expiresAt, version)
	}
	if err := fixture.service.Cleanup(context.Background()); err != nil {
		t.Fatalf("cleanup stale room: %v", err)
	}
	var remains bool
	if err := fixture.pool.QueryRow(context.Background(), `SELECT EXISTS (SELECT 1 FROM playback_rooms WHERE id = $1::uuid)`, room.ID).Scan(&remains); err != nil {
		t.Fatalf("check stale room cleanup: %v", err)
	}
	if remains {
		t.Fatal("cleanup retained room whose host failed the shared liveness predicate")
	}
}

func TestRoomRosterUsesOnlyRoomScopedOpaqueMemberIDs(t *testing.T) {
	fixture := newCoordinationDatabaseFixture(t)
	room, err := fixture.service.CreateRoom(context.Background(), fixture.host, fixture.roomInput())
	if err != nil {
		t.Fatalf("create room: %v", err)
	}
	joined, err := fixture.service.JoinRoom(context.Background(), fixture.participant, room.JoinCode)
	if err != nil {
		t.Fatalf("join room: %v", err)
	}
	if len(joined.Members) != 2 {
		t.Fatalf("room members = %d, want 2", len(joined.Members))
	}
	seen := make(map[string]struct{}, len(joined.Members))
	for _, member := range joined.Members {
		if !uuidPattern.MatchString(member.MemberID) || member.MemberID == fixture.host.SessionID || member.MemberID == fixture.participant.SessionID || member.MemberID == fixture.profileID {
			t.Fatalf("member ID is not room-scoped and opaque: %+v", member)
		}
		if _, duplicate := seen[member.MemberID]; duplicate {
			t.Fatalf("duplicate opaque member ID %s", member.MemberID)
		}
		seen[member.MemberID] = struct{}{}
	}
	payload, err := json.Marshal(joined.Members)
	if err != nil {
		t.Fatalf("encode room roster: %v", err)
	}
	serialized := string(payload)
	if strings.Contains(serialized, "sessionId") || strings.Contains(serialized, "profileId") || strings.Contains(serialized, fixture.host.SessionID) || strings.Contains(serialized, fixture.participant.SessionID) || strings.Contains(serialized, fixture.profileID) {
		t.Fatalf("room roster leaked stable identity: %s", serialized)
	}
}

type coordinationDatabaseFixture struct {
	pool        *pgxpool.Pool
	service     *Service
	host        auth.Principal
	participant auth.Principal
	profileID   string
	addonID     string
	titleID     string
}

func newCoordinationDatabaseFixture(t *testing.T) coordinationDatabaseFixture {
	t.Helper()
	databaseURL := os.Getenv("RIVUNE_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("set RIVUNE_TEST_DATABASE_URL to run PostgreSQL coordination tests")
	}
	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		t.Fatalf("parse coordination test database URL: %v", err)
	}
	config.MaxConns = 16
	config.ConnConfig.RuntimeParams["application_name"] = "rivune-coordination-security-test"
	pool, err := pgxpool.NewWithConfig(context.Background(), config)
	if err != nil {
		t.Fatalf("open coordination test database: %v", err)
	}
	t.Cleanup(pool.Close)
	if err := database.Migrate(context.Background(), pool); err != nil {
		t.Fatalf("migrate coordination test database: %v", err)
	}
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	fixture := coordinationDatabaseFixture{pool: pool, service: NewService(pool, nil)}
	hostAccessHash := sha256.Sum256([]byte("coordination-host-access-" + suffix))
	participantAccessHash := sha256.Sum256([]byte("coordination-participant-access-" + suffix))
	var hostUserID, participantUserID, hostDeviceID, participantDeviceID string
	if err := pool.QueryRow(context.Background(), `
		WITH host_user AS (
			INSERT INTO users (username, password_hash, role) VALUES ($1, 'unused-test-hash', 'admin') RETURNING id
		), participant_user AS (
			INSERT INTO users (username, password_hash, role) VALUES ($2, 'unused-test-hash', 'admin') RETURNING id
		), profile AS (
			INSERT INTO profiles (name) VALUES ($3) RETURNING id
		), host_device AS (
			INSERT INTO devices (user_id, name, platform, approved_at) SELECT id, 'Coordination host', 'test', now() FROM host_user RETURNING id
		), participant_device AS (
			INSERT INTO devices (user_id, name, platform, approved_at) SELECT id, 'Coordination participant', 'test', now() FROM participant_user RETURNING id
		), addon AS (
			INSERT INTO profile_addons (profile_id, transport_url, manifest, manifest_id, manifest_version, position)
			SELECT id, $4, '{}'::jsonb, $5, '1.0.0', 0 FROM profile RETURNING id, profile_id
		), grant_access AS (
			INSERT INTO addon_profile_access (addon_id, profile_id, position) SELECT id, profile_id, 0 FROM addon
		), title AS (
			INSERT INTO titles (media_type, display_title, resource_id, resource_provider, source_addon_id, is_current)
			SELECT 'movie', 'Coordination security fixture', 'opaque-resource', 'addon', id, true FROM addon RETURNING id
		), host_session AS (
			INSERT INTO auth_sessions (user_id, device_id, authorization_scope, active_profile_id, profile_grant_expires_at, profile_context_hash,
				access_token_hash, access_expires_at, refresh_expires_at, last_seen_at)
			SELECT host_user.id, host_device.id, 'global_admin', profile.id, now() + interval '2 hours', decode(repeat('a1', 32), 'hex'),
			       $6, now() + interval '1 hour', now() + interval '2 hours', now()
			FROM host_user, host_device, profile RETURNING id
		), participant_session AS (
			INSERT INTO auth_sessions (user_id, device_id, authorization_scope, active_profile_id, profile_grant_expires_at, profile_context_hash,
				access_token_hash, access_expires_at, refresh_expires_at, last_seen_at)
			SELECT participant_user.id, participant_device.id, 'global_admin', profile.id, now() + interval '2 hours', decode(repeat('a2', 32), 'hex'),
			       $7, now() + interval '1 hour', now() + interval '2 hours', now()
			FROM participant_user, participant_device, profile RETURNING id
		)
		SELECT host_user.id::text, participant_user.id::text, host_device.id::text, participant_device.id::text,
		       profile.id::text, addon.id::text, title.id::text, host_session.id::text, participant_session.id::text
		FROM host_user, participant_user, host_device, participant_device, profile, addon, title, host_session, participant_session
	`, "coordination-host-"+suffix, "coordination-participant-"+suffix, "Coordination profile "+suffix,
		"https://coordination-"+suffix+".invalid/manifest.json", "org.rivune.coordination."+suffix, hostAccessHash[:], participantAccessHash[:]).Scan(
		&hostUserID, &participantUserID, &hostDeviceID, &participantDeviceID,
		&fixture.profileID, &fixture.addonID, &fixture.titleID, &fixture.host.SessionID, &fixture.participant.SessionID,
	); err != nil {
		t.Fatalf("seed coordination security fixture: %v", err)
	}
	expiresAt := time.Now().UTC().Add(time.Hour)
	fixture.host.UserID = hostUserID
	fixture.host.DeviceID = hostDeviceID
	fixture.host.Role = "admin"
	fixture.host.AuthorizationScope = auth.AuthorizationScopeGlobalAdministrator
	fixture.host.ActiveProfileID = new(fixture.profileID)
	fixture.host.ProfileGrantExpiresAt = &expiresAt
	fixture.host.ProfileContextHash = bytes.Repeat([]byte{0xa1}, sha256.Size)
	fixture.participant.UserID = participantUserID
	fixture.participant.DeviceID = participantDeviceID
	fixture.participant.Role = "admin"
	fixture.participant.AuthorizationScope = auth.AuthorizationScopeGlobalAdministrator
	fixture.participant.ActiveProfileID = new(fixture.profileID)
	fixture.participant.ProfileGrantExpiresAt = &expiresAt
	fixture.participant.ProfileContextHash = bytes.Repeat([]byte{0xa2}, sha256.Size)
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM titles WHERE id = $1::uuid`, fixture.titleID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM users WHERE id = ANY($1::uuid[])`, []string{hostUserID, participantUserID})
		_, _ = pool.Exec(context.Background(), `DELETE FROM devices WHERE id = ANY($1::uuid[])`, []string{hostDeviceID, participantDeviceID})
		_, _ = pool.Exec(context.Background(), `DELETE FROM profiles WHERE id = $1::uuid`, fixture.profileID)
	})
	return fixture
}

func (fixture coordinationDatabaseFixture) roomInput() CreateRoomInput {
	return CreateRoomInput{
		Item: PlaybackItem{TitleID: fixture.titleID}, State: "paused",
		PositionMilliseconds: 0, DurationMilliseconds: 0,
	}
}
