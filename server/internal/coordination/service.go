package coordination

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/moodiness/rivune/server/internal/auth"
	"github.com/moodiness/rivune/server/internal/watchstate"
)

const (
	presenceTTL            = 45 * time.Second
	commandTTL             = 2 * time.Minute
	roomTTL                = 8 * time.Hour
	maximumPendingCommands = 100
	maximumRoomsPerSession = 4
	maximumRoomMembers     = 20
	maximumCapabilities    = 16
	maximumPositionMillis  = int64(7 * 24 * time.Hour / time.Millisecond)
)

const liveRoomHostPredicateSQL = `EXISTS (
	SELECT 1 FROM playback_room_members live_host
	WHERE live_host.room_id = room.id AND live_host.role = 'host'
	  AND live_host.last_seen_at > clock_timestamp() - $1::interval
)`

var (
	capabilityPattern = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,31}$`)
	roomCodePattern   = regexp.MustCompile(`^[23456789ABCDEFGHJKMNPQRSTUVWXYZ]{10}$`)
)

type Catalog interface {
	GetCatalogTitle(context.Context, auth.Principal, string) (watchstate.CatalogTitle, error)
}

type Service struct {
	pool    *pgxpool.Pool
	catalog Catalog
	now     func() time.Time
}

func NewService(pool *pgxpool.Pool, catalog Catalog) *Service {
	return &Service{pool: pool, catalog: catalog, now: func() time.Time { return time.Now().UTC() }}
}

func (s *Service) Heartbeat(ctx context.Context, principal auth.Principal, input DeviceHeartbeatInput) (Device, error) {
	input, err := s.normalizeHeartbeat(input)
	if err != nil {
		return Device{}, err
	}
	tx, profileID, err := s.beginAuthorizedProfileTx(ctx, principal)
	if err != nil {
		return Device{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if input.State.Item != nil {
		item, itemErr := s.canonicalItemTx(ctx, tx, profileID, *input.State.Item)
		if itemErr != nil {
			return Device{}, itemErr
		}
		input.State.Item = &item
	}
	input.State.UpdatedAt = s.now()
	stateJSON, err := json.Marshal(input.State)
	if err != nil {
		return Device{}, fmt.Errorf("encode playback device state: %w", err)
	}
	now := s.now()
	if _, err := tx.Exec(ctx, `
		INSERT INTO playback_device_presence (auth_session_id, profile_id, capabilities, playback_state, last_seen_at, updated_at)
		VALUES ($1::uuid, $2::uuid, $3::text[], $4::jsonb, $5, $5)
		ON CONFLICT (auth_session_id) DO UPDATE
		SET profile_id = EXCLUDED.profile_id,
		    capabilities = EXCLUDED.capabilities,
		    playback_state = EXCLUDED.playback_state,
		    revision = playback_device_presence.revision + 1,
		    last_seen_at = EXCLUDED.last_seen_at,
		    updated_at = EXCLUDED.updated_at
	`, principal.SessionID, profileID, input.Capabilities, stateJSON, now); err != nil {
		return Device{}, fmt.Errorf("store playback device presence: %w", err)
	}
	device, err := scanDevice(tx.QueryRow(ctx, `
		SELECT session.id::text, device.id::text, device.name, device.platform,
		       presence.capabilities, presence.playback_state, presence.revision, true, presence.last_seen_at
		FROM playback_device_presence presence
		JOIN auth_sessions session ON session.id = presence.auth_session_id
		JOIN devices device ON device.id = session.device_id
		WHERE presence.auth_session_id = $1::uuid
	`, principal.SessionID))
	if err != nil {
		return Device{}, fmt.Errorf("read playback device presence: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return Device{}, fmt.Errorf("commit playback device presence: %w", err)
	}
	return device, nil
}

func (s *Service) Devices(ctx context.Context, principal auth.Principal) (DeviceList, error) {
	tx, profileID, err := s.beginAuthorizedProfileTx(ctx, principal)
	if err != nil {
		return DeviceList{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	rows, err := tx.Query(ctx, `
		SELECT session.id::text, device.id::text, device.name, device.platform,
		       presence.capabilities, presence.playback_state, presence.revision,
		       session.id = $1::uuid, presence.last_seen_at
		FROM playback_device_presence presence
		JOIN auth_sessions session ON session.id = presence.auth_session_id
		JOIN devices device ON device.id = session.device_id
		WHERE presence.profile_id = $2::uuid
		  AND session.user_id = $3::uuid
		  AND session.active_profile_id = $2::uuid
		  AND session.revoked_at IS NULL
		  AND session.refresh_expires_at > now()
		  AND session.profile_grant_expires_at > now()
		  AND presence.last_seen_at > now() - $4::interval
		ORDER BY session.id = $1::uuid DESC, presence.last_seen_at DESC, session.id
	`, principal.SessionID, profileID, principal.UserID, intervalLiteral(presenceTTL))
	if err != nil {
		return DeviceList{}, fmt.Errorf("query playback devices: %w", err)
	}
	defer rows.Close()
	devices := make([]Device, 0)
	for rows.Next() {
		device, scanErr := scanDevice(rows)
		if scanErr != nil {
			return DeviceList{}, fmt.Errorf("scan playback device: %w", scanErr)
		}
		devices = append(devices, device)
	}
	if err := rows.Err(); err != nil {
		return DeviceList{}, fmt.Errorf("iterate playback devices: %w", err)
	}
	rows.Close()
	visibleDevices := devices[:0]
	for index := range devices {
		if devices[index].State.Item != nil {
			item, itemErr := s.canonicalItemTx(ctx, tx, profileID, *devices[index].State.Item)
			if errors.Is(itemErr, ErrNotFound) {
				continue
			}
			if itemErr != nil {
				return DeviceList{}, itemErr
			}
			devices[index].State.Item = &item
		}
		visibleDevices = append(visibleDevices, devices[index])
	}
	devices = visibleDevices
	if err := tx.Commit(ctx); err != nil {
		return DeviceList{}, fmt.Errorf("commit playback devices read: %w", err)
	}
	return DeviceList{Devices: devices}, nil
}

func (s *Service) SendCommand(ctx context.Context, principal auth.Principal, targetSessionID string, input CommandInput) (Command, error) {
	targetSessionID = strings.ToLower(strings.TrimSpace(targetSessionID))
	input, err := s.normalizeCommand(input)
	if err != nil || !uuidPattern.MatchString(targetSessionID) {
		return Command{}, ErrInvalidInput
	}
	tx, profileID, err := s.beginAuthorizedProfileTx(ctx, principal)
	if err != nil {
		return Command{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if input.Item != nil {
		item, itemErr := s.canonicalItemTx(ctx, tx, profileID, *input.Item)
		if itemErr != nil {
			return Command{}, itemErr
		}
		input.Item = &item
	}
	payload, err := json.Marshal(input)
	if err != nil {
		return Command{}, fmt.Errorf("encode playback command: %w", err)
	}
	existing, found, err := queryCommandByOperation(ctx, tx, principal.SessionID, input.OperationID, true)
	if err != nil {
		return Command{}, err
	}
	if found {
		if existing.targetSessionID != targetSessionID || !sameCommandInput(existing.input, input) {
			return Command{}, ErrConflict
		}
		if err := tx.Commit(ctx); err != nil {
			return Command{}, fmt.Errorf("commit playback command replay: %w", err)
		}
		return existing.command, nil
	}
	var senderName string
	var targetRevision int64
	if err := tx.QueryRow(ctx, `
		SELECT sender_device.name, presence.revision
		FROM auth_sessions session
		JOIN playback_device_presence presence ON presence.auth_session_id = session.id
		JOIN auth_sessions sender ON sender.id = $2::uuid
		JOIN devices sender_device ON sender_device.id = sender.device_id
		WHERE session.id = $1::uuid AND session.id <> $2::uuid
		  AND session.user_id = $3::uuid AND session.active_profile_id = $4::uuid
		  AND session.revoked_at IS NULL AND session.refresh_expires_at > now()
		  AND session.profile_grant_expires_at > now() AND presence.profile_id = $4::uuid
		  AND presence.last_seen_at > now() - $5::interval
		  AND 'remote-control' = ANY(presence.capabilities)
		FOR UPDATE OF presence
	`, targetSessionID, principal.SessionID, principal.UserID, profileID, intervalLiteral(presenceTTL)).Scan(&senderName, &targetRevision); errors.Is(err, pgx.ErrNoRows) {
		return Command{}, ErrNotFound
	} else if err != nil {
		return Command{}, fmt.Errorf("authorize playback command target: %w", err)
	}
	if input.TargetRevision != nil && *input.TargetRevision != targetRevision {
		return Command{}, ErrConflict
	}
	var pending int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM playback_commands WHERE target_session_id=$1::uuid AND result_status IS NULL AND expires_at>now()`, targetSessionID).Scan(&pending); err != nil {
		return Command{}, fmt.Errorf("count pending playback commands: %w", err)
	}
	if pending >= maximumPendingCommands {
		return Command{}, ErrCapacity
	}
	now := s.now()
	command := Command{
		OperationID: input.OperationID, Command: input.Command, Mode: input.Mode, TargetRevision: input.TargetRevision,
		Item: input.Item, PositionMilliseconds: input.PositionMilliseconds, SenderDeviceName: senderName,
		Status: "pending", CreatedAt: now, ExpiresAt: now.Add(commandTTL),
	}
	if err := tx.QueryRow(ctx, `
		INSERT INTO playback_commands (operation_id,target_session_id,sender_session_id,profile_id,command,payload,target_revision,created_at,expires_at)
		VALUES ($1::uuid,$2::uuid,$3::uuid,$4::uuid,$5,$6::jsonb,$7,$8,$9)
		ON CONFLICT (sender_session_id,operation_id) DO NOTHING
		RETURNING created_at,expires_at
	`, input.OperationID, targetSessionID, principal.SessionID, profileID, input.Command, payload, input.TargetRevision, command.CreatedAt, command.ExpiresAt).Scan(&command.CreatedAt, &command.ExpiresAt); errors.Is(err, pgx.ErrNoRows) {
		replayed, replayFound, replayErr := queryCommandByOperation(ctx, tx, principal.SessionID, input.OperationID, true)
		if replayErr != nil {
			return Command{}, replayErr
		}
		if !replayFound || replayed.targetSessionID != targetSessionID || !sameCommandInput(replayed.input, input) {
			return Command{}, ErrConflict
		}
		command = replayed.command
	} else if err != nil {
		return Command{}, fmt.Errorf("store playback command: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return Command{}, fmt.Errorf("commit playback command: %w", err)
	}
	return command, nil
}

