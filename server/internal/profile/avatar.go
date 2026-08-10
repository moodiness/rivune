package profile

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"image"
	"image/color"
	_ "image/jpeg"
	"image/png"
	"math"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/moodiness/rivune/server/internal/auth"
)

const (
	defaultAvatarPreset                   = "aurora"
	avatarOutputSize                      = 512
	maximumAvatarPixels                   = 16_777_216
	maximumConcurrentAvatarNormalizations = 2
)

// ErrAvatarNormalizationBusy reports that the process-wide avatar normalization capacity is full.
var ErrAvatarNormalizationBusy = errors.New("avatar normalization capacity exhausted")

var avatarNormalizationSlots = make(chan struct{}, maximumConcurrentAvatarNormalizations)

type AvatarPreset struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type avatarPresetDefinition struct {
	AvatarPreset
	Start  string
	End    string
	Accent string
	Path   string
}

type AvatarImage struct {
	ContentType string
	Data        []byte
	UpdatedAt   time.Time
}

var avatarPresetDefinitions = []avatarPresetDefinition{
	{AvatarPreset: AvatarPreset{ID: "aurora", Name: "Aurora"}, Start: "#432371", End: "#00D4A8", Accent: "#F7E8FF", Path: "M94 330C159 214 230 371 418 158C355 352 225 438 94 330Z"},
	{AvatarPreset: AvatarPreset{ID: "ember", Name: "Ember"}, Start: "#53131E", End: "#FF6B35", Accent: "#FFE8B6", Path: "M256 74C344 174 381 244 336 337C300 412 188 415 151 338C113 257 174 189 256 74Z"},
	{AvatarPreset: AvatarPreset{ID: "tide", Name: "Tide"}, Start: "#062A4D", End: "#168AAD", Accent: "#D9F7FF", Path: "M60 298C126 198 203 221 260 276C319 332 391 347 452 252C431 400 324 446 225 394C150 355 112 292 60 298Z"},
	{AvatarPreset: AvatarPreset{ID: "grove", Name: "Grove"}, Start: "#16351A", End: "#70A33A", Accent: "#EDFFD1", Path: "M256 65C278 172 379 172 414 239C449 308 393 403 306 421C227 437 142 390 114 309C87 231 152 149 256 65Z"},
	{AvatarPreset: AvatarPreset{ID: "violet", Name: "Violet"}, Start: "#24124D", End: "#9B5DE5", Accent: "#F4DCFF", Path: "M256 68L310 202L450 211L341 301L376 438L256 363L136 438L171 301L62 211L202 202Z"},
	{AvatarPreset: AvatarPreset{ID: "solar", Name: "Solar"}, Start: "#7A2E00", End: "#FFB703", Accent: "#FFF6C2", Path: "M256 91L292 194L401 167L340 259L432 319L323 318L310 427L256 332L202 427L189 318L80 319L172 259L111 167L220 194Z"},
	{AvatarPreset: AvatarPreset{ID: "glacier", Name: "Glacier"}, Start: "#12355B", End: "#5DD9C1", Accent: "#E9FFFD", Path: "M256 62L409 185L357 420H155L103 185Z"},
	{AvatarPreset: AvatarPreset{ID: "rose", Name: "Rose"}, Start: "#4A1942", End: "#E56B8A", Accent: "#FFE4EC", Path: "M256 421C215 367 99 303 104 207C108 132 201 107 256 181C311 107 404 132 408 207C413 303 297 367 256 421Z"},
	{AvatarPreset: AvatarPreset{ID: "luna", Name: "Luna"}, Start: "#10143D", End: "#6D5DFB", Accent: "#F5E8A8", Path: "M340 91C227 120 182 258 249 351C291 410 374 426 429 376C359 384 296 347 272 287C241 210 270 132 340 91Z"},
	{AvatarPreset: AvatarPreset{ID: "coral", Name: "Coral"}, Start: "#5A153B", End: "#FF7A7A", Accent: "#FFE6C7", Path: "M256 241C181 103 83 158 145 260C58 306 129 405 243 312C275 442 398 399 341 286C460 236 402 124 274 222C328 88 195 54 256 241Z"},
	{AvatarPreset: AvatarPreset{ID: "nebula", Name: "Nebula"}, Start: "#0B1026", End: "#D946EF", Accent: "#BFFBFF", Path: "M83 301C122 237 207 185 296 166C385 147 447 166 459 207C470 249 421 299 340 336C257 375 155 390 91 365C46 348 42 322 83 301ZM151 300C210 323 303 298 370 252C307 274 218 276 151 300Z"},
	{AvatarPreset: AvatarPreset{ID: "meadow", Name: "Meadow"}, Start: "#0F3D2E", End: "#82C91E", Accent: "#FFF3A3", Path: "M251 249C202 136 100 126 108 225C112 281 178 291 236 270C198 333 185 412 256 421C327 412 314 333 276 270C334 291 400 281 404 225C412 126 310 136 261 249Z"},
	{AvatarPreset: AvatarPreset{ID: "cobalt", Name: "Cobalt"}, Start: "#061B3A", End: "#2563EB", Accent: "#D8F3FF", Path: "M256 61L407 174L372 370L256 448L140 370L105 174Z"},
	{AvatarPreset: AvatarPreset{ID: "peach", Name: "Peach"}, Start: "#7C2D52", End: "#FDBA8C", Accent: "#FFF1E8", Path: "M122 352C70 352 51 286 88 252C110 232 139 227 165 238C174 174 230 132 292 151C335 164 366 198 374 241C430 232 467 274 455 322C446 360 411 382 372 382H133C126 382 122 369 122 352Z"},
	{AvatarPreset: AvatarPreset{ID: "volt", Name: "Volt"}, Start: "#18203F", End: "#00C2FF", Accent: "#F7FF72", Path: "M286 56L111 285H224L188 456L401 206H281Z"},
	{AvatarPreset: AvatarPreset{ID: "summit", Name: "Summit"}, Start: "#28203D", End: "#FF7A45", Accent: "#FFF0D1", Path: "M55 405L197 152L262 263L324 105L457 405Z"},
}

