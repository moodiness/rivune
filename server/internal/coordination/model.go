package coordination

import (
	"errors"
	"time"
)

var (
	ErrActiveProfileRequired = errors.New("an active profile is required")
	ErrInvalidInput          = errors.New("invalid playback coordination input")
	ErrNotFound              = errors.New("playback coordination resource not found")
	ErrConflict              = errors.New("playback coordination state conflict")
	ErrForbidden             = errors.New("playback coordination operation forbidden")
	ErrCapacity              = errors.New("playback coordination capacity reached")
)

type PlaybackItem struct {
	TitleID       string `json:"titleId"`
	MediaType     string `json:"mediaType"`
	ResourceID    string `json:"resourceId"`
	SourceAddonID string `json:"sourceAddonId,omitempty"`
	Title         string `json:"title"`
	PosterURL     string `json:"posterUrl,omitempty"`
}

type DeviceState struct {
	Status               string        `json:"status"`
	Item                 *PlaybackItem `json:"item,omitempty"`
	PositionMilliseconds int64         `json:"positionMilliseconds"`
	DurationMilliseconds int64         `json:"durationMilliseconds"`
	UpdatedAt            time.Time     `json:"updatedAt"`
}

type DeviceHeartbeatInput struct {
	Capabilities []string    `json:"capabilities"`
	State        DeviceState `json:"state"`
}

type Device struct {
	SessionID    string      `json:"sessionId"`
	DeviceID     string      `json:"deviceId"`
	Name         string      `json:"name"`
	Platform     string      `json:"platform"`
	Capabilities []string    `json:"capabilities"`
	State        DeviceState `json:"state"`
	Revision     int64       `json:"revision"`
	Current      bool        `json:"current"`
	LastSeenAt   time.Time   `json:"lastSeenAt"`
}

type DeviceList struct {
	Devices []Device `json:"devices"`
}

type CommandInput struct {
	OperationID          string        `json:"operationId"`
	Command              string        `json:"command"`
	Mode                 string        `json:"mode,omitempty"`
	TargetRevision       *int64        `json:"targetRevision,omitempty"`
	Item                 *PlaybackItem `json:"item,omitempty"`
	PositionMilliseconds *int64        `json:"positionMilliseconds,omitempty"`
}

type CommandResultInput struct {
	Status string `json:"status"`
	Code   string `json:"code"`
}

type Command struct {
	OperationID          string        `json:"operationId"`
	Command              string        `json:"command"`
	Mode                 string        `json:"mode,omitempty"`
	TargetRevision       *int64        `json:"targetRevision,omitempty"`
	Item                 *PlaybackItem `json:"item,omitempty"`
	PositionMilliseconds *int64        `json:"positionMilliseconds,omitempty"`
	SenderDeviceName     string        `json:"senderDeviceName"`
	Status               string        `json:"status"`
	ResultCode           string        `json:"resultCode,omitempty"`
	CreatedAt            time.Time     `json:"createdAt"`
	ExpiresAt            time.Time     `json:"expiresAt"`
}

type CommandList struct {
	Commands []Command `json:"commands"`
}

type CreateRoomInput struct {
	Item                 PlaybackItem `json:"item"`
	State                string       `json:"state"`
	PositionMilliseconds int64        `json:"positionMilliseconds"`
	DurationMilliseconds int64        `json:"durationMilliseconds"`
}

type JoinRoomInput struct {
	Code string `json:"code"`
}

type UpdateRoomInput struct {
	State                string `json:"state"`
	PositionMilliseconds int64  `json:"positionMilliseconds"`
	DurationMilliseconds int64  `json:"durationMilliseconds"`
	ExpectedVersion      int64  `json:"expectedVersion"`
}

type RoomMember struct {
	MemberID   string    `json:"memberId"`
	Profile    string    `json:"profile"`
	DeviceName string    `json:"deviceName"`
	Platform   string    `json:"platform"`
	Role       string    `json:"role"`
	Current    bool      `json:"current"`
	JoinedAt   time.Time `json:"joinedAt"`
	LastSeenAt time.Time `json:"lastSeenAt"`
}

type Room struct {
	ID                   string       `json:"id"`
	JoinCode             string       `json:"joinCode,omitempty"`
	Item                 PlaybackItem `json:"item"`
	State                string       `json:"state"`
	PositionMilliseconds int64        `json:"positionMilliseconds"`
	DurationMilliseconds int64        `json:"durationMilliseconds"`
	Version              int64        `json:"version"`
	UpdatedAt            time.Time    `json:"updatedAt"`
	ExpiresAt            time.Time    `json:"expiresAt"`
	Members              []RoomMember `json:"members"`
}