func (s *Service) Commands(ctx context.Context, principal auth.Principal, afterOperationID string) (CommandList, error) {
	afterOperationID = strings.ToLower(strings.TrimSpace(afterOperationID))
	if afterOperationID != "" && !uuidPattern.MatchString(afterOperationID) {
		return CommandList{}, ErrInvalidInput
	}
	tx, profileID, err := s.beginAuthorizedProfileTx(ctx, principal)
	if err != nil {
		return CommandList{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	rows, err := tx.Query(ctx, `
		SELECT command.operation_id::text,command.command,command.payload,sender_device.name,
		       command.result_status,command.result_code,command.created_at,command.expires_at,command.target_session_id::text
		FROM playback_commands command
		JOIN auth_sessions sender ON sender.id=command.sender_session_id
		JOIN devices sender_device ON sender_device.id=sender.device_id
		WHERE command.target_session_id=$1::uuid AND command.profile_id=$2::uuid
		  AND command.result_status IS NULL AND command.expires_at>now()
		  AND ($3='' OR EXISTS (
		      SELECT 1 FROM playback_commands cursor
		      WHERE cursor.target_session_id=$1::uuid AND cursor.operation_id=$3::uuid
		        AND (command.created_at,command.operation_id)>(cursor.created_at,cursor.operation_id)))
		ORDER BY command.created_at,command.operation_id LIMIT 100
	`, principal.SessionID, profileID, afterOperationID)
	if err != nil {
		return CommandList{}, fmt.Errorf("query playback commands: %w", err)
	}
	defer rows.Close()
	storedCommands := make([]storedCommand, 0)
	for rows.Next() {
		stored, scanErr := scanStoredCommand(rows)
		if scanErr != nil {
			return CommandList{}, scanErr
		}
		storedCommands = append(storedCommands, stored)
	}
	if err := rows.Err(); err != nil {
		return CommandList{}, fmt.Errorf("iterate playback commands: %w", err)
	}
	rows.Close()
	commands := make([]Command, 0, len(storedCommands))
	for _, stored := range storedCommands {
		if stored.command.Item != nil {
			item, itemErr := s.canonicalItemTx(ctx, tx, profileID, *stored.command.Item)
			if errors.Is(itemErr, ErrNotFound) {
				continue
			}
			if itemErr != nil {
				return CommandList{}, itemErr
			}
			stored.command.Item = &item
		}
		commands = append(commands, stored.command)
	}
	if err := tx.Commit(ctx); err != nil {
		return CommandList{}, fmt.Errorf("commit playback commands read: %w", err)
	}
	return CommandList{Commands: commands}, nil
}

func (s *Service) CompleteCommand(ctx context.Context, principal auth.Principal, operationID string, input CommandResultInput) (Command, error) {
	operationID = strings.ToLower(strings.TrimSpace(operationID))
	input.Status = strings.ToLower(strings.TrimSpace(input.Status))
	input.Code = strings.ToLower(strings.TrimSpace(input.Code))
	if !uuidPattern.MatchString(operationID) || !validCommandResult(input) {
		return Command{}, ErrInvalidInput
	}
	tx, profileID, err := s.beginAuthorizedProfileTx(ctx, principal)
	if err != nil {
		return Command{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var currentStatus, currentCode *string
	var expiresAt time.Time
	if err := tx.QueryRow(ctx, `
		SELECT result_status,result_code,expires_at FROM playback_commands
		WHERE operation_id=$1::uuid AND target_session_id=$2::uuid AND profile_id=$3::uuid FOR UPDATE
	`, operationID, principal.SessionID, profileID).Scan(&currentStatus, &currentCode, &expiresAt); errors.Is(err, pgx.ErrNoRows) {
		return Command{}, ErrNotFound
	} else if err != nil {
		return Command{}, fmt.Errorf("lock playback command result: %w", err)
	}
	status, code := input.Status, input.Code
	if !expiresAt.After(s.now()) {
		status, code = "expired", "expired"
	}
	if currentStatus != nil {
		if *currentStatus != status || currentCode == nil || *currentCode != code {
			return Command{}, ErrConflict
		}
	} else if _, err := tx.Exec(ctx, `UPDATE playback_commands SET result_status=$4,result_code=$5,completed_at=$6 WHERE operation_id=$1::uuid AND target_session_id=$2::uuid AND profile_id=$3::uuid`, operationID, principal.SessionID, profileID, status, code, s.now()); err != nil {
		return Command{}, fmt.Errorf("store playback command result: %w", err)
	}
	stored, found, err := queryCommandByOperation(ctx, tx, principal.SessionID, operationID, false)
	if err != nil {
		return Command{}, err
	}
	if !found {
		return Command{}, ErrNotFound
	}
	if err := tx.Commit(ctx); err != nil {
		return Command{}, fmt.Errorf("commit playback command result: %w", err)
	}
	return stored.command, nil
}

func (s *Service) OutgoingCommand(ctx context.Context, principal auth.Principal, operationID string) (Command, error) {
	operationID = strings.ToLower(strings.TrimSpace(operationID))
	if !uuidPattern.MatchString(operationID) {
		return Command{}, ErrInvalidInput
	}
	tx, _, err := s.beginAuthorizedProfileTx(ctx, principal)
	if err != nil {
		return Command{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	stored, found, err := queryCommandByOperation(ctx, tx, principal.SessionID, operationID, true)
	if err != nil {
		return Command{}, err
	}
	if !found {
		return Command{}, ErrNotFound
	}
	if err := tx.Commit(ctx); err != nil {
		return Command{}, fmt.Errorf("commit outgoing playback command read: %w", err)
	}
	return stored.command, nil
}

type storedCommand struct {
	command         Command
	input           CommandInput
	targetSessionID string
}

func scanStoredCommand(row pgx.Row) (storedCommand, error) {
	var stored storedCommand
	var payload []byte
	var status, code *string
	if err := row.Scan(&stored.command.OperationID, &stored.command.Command, &payload, &stored.command.SenderDeviceName,
		&status, &code, &stored.command.CreatedAt, &stored.command.ExpiresAt, &stored.targetSessionID); err != nil {
		return storedCommand{}, fmt.Errorf("scan playback command: %w", err)
	}
	if err := json.Unmarshal(payload, &stored.input); err != nil || stored.input.Command != stored.command.Command || stored.input.OperationID != stored.command.OperationID {
		return storedCommand{}, fmt.Errorf("decode playback command")
	}
	stored.command.Mode = stored.input.Mode
	stored.command.TargetRevision = stored.input.TargetRevision
	stored.command.Item = stored.input.Item
	stored.command.PositionMilliseconds = stored.input.PositionMilliseconds
	stored.command.Status = "pending"
	if status != nil {
		stored.command.Status = *status
	}
	if code != nil {
		stored.command.ResultCode = *code
	}
	if stored.command.Status == "pending" && !stored.command.ExpiresAt.After(time.Now().UTC()) {
		stored.command.Status, stored.command.ResultCode = "expired", "expired"
	}
	return stored, nil
}

func queryCommandByOperation(ctx context.Context, tx pgx.Tx, sessionID, operationID string, sender bool) (storedCommand, bool, error) {
	column := "target_session_id"
	if sender {
		column = "sender_session_id"
	}
	stored, err := scanStoredCommand(tx.QueryRow(ctx, `
		SELECT command.operation_id::text,command.command,command.payload,sender_device.name,
		       command.result_status,command.result_code,command.created_at,command.expires_at,command.target_session_id::text
		FROM playback_commands command
		JOIN auth_sessions sender ON sender.id=command.sender_session_id
		JOIN devices sender_device ON sender_device.id=sender.device_id
		WHERE command.`+column+`=$1::uuid AND command.operation_id=$2::uuid
		FOR UPDATE OF command
	`, sessionID, operationID))
	if errors.Is(err, pgx.ErrNoRows) {
		return storedCommand{}, false, nil
	}
	if err != nil {
		return storedCommand{}, false, err
	}
	return stored, true, nil
}

func sameCommandInput(left, right CommandInput) bool {
	leftJSON, leftErr := json.Marshal(left)
	rightJSON, rightErr := json.Marshal(right)
	return leftErr == nil && rightErr == nil && string(leftJSON) == string(rightJSON)
}

func validCommandResult(input CommandResultInput) bool {
	switch input.Status {
	case "applied":
		return input.Code == "applied"
	case "failed":
		switch input.Code {
		case "unsupported", "invalid_state", "stale_target", "execution_failed":
			return true
		}
	case "expired":
		return input.Code == "expired"
	}
	return false
}

func (s *Service) CreateRoom(ctx context.Context, principal auth.Principal, input CreateRoomInput) (Room, error) {
	if !validRoomState(input.State) || !validTimeline(input.PositionMilliseconds, input.DurationMilliseconds) {
		return Room{}, ErrInvalidInput
	}
	code, codeHash, err := newRoomCode()
	if err != nil {
		return Room{}, err
	}
	tx, profileID, err := s.beginAuthorizedProfileTx(ctx, principal)
	if err != nil {
		return Room{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	item, err := s.canonicalItemTx(ctx, tx, profileID, input.Item)
	if err != nil {
		return Room{}, err
	}
	itemJSON, err := json.Marshal(item)
	if err != nil {
		return Room{}, fmt.Errorf("encode room playback item: %w", err)
	}
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 2))`, principal.SessionID); err != nil {
		return Room{}, fmt.Errorf("lock playback room admission: %w", err)
	}
	var roomCount int
	if err := tx.QueryRow(ctx, `
		SELECT count(*) FROM playback_rooms
		WHERE host_session_id = $1::uuid AND expires_at > clock_timestamp()
	`, principal.SessionID).Scan(&roomCount); err != nil {
		return Room{}, fmt.Errorf("count playback rooms: %w", err)
	}
	if roomCount >= maximumRoomsPerSession {
		return Room{}, ErrCapacity
	}
	now := s.now()
	var room Room
	if err := tx.QueryRow(ctx, `
		INSERT INTO playback_rooms (
			host_session_id, host_profile_id, join_code_hash, title_id, playback_item,
			state, position_milliseconds, duration_milliseconds, created_at, updated_at, expires_at
		) VALUES ($1::uuid, $2::uuid, $3, $4::uuid, $5::jsonb, $6, $7, $8, $9, $9, $10)
		RETURNING id::text, state, position_milliseconds, duration_milliseconds, version, updated_at, expires_at
	`, principal.SessionID, profileID, codeHash[:], item.TitleID, itemJSON, input.State,
		input.PositionMilliseconds, input.DurationMilliseconds, now, now.Add(roomTTL)).Scan(
		&room.ID, &room.State, &room.PositionMilliseconds, &room.DurationMilliseconds,
		&room.Version, &room.UpdatedAt, &room.ExpiresAt,
	); err != nil {
		return Room{}, fmt.Errorf("create playback room: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO playback_room_members (room_id, auth_session_id, profile_id, role, joined_at, last_seen_at)
		VALUES ($1::uuid, $2::uuid, $3::uuid, 'host', $4, $4)
	`, room.ID, principal.SessionID, profileID, now); err != nil {
		return Room{}, fmt.Errorf("create playback room host: %w", err)
	}
	room.JoinCode = code
	room.Item = item
	room.Members, err = s.roomMembersTx(ctx, tx, principal.SessionID, room.ID)
	if err != nil {
		return Room{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Room{}, fmt.Errorf("commit playback room: %w", err)
	}
	return room, nil
}

func (s *Service) JoinRoom(ctx context.Context, principal auth.Principal, code string) (Room, error) {
	code = normalizeRoomCode(code)
	if !roomCodePattern.MatchString(code) {
		return Room{}, ErrInvalidInput
	}
	codeHash := sha256.Sum256([]byte(code))
	tx, profileID, err := s.beginAuthorizedProfileTx(ctx, principal)
	if err != nil {
		return Room{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	locked, err := s.lockLiveRoom(ctx, tx, "", codeHash[:])
	if err != nil {
		return Room{}, err
	}
	item, err := s.canonicalItemTx(ctx, tx, profileID, PlaybackItem{TitleID: locked.TitleID})
	if err != nil {
		return Room{}, err
	}
	now := s.now()
	if _, err := tx.Exec(ctx, `
		DELETE FROM playback_room_members
		WHERE room_id = $1::uuid AND role = 'participant' AND last_seen_at <= clock_timestamp() - $2::interval
	`, locked.ID, intervalLiteral(presenceTTL)); err != nil {
		return Room{}, fmt.Errorf("remove stale playback room members: %w", err)
	}
	var members int
	var alreadyJoined bool
	if err := tx.QueryRow(ctx, `
		SELECT count(*), bool_or(auth_session_id = $2::uuid)
		FROM playback_room_members WHERE room_id = $1::uuid
	`, locked.ID, principal.SessionID).Scan(&members, &alreadyJoined); err != nil {
		return Room{}, fmt.Errorf("count playback room members: %w", err)
	}
	if members >= maximumRoomMembers && !alreadyJoined {
		return Room{}, ErrCapacity
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO playback_room_members (room_id, auth_session_id, profile_id, role, joined_at, last_seen_at)
		VALUES ($1::uuid, $2::uuid, $3::uuid, 'participant', $4, $4)
		ON CONFLICT (room_id, auth_session_id) DO UPDATE SET profile_id = EXCLUDED.profile_id, last_seen_at = EXCLUDED.last_seen_at
	`, locked.ID, principal.SessionID, profileID, now); err != nil {
		return Room{}, fmt.Errorf("join playback room: %w", err)
	}
	if _, err := tx.Exec(ctx, `UPDATE playback_rooms SET expires_at = $2 WHERE id = $1::uuid`, locked.ID, now.Add(roomTTL)); err != nil {
		return Room{}, fmt.Errorf("extend playback room: %w", err)
	}
	room := locked.Room
	room.Item = item
	room.ExpiresAt = now.Add(roomTTL)
	room.Members, err = s.roomMembersTx(ctx, tx, principal.SessionID, room.ID)
	if err != nil {
		return Room{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Room{}, fmt.Errorf("commit playback room join: %w", err)
	}
	return room, nil
}

func (s *Service) Room(ctx context.Context, principal auth.Principal, roomID string) (Room, error) {
	roomID = strings.TrimSpace(roomID)
	if !uuidPattern.MatchString(roomID) {
		return Room{}, ErrNotFound
	}
	tx, profileID, err := s.beginAuthorizedProfileTx(ctx, principal)
	if err != nil {
		return Room{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	locked, err := s.lockLiveRoom(ctx, tx, roomID, nil)
	if err != nil {
		return Room{}, err
	}
	item, err := s.canonicalItemTx(ctx, tx, profileID, PlaybackItem{TitleID: locked.TitleID})
	if err != nil {
		return Room{}, err
	}
	now := s.now()
	result, err := tx.Exec(ctx, `
		UPDATE playback_room_members
		SET last_seen_at = $4
		WHERE room_id = $1::uuid AND auth_session_id = $2::uuid AND profile_id = $3::uuid
	`, roomID, principal.SessionID, profileID, now)
	if err != nil {
		return Room{}, fmt.Errorf("refresh playback room member: %w", err)
	}
	if result.RowsAffected() == 0 {
		return Room{}, ErrNotFound
	}
	room := locked.Room
	room.Item = item
	room.Members, err = s.roomMembersTx(ctx, tx, principal.SessionID, room.ID)
	if err != nil {
		return Room{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Room{}, fmt.Errorf("commit playback room read: %w", err)
	}
	return room, nil
}

func (s *Service) UpdateRoom(ctx context.Context, principal auth.Principal, roomID string, input UpdateRoomInput) (Room, error) {
	roomID = strings.TrimSpace(roomID)
	if !uuidPattern.MatchString(roomID) || !validRoomState(input.State) ||
		!validTimeline(input.PositionMilliseconds, input.DurationMilliseconds) || input.ExpectedVersion <= 0 {
		return Room{}, ErrInvalidInput
	}
	tx, profileID, err := s.beginAuthorizedProfileTx(ctx, principal)
	if err != nil {
		return Room{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	locked, err := s.lockLiveRoom(ctx, tx, roomID, nil)
	if err != nil {
		return Room{}, err
	}
	item, err := s.canonicalItemTx(ctx, tx, profileID, PlaybackItem{TitleID: locked.TitleID})
	if err != nil {
		return Room{}, err
	}
	var host bool
	if err := tx.QueryRow(ctx, `
		SELECT role = 'host'
		FROM playback_room_members
		WHERE room_id = $1::uuid AND auth_session_id = $2::uuid AND profile_id = $3::uuid
	`, roomID, principal.SessionID, profileID).Scan(&host); errors.Is(err, pgx.ErrNoRows) {
		return Room{}, ErrForbidden
	} else if err != nil {
		return Room{}, fmt.Errorf("authorize playback room host: %w", err)
	}
	if !host {
		return Room{}, ErrForbidden
	}
	if locked.Version != input.ExpectedVersion {
		return Room{}, ErrConflict
	}
	now := s.now()
	var room Room
	if err := tx.QueryRow(ctx, `
		UPDATE playback_rooms
		SET state = $2, position_milliseconds = $3, duration_milliseconds = $4,
		    version = version + 1, updated_at = $5, expires_at = $6
		WHERE id = $1::uuid AND version = $7
		RETURNING id::text, state, position_milliseconds, duration_milliseconds, version, updated_at, expires_at
	`, roomID, input.State, input.PositionMilliseconds, input.DurationMilliseconds, now,
		now.Add(roomTTL), input.ExpectedVersion).Scan(
		&room.ID, &room.State, &room.PositionMilliseconds, &room.DurationMilliseconds,
		&room.Version, &room.UpdatedAt, &room.ExpiresAt,
	); errors.Is(err, pgx.ErrNoRows) {
		return Room{}, ErrConflict
	} else if err != nil {
		return Room{}, fmt.Errorf("update playback room: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE playback_room_members SET last_seen_at = $3
		WHERE room_id = $1::uuid AND auth_session_id = $2::uuid
	`, roomID, principal.SessionID, now); err != nil {
		return Room{}, fmt.Errorf("refresh playback room host: %w", err)
	}
	room.Item = item
	room.Members, err = s.roomMembersTx(ctx, tx, principal.SessionID, room.ID)
	if err != nil {
		return Room{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Room{}, fmt.Errorf("commit playback room update: %w", err)
	}
	return room, nil
}

func (s *Service) LeaveRoom(ctx context.Context, principal auth.Principal, roomID string) error {
	if !uuidPattern.MatchString(strings.TrimSpace(roomID)) {
		return ErrNotFound
	}
	tx, profileID, err := s.beginAuthorizedProfileTx(ctx, principal)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := s.lockLiveRoom(ctx, tx, roomID, nil); err != nil {
		return err
	}
	var role string
	if err := tx.QueryRow(ctx, `
		SELECT role FROM playback_room_members
		WHERE room_id = $1::uuid AND auth_session_id = $2::uuid AND profile_id = $3::uuid
		FOR UPDATE
	`, roomID, principal.SessionID, profileID).Scan(&role); errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	} else if err != nil {
		return fmt.Errorf("lock playback room membership: %w", err)
	}
	if role == "host" {
		if _, err := tx.Exec(ctx, `DELETE FROM playback_rooms WHERE id = $1::uuid`, roomID); err != nil {
			return fmt.Errorf("close playback room: %w", err)
		}
	} else if _, err := tx.Exec(ctx, `
		DELETE FROM playback_room_members WHERE room_id = $1::uuid AND auth_session_id = $2::uuid
	`, roomID, principal.SessionID); err != nil {
		return fmt.Errorf("leave playback room: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit playback room leave: %w", err)
	}
	return nil
}

func (s *Service) Cleanup(ctx context.Context) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin playback coordination cleanup: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `
		DELETE FROM playback_commands
		WHERE expires_at <= clock_timestamp() - interval '10 minutes'
		   OR completed_at < clock_timestamp() - interval '10 minutes'
	`); err != nil {
		return fmt.Errorf("clean playback coordination commands: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		DELETE FROM playback_rooms room
		WHERE room.expires_at <= clock_timestamp()
		   OR NOT `+liveRoomHostPredicateSQL, intervalLiteral(presenceTTL)); err != nil {
		return fmt.Errorf("clean playback coordination rooms: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		DELETE FROM playback_device_presence
		WHERE last_seen_at <= clock_timestamp() - interval '24 hours'
	`); err != nil {
		return fmt.Errorf("clean playback coordination presence: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit playback coordination cleanup: %w", err)
	}
	return nil
}
func (s *Service) RunScheduled(ctx context.Context) error {
	return s.Cleanup(ctx)
}

func (s *Service) normalizeHeartbeat(input DeviceHeartbeatInput) (DeviceHeartbeatInput, error) {
	if len(input.Capabilities) > maximumCapabilities || !validDeviceState(input.State) {
		return DeviceHeartbeatInput{}, ErrInvalidInput
	}
	seen := make(map[string]struct{}, len(input.Capabilities))
	capabilities := make([]string, 0, len(input.Capabilities))
	for _, raw := range input.Capabilities {
		capability := strings.ToLower(strings.TrimSpace(raw))
		if !capabilityPattern.MatchString(capability) {
			return DeviceHeartbeatInput{}, ErrInvalidInput
		}
		if _, duplicate := seen[capability]; duplicate {
			continue
		}
		seen[capability] = struct{}{}
		capabilities = append(capabilities, capability)
	}
	sort.Strings(capabilities)
	input.Capabilities = capabilities
	return input, nil
}

func (s *Service) normalizeCommand(input CommandInput) (CommandInput, error) {
	input.OperationID = strings.ToLower(strings.TrimSpace(input.OperationID))
	input.Command = strings.ToLower(strings.TrimSpace(input.Command))
	input.Mode = strings.ToLower(strings.TrimSpace(input.Mode))
	if !uuidPattern.MatchString(input.OperationID) || input.TargetRevision != nil && *input.TargetRevision <= 0 {
		return CommandInput{}, ErrInvalidInput
	}
	switch input.Command {
	case "play", "pause", "stop":
		if input.Mode != "" || input.Item != nil || input.PositionMilliseconds != nil {
			return CommandInput{}, ErrInvalidInput
		}
	case "seek":
		if input.Mode != "" || input.Item != nil || input.PositionMilliseconds == nil || *input.PositionMilliseconds < 0 || *input.PositionMilliseconds > maximumPositionMillis {
			return CommandInput{}, ErrInvalidInput
		}
	case "load":
		if input.Mode != "handoff" && input.Mode != "play-copy" || input.Item == nil || input.PositionMilliseconds == nil || *input.PositionMilliseconds < 0 || *input.PositionMilliseconds > maximumPositionMillis {
			return CommandInput{}, ErrInvalidInput
		}
	default:
		return CommandInput{}, ErrInvalidInput
	}
	return input, nil
}

func (s *Service) canonicalItemTx(ctx context.Context, tx pgx.Tx, profileID string, requested PlaybackItem) (PlaybackItem, error) {
	titleID := strings.TrimSpace(requested.TitleID)
	if !uuidPattern.MatchString(titleID) {
		return PlaybackItem{}, ErrInvalidInput
	}
	var sourceAddonID *string
	if err := tx.QueryRow(ctx, `
		SELECT source_addon_id::text
		FROM titles
		WHERE id = $1::uuid
		FOR UPDATE
	`, titleID).Scan(&sourceAddonID); errors.Is(err, pgx.ErrNoRows) {
		return PlaybackItem{}, ErrNotFound
	} else if err != nil {
		return PlaybackItem{}, fmt.Errorf("lock coordinated playback title: %w", err)
	}
	identityRows, err := tx.Query(ctx, `
		SELECT profile_id::text
		FROM profile_title_external_ids
		WHERE title_id = $1::uuid
		ORDER BY profile_id
		FOR SHARE
	`, titleID)
	if err != nil {
		return PlaybackItem{}, fmt.Errorf("lock coordinated playback title identities: %w", err)
	}
	identityRows.Close()
	if err := identityRows.Err(); err != nil {
		return PlaybackItem{}, fmt.Errorf("iterate coordinated playback title identities: %w", err)
	}
	if sourceAddonID != nil {
		var lockedAddonID string
		if err := tx.QueryRow(ctx, `
			SELECT id::text FROM profile_addons
			WHERE id = $1::uuid AND enabled
			FOR SHARE
		`, *sourceAddonID).Scan(&lockedAddonID); errors.Is(err, pgx.ErrNoRows) {
			return PlaybackItem{}, ErrNotFound
		} else if err != nil {
			return PlaybackItem{}, fmt.Errorf("lock coordinated playback add-on: %w", err)
		}
		var lockedAccessID string
		err := tx.QueryRow(ctx, `
			SELECT addon_id::text FROM addon_profile_access
			WHERE addon_id = $1::uuid AND profile_id = $2::uuid
			FOR SHARE
		`, *sourceAddonID, profileID).Scan(&lockedAccessID)
		if errors.Is(err, pgx.ErrNoRows) {
			err = tx.QueryRow(ctx, `
				SELECT access.addon_id::text
				FROM addon_category_access access
				JOIN profiles profile ON profile.category_id = access.category_id
				WHERE access.addon_id = $1::uuid AND profile.id = $2::uuid
				FOR SHARE OF access
			`, *sourceAddonID, profileID).Scan(&lockedAccessID)
		}
		if errors.Is(err, pgx.ErrNoRows) {
			return PlaybackItem{}, ErrNotFound
		} else if err != nil {
			return PlaybackItem{}, fmt.Errorf("lock coordinated playback add-on access: %w", err)
		}
	}
	var item PlaybackItem
	if err := tx.QueryRow(ctx, `
		SELECT title.id::text, title.media_type, COALESCE(title.resource_id, ''),
		       COALESCE(title.source_addon_id::text, ''), COALESCE(title.display_title, ''),
		       COALESCE(title.poster_url, '')
		FROM titles title
		WHERE title.id = $2::uuid AND title.is_current
		  AND CASE
		    WHEN title.media_type = 'tv' THEN EXISTS (
		        SELECT 1 FROM profile_addons addon
		        JOIN profiles profile ON profile.id = $1::uuid
		        WHERE addon.id = title.source_addon_id AND addon.enabled
		          AND (EXISTS (SELECT 1 FROM addon_profile_access access WHERE access.addon_id = addon.id AND access.profile_id = profile.id)
		               OR EXISTS (SELECT 1 FROM addon_category_access access WHERE access.addon_id = addon.id AND access.category_id = profile.category_id))
		    )
		    ELSE (NOT EXISTS (SELECT 1 FROM profile_title_external_ids scoped WHERE scoped.title_id = title.id)
		          OR EXISTS (SELECT 1 FROM profile_title_external_ids scoped WHERE scoped.title_id = title.id AND scoped.profile_id = $1::uuid))
		      AND (title.source_addon_id IS NULL OR EXISTS (
		        SELECT 1 FROM profile_addons addon
		        JOIN profiles profile ON profile.id = $1::uuid
		        WHERE addon.id = title.source_addon_id AND addon.enabled
		          AND (EXISTS (SELECT 1 FROM addon_profile_access access WHERE access.addon_id = addon.id AND access.profile_id = profile.id)
		               OR EXISTS (SELECT 1 FROM addon_category_access access WHERE access.addon_id = addon.id AND access.category_id = profile.category_id))
		      ))
		  END
	`, profileID, titleID).Scan(&item.TitleID, &item.MediaType, &item.ResourceID,
		&item.SourceAddonID, &item.Title, &item.PosterURL); errors.Is(err, pgx.ErrNoRows) {
		return PlaybackItem{}, ErrNotFound
	} else if err != nil {
		return PlaybackItem{}, fmt.Errorf("authorize coordinated playback title: %w", err)
	}
	if item.ResourceID == "" || !playableMediaType(item.MediaType) {
		return PlaybackItem{}, ErrInvalidInput
	}
	return item, nil
}

func (s *Service) beginAuthorizedProfileTx(ctx context.Context, principal auth.Principal) (pgx.Tx, string, error) {
	if principal.ActiveProfileID == nil || strings.TrimSpace(*principal.ActiveProfileID) == "" ||
		principal.ProfileGrantExpiresAt == nil || !principal.ProfileGrantExpiresAt.After(s.now()) {
		return nil, "", ErrActiveProfileRequired
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, "", fmt.Errorf("begin playback coordination transaction: %w", err)
	}
	profileID := *principal.ActiveProfileID
	authorized, err := auth.AuthorizeAndLockProfiles(ctx, tx, principal, []string{profileID}, false)
	if err != nil {
		_ = tx.Rollback(ctx)
		return nil, "", fmt.Errorf("authorize playback coordination profile: %w", err)
	}
	valid, err := auth.LockActiveProfileSelection(ctx, tx, principal)
	if err != nil {
		_ = tx.Rollback(ctx)
		return nil, "", fmt.Errorf("lock playback coordination profile selection: %w", err)
	}
	if !authorized || !valid {
		_ = tx.Rollback(ctx)
		return nil, "", ErrActiveProfileRequired
	}
	return tx, profileID, nil
}

type lockedRoom struct {
	Room
	TitleID string
}

func (s *Service) lockLiveRoom(ctx context.Context, tx pgx.Tx, roomID string, codeHash []byte) (lockedRoom, error) {
	var room lockedRoom
	if err := tx.QueryRow(ctx, `
		SELECT room.id::text, room.title_id::text, room.state, room.position_milliseconds,
		       room.duration_milliseconds, room.version, room.updated_at, room.expires_at
		FROM playback_rooms room
		WHERE room.expires_at > clock_timestamp()
		  AND `+liveRoomHostPredicateSQL+`
		  AND (($2 <> '' AND room.id::text = $2) OR ($2 = '' AND room.join_code_hash = $3))
		FOR UPDATE OF room
	`, intervalLiteral(presenceTTL), roomID, codeHash).Scan(
		&room.ID, &room.TitleID, &room.State, &room.PositionMilliseconds, &room.DurationMilliseconds,
		&room.Version, &room.UpdatedAt, &room.ExpiresAt,
	); errors.Is(err, pgx.ErrNoRows) {
		return lockedRoom{}, ErrNotFound
	} else if err != nil {
		return lockedRoom{}, fmt.Errorf("lock live playback room: %w", err)
	}
	return room, nil
}

func (s *Service) roomMembersTx(ctx context.Context, tx pgx.Tx, currentSessionID, roomID string) ([]RoomMember, error) {
	rows, err := tx.Query(ctx, `
		SELECT member.member_id::text, profile.name, device.name, device.platform,
		       member.role, member.auth_session_id = $2::uuid, member.joined_at, member.last_seen_at
		FROM playback_room_members member
		JOIN profiles profile ON profile.id = member.profile_id
		JOIN auth_sessions session ON session.id = member.auth_session_id
		JOIN devices device ON device.id = session.device_id
		WHERE member.room_id = $1::uuid AND member.last_seen_at > $3
		ORDER BY member.role = 'host' DESC, member.joined_at, member.member_id
	`, roomID, currentSessionID, s.now().Add(-presenceTTL))
	if err != nil {
		return nil, fmt.Errorf("query playback room members: %w", err)
	}
	defer rows.Close()
	members := make([]RoomMember, 0)
	for rows.Next() {
		var member RoomMember
		if err := rows.Scan(&member.MemberID, &member.Profile, &member.DeviceName, &member.Platform,
			&member.Role, &member.Current, &member.JoinedAt, &member.LastSeenAt); err != nil {
			return nil, fmt.Errorf("scan playback room member: %w", err)
		}
		members = append(members, member)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate playback room members: %w", err)
	}
	return members, nil
}

func scanDevice(row pgx.Row) (Device, error) {
	var device Device
	var stateJSON []byte
	if err := row.Scan(&device.SessionID, &device.DeviceID, &device.Name, &device.Platform,
		&device.Capabilities, &stateJSON, &device.Revision, &device.Current, &device.LastSeenAt); err != nil {
		return Device{}, err
	}
	if err := json.Unmarshal(stateJSON, &device.State); err != nil {
		return Device{}, err
	}
	if device.Capabilities == nil {
		device.Capabilities = []string{}
	}
	return device, nil
}

func newRoomCode() (string, [32]byte, error) {
	const alphabet = "23456789ABCDEFGHJKMNPQRSTUVWXYZ"
	var randomBytes [10]byte
	if _, err := rand.Read(randomBytes[:]); err != nil {
		return "", [32]byte{}, fmt.Errorf("create playback room code: %w", err)
	}
	code := make([]byte, len(randomBytes))
	for index, value := range randomBytes {
		code[index] = alphabet[int(value)%len(alphabet)]
	}
	text := string(code)
	return text, sha256.Sum256([]byte(text)), nil
}

func normalizeRoomCode(value string) string {
	value = strings.ToUpper(strings.TrimSpace(value))
	value = strings.ReplaceAll(value, "-", "")
	value = strings.ReplaceAll(value, " ", "")
	return value
}

func validDeviceState(state DeviceState) bool {
	if !validTimeline(state.PositionMilliseconds, state.DurationMilliseconds) {
		return false
	}
	switch state.Status {
	case "idle":
		return state.Item == nil && state.PositionMilliseconds == 0 && state.DurationMilliseconds == 0
	case "playing", "paused", "ended":
		return state.Item != nil
	default:
		return false
	}
}

func validRoomState(state string) bool {
	return state == "playing" || state == "paused" || state == "ended"
}

func validTimeline(position, duration int64) bool {
	return position >= 0 && duration >= 0 && position <= maximumPositionMillis && duration <= maximumPositionMillis &&
		(duration == 0 || position <= duration+30_000)
}

func playableMediaType(value string) bool {
	switch value {
	case "movie", "episode", "video", "tv":
		return true
	default:
		return false
	}
}

func intervalLiteral(duration time.Duration) string {
	return fmt.Sprintf("%d microseconds", duration.Microseconds())
}

var uuidPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[1-8][0-9a-fA-F]{3}-[89abAB][0-9a-fA-F]{3}-[0-9a-fA-F]{12}$`)