func AvatarPresets() []AvatarPreset {
	presets := make([]AvatarPreset, len(avatarPresetDefinitions))
	for index, definition := range avatarPresetDefinitions {
		presets[index] = definition.AvatarPreset
	}
	return presets
}

func AvatarPresetSVG(presetID string) ([]byte, bool) {
	definition, found := avatarPresetByID(presetID)
	if !found {
		return nil, false
	}
	svg := fmt.Sprintf(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 512 512" role="img" aria-label="%s profile avatar"><defs><linearGradient id="g" x1="0" y1="0" x2="1" y2="1"><stop stop-color="%s"/><stop offset="1" stop-color="%s"/></linearGradient><radialGradient id="h" cx="35%%" cy="28%%" r="65%%"><stop stop-color="#fff" stop-opacity=".42"/><stop offset="1" stop-color="#fff" stop-opacity="0"/></radialGradient></defs><rect width="512" height="512" rx="128" fill="url(#g)"/><circle cx="168" cy="142" r="190" fill="url(#h)"/><path d="%s" fill="%s" fill-opacity=".88"/><circle cx="214" cy="210" r="28" fill="#fff" fill-opacity=".3"/></svg>`, definition.Name, definition.Start, definition.End, definition.Path, definition.Accent)
	return []byte(svg), true
}

type orientedAvatarImage struct {
	source      image.Image
	orientation int
	width       int
	height      int
}

func (oriented orientedAvatarImage) ColorModel() color.Model {
	return oriented.source.ColorModel()
}

func (oriented orientedAvatarImage) Bounds() image.Rectangle {
	if oriented.orientation >= 5 {
		return image.Rect(0, 0, oriented.height, oriented.width)
	}
	return image.Rect(0, 0, oriented.width, oriented.height)
}

func (oriented orientedAvatarImage) At(x, y int) color.Color {
	bounds := oriented.source.Bounds()
	var sourceX, sourceY int
	switch oriented.orientation {
	case 2:
		sourceX, sourceY = oriented.width-1-x, y
	case 3:
		sourceX, sourceY = oriented.width-1-x, oriented.height-1-y
	case 4:
		sourceX, sourceY = x, oriented.height-1-y
	case 5:
		sourceX, sourceY = y, x
	case 6:
		sourceX, sourceY = y, oriented.height-1-x
	case 7:
		sourceX, sourceY = oriented.width-1-y, oriented.height-1-x
	case 8:
		sourceX, sourceY = oriented.width-1-y, x
	default:
		sourceX, sourceY = x, y
	}
	return oriented.source.At(bounds.Min.X+sourceX, bounds.Min.Y+sourceY)
}

func applyJPEGOrientation(source image.Image, input []byte) image.Image {
	orientation := jpegEXIFOrientation(input)
	if orientation <= 1 {
		return source
	}
	bounds := source.Bounds()
	return orientedAvatarImage{
		source: source, orientation: orientation, width: bounds.Dx(), height: bounds.Dy(),
	}
}

func jpegEXIFOrientation(input []byte) int {
	if len(input) < 4 || input[0] != 0xff || input[1] != 0xd8 {
		return 1
	}
	for offset := 2; offset+4 <= len(input); {
		if input[offset] != 0xff {
			return 1
		}
		for offset < len(input) && input[offset] == 0xff {
			offset++
		}
		if offset >= len(input) {
			return 1
		}
		marker := input[offset]
		offset++
		if marker == 0xd9 || marker == 0xda {
			return 1
		}
		if marker == 0x01 || marker >= 0xd0 && marker <= 0xd7 {
			continue
		}
		if offset+2 > len(input) {
			return 1
		}
		segmentLength := int(binary.BigEndian.Uint16(input[offset : offset+2]))
		if segmentLength < 2 || offset+segmentLength > len(input) {
			return 1
		}
		segment := input[offset+2 : offset+segmentLength]
		if marker == 0xe1 && len(segment) >= 14 && bytes.Equal(segment[:6], []byte("Exif\x00\x00")) {
			if orientation := tiffOrientation(segment[6:]); orientation != 1 {
				return orientation
			}
		}
		offset += segmentLength
	}
	return 1
}

func tiffOrientation(tiff []byte) int {
	if len(tiff) < 8 {
		return 1
	}
	var order binary.ByteOrder
	switch string(tiff[:2]) {
	case "II":
		order = binary.LittleEndian
	case "MM":
		order = binary.BigEndian
	default:
		return 1
	}
	if order.Uint16(tiff[2:4]) != 42 {
		return 1
	}
	ifdOffset := uint64(order.Uint32(tiff[4:8]))
	if ifdOffset+2 > uint64(len(tiff)) {
		return 1
	}
	entryCount := uint64(order.Uint16(tiff[ifdOffset : ifdOffset+2]))
	firstEntry := ifdOffset + 2
	if entryCount > (uint64(len(tiff))-firstEntry)/12 {
		return 1
	}
	for index := uint64(0); index < entryCount; index++ {
		entryOffset := firstEntry + index*12
		entry := tiff[entryOffset : entryOffset+12]
		if order.Uint16(entry[:2]) != 0x0112 {
			continue
		}
		if order.Uint16(entry[2:4]) != 3 || order.Uint32(entry[4:8]) != 1 {
			return 1
		}
		orientation := int(order.Uint16(entry[8:10]))
		if orientation >= 1 && orientation <= 8 {
			return orientation
		}
		return 1
	}
	return 1
}

func NormalizeAvatarImage(input []byte) ([]byte, error) {
	return normalizeAvatarImageBounded(input, normalizeAvatarImage)
}

func normalizeAvatarImageBounded(input []byte, normalize func([]byte) ([]byte, error)) ([]byte, error) {
	select {
	case avatarNormalizationSlots <- struct{}{}:
		defer func() { <-avatarNormalizationSlots }()
	default:
		return nil, ErrAvatarNormalizationBusy
	}
	return normalize(input)
}

func normalizeAvatarImage(input []byte) ([]byte, error) {
	if len(input) == 0 || len(input) > 2<<20 {
		return nil, fmt.Errorf("%w: avatar image must not exceed 2 MiB", ErrInvalidInput)
	}
	configuration, format, err := image.DecodeConfig(bytes.NewReader(input))
	if err != nil || (format != "png" && format != "jpeg") {
		return nil, fmt.Errorf("%w: avatar must be a PNG or JPEG image", ErrInvalidInput)
	}
	if configuration.Width < 64 || configuration.Height < 64 || configuration.Width > 4096 || configuration.Height > 4096 || configuration.Width*configuration.Height > maximumAvatarPixels {
		return nil, fmt.Errorf("%w: avatar dimensions must be between 64 and 4096 pixels", ErrInvalidInput)
	}
	source, decodedFormat, err := image.Decode(bytes.NewReader(input))
	if err != nil || decodedFormat != format {
		return nil, fmt.Errorf("%w: avatar image could not be decoded", ErrInvalidInput)
	}
	if format == "jpeg" {
		source = applyJPEGOrientation(source, input)
	}
	bounds := source.Bounds()
	side := bounds.Dx()
	if bounds.Dy() < side {
		side = bounds.Dy()
	}
	left := bounds.Min.X + (bounds.Dx()-side)/2
	top := bounds.Min.Y + (bounds.Dy()-side)/2
	output := image.NewNRGBA(image.Rect(0, 0, avatarOutputSize, avatarOutputSize))
	for y := 0; y < avatarOutputSize; y++ {
		sourceY := (float64(y)+0.5)*float64(side)/avatarOutputSize - 0.5
		sourceYFloor := math.Floor(sourceY)
		y0 := clamp(int(sourceYFloor), 0, side-1)
		y1 := clamp(int(sourceYFloor)+1, 0, side-1)
		yWeight := sourceY - sourceYFloor
		for x := 0; x < avatarOutputSize; x++ {
			sourceX := (float64(x)+0.5)*float64(side)/avatarOutputSize - 0.5
			sourceXFloor := math.Floor(sourceX)
			x0 := clamp(int(sourceXFloor), 0, side-1)
			x1 := clamp(int(sourceXFloor)+1, 0, side-1)
			xWeight := sourceX - sourceXFloor
			topLeft := color.NRGBAModel.Convert(source.At(left+x0, top+y0)).(color.NRGBA)
			topRight := color.NRGBAModel.Convert(source.At(left+x1, top+y0)).(color.NRGBA)
			bottomLeft := color.NRGBAModel.Convert(source.At(left+x0, top+y1)).(color.NRGBA)
			bottomRight := color.NRGBAModel.Convert(source.At(left+x1, top+y1)).(color.NRGBA)
			output.SetNRGBA(x, y, color.NRGBA{
				R: bilinear(topLeft.R, topRight.R, bottomLeft.R, bottomRight.R, xWeight, yWeight),
				G: bilinear(topLeft.G, topRight.G, bottomLeft.G, bottomRight.G, xWeight, yWeight),
				B: bilinear(topLeft.B, topRight.B, bottomLeft.B, bottomRight.B, xWeight, yWeight),
				A: bilinear(topLeft.A, topRight.A, bottomLeft.A, bottomRight.A, xWeight, yWeight),
			})
		}
	}
	var encoded bytes.Buffer
	encoder := png.Encoder{CompressionLevel: png.DefaultCompression}
	if err := encoder.Encode(&encoded, output); err != nil {
		return nil, fmt.Errorf("encode profile avatar: %w", err)
	}
	if encoded.Len() > 2<<20 {
		return nil, fmt.Errorf("%w: normalized avatar exceeds 2 MiB", ErrInvalidInput)
	}
	return encoded.Bytes(), nil
}

func (s *Service) SetAvatarPreset(ctx context.Context, principal auth.Principal, profileID, presetID string) (Profile, error) {
	presetID = strings.TrimSpace(presetID)
	if _, found := avatarPresetByID(presetID); !found {
		return Profile{}, fmt.Errorf("%w: unknown avatar preset", ErrInvalidInput)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Profile{}, fmt.Errorf("begin avatar preset update: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	profile, err := s.updateAvatar(ctx, tx, principal, profileID, presetID, nil)
	if err != nil {
		return Profile{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Profile{}, fmt.Errorf("commit avatar preset update: %w", err)
	}
	return profile, nil
}

func (s *Service) AuthorizeAvatarUpload(ctx context.Context, principal auth.Principal, profileID string) error {
	profileID = strings.TrimSpace(profileID)
	authorized, err := auth.CanManageProfiles(ctx, s.pool, principal, []string{profileID})
	if err != nil {
		return fmt.Errorf("preflight authorize custom profile avatar update: %w", err)
	}
	if !authorized {
		return ErrNotFound
	}
	return nil
}

func (s *Service) SetAvatarImage(ctx context.Context, principal auth.Principal, profileID string, imageData []byte) (Profile, error) {
	if err := s.AuthorizeAvatarUpload(ctx, principal, profileID); err != nil {
		return Profile{}, err
	}
	normalized, err := NormalizeAvatarImage(imageData)
	if err != nil {
		return Profile{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Profile{}, fmt.Errorf("begin custom avatar update: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	profile, err := s.updateAvatar(ctx, tx, principal, profileID, "", normalized)
	if err != nil {
		return Profile{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Profile{}, fmt.Errorf("commit custom avatar update: %w", err)
	}
	return profile, nil
}

func (s *Service) AvatarImage(ctx context.Context, principal auth.Principal, profileID string) (AvatarImage, error) {
	profileID = strings.TrimSpace(profileID)
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return AvatarImage{}, fmt.Errorf("begin custom profile avatar read: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	authorized, err := auth.AuthorizeAndLockProfiles(ctx, tx, principal, []string{profileID}, false)
	if err != nil {
		return AvatarImage{}, fmt.Errorf("authorize custom profile avatar: %w", err)
	}
	if !authorized {
		return AvatarImage{}, ErrNotFound
	}
	var image AvatarImage
	err = tx.QueryRow(ctx, `
		SELECT avatar.content_type, avatar.image_data, avatar.updated_at
		FROM profile_avatar_images avatar
		WHERE avatar.profile_id::text = $1
	`, profileID).Scan(&image.ContentType, &image.Data, &image.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return AvatarImage{}, ErrNotFound
	}
	if err != nil {
		return AvatarImage{}, fmt.Errorf("read custom profile avatar: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return AvatarImage{}, fmt.Errorf("commit custom profile avatar read: %w", err)
	}
	return image, nil
}

func (s *Service) updateAvatar(
	ctx context.Context,
	tx pgx.Tx,
	principal auth.Principal,
	profileID string,
	presetID string,
	imageData []byte,
) (Profile, error) {
	profileID = strings.TrimSpace(profileID)
	authorized, err := auth.AuthorizeAndLockProfiles(ctx, tx, principal, []string{profileID}, true)
	if err != nil {
		if imageData != nil {
			return Profile{}, fmt.Errorf("authorize custom profile avatar update: %w", err)
		}
		return Profile{}, fmt.Errorf("authorize profile avatar update: %w", err)
	}
	if !authorized {
		return Profile{}, ErrNotFound
	}
	if imageData == nil {
		if _, err := tx.Exec(ctx, `DELETE FROM profile_avatar_images WHERE profile_id = $1::uuid`, profileID); err != nil {
			return Profile{}, fmt.Errorf("remove custom profile avatar: %w", err)
		}
		if _, err := tx.Exec(ctx, `UPDATE profiles SET avatar_preset = $2, updated_at = now() WHERE id = $1::uuid`, profileID, presetID); err != nil {
			return Profile{}, fmt.Errorf("update profile avatar preset: %w", err)
		}
	} else {
		if _, err := tx.Exec(ctx, `
			INSERT INTO profile_avatar_images (profile_id, content_type, image_data)
			VALUES ($1::uuid, 'image/png', $2)
			ON CONFLICT (profile_id) DO UPDATE
			SET content_type = EXCLUDED.content_type, image_data = EXCLUDED.image_data, updated_at = now()
		`, profileID, imageData); err != nil {
			return Profile{}, fmt.Errorf("store custom profile avatar: %w", err)
		}
		if _, err := tx.Exec(ctx, `UPDATE profiles SET updated_at = now() WHERE id = $1::uuid`, profileID); err != nil {
			return Profile{}, fmt.Errorf("touch profile after avatar update: %w", err)
		}
	}

	var profile Profile
	var custom bool
	err = tx.QueryRow(ctx, `
		SELECT profile.id::text, profile.category_id::text, category.name, category.color, category.icon,
		       profile.name, profile.description, profile.is_child, profile.pin_hash IS NOT NULL,
		       profile.avatar_preset, EXISTS (
		           SELECT 1 FROM profile_avatar_images avatar WHERE avatar.profile_id = profile.id
		       ),
		       profile.enabled, profile.available_from::text, profile.available_until::text,
		       to_char(profile.access_start_time, 'HH24:MI'), to_char(profile.access_end_time, 'HH24:MI'),
		       profile.access_timezone
		FROM profiles profile
		JOIN access_categories category ON category.id = profile.category_id
		WHERE profile.id::text = $1
	`, profileID).Scan(
		&profile.ID, &profile.CategoryID, &profile.CategoryName, &profile.CategoryColor, &profile.CategoryIcon,
		&profile.Name, &profile.Description, &profile.IsChild, &profile.HasPIN, &profile.AvatarPreset, &custom,
		&profile.Enabled, &profile.AvailableFrom, &profile.AvailableUntil, &profile.AccessStartTime,
		&profile.AccessEndTime, &profile.AccessTimezone,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return Profile{}, ErrNotFound
	}
	if err != nil {
		return Profile{}, fmt.Errorf("query profile avatar update: %w", err)
	}
	profile.CanManage = true
	profile.AccessTimezone = s.runtimeTimezone(ctx)
	profile.Accessible = profileAccessible(profile, time.Now().UTC())
	profile.AvatarKind = "preset"
	if custom {
		profile.AvatarKind = "custom"
	}
	return profile, nil
}

func avatarPresetByID(presetID string) (avatarPresetDefinition, bool) {
	for _, definition := range avatarPresetDefinitions {
		if definition.ID == presetID {
			return definition, true
		}
	}
	return avatarPresetDefinition{}, false
}

func clamp(value, minimum, maximum int) int {
	if value < minimum {
		return minimum
	}
	if value > maximum {
		return maximum
	}
	return value
}

func bilinear(topLeft, topRight, bottomLeft, bottomRight uint8, xWeight, yWeight float64) uint8 {
	top := float64(topLeft)*(1-xWeight) + float64(topRight)*xWeight
	bottom := float64(bottomLeft)*(1-xWeight) + float64(bottomRight)*xWeight
	return uint8(math.Round(top*(1-yWeight) + bottom*yWeight))
}
