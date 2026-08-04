package profile

import (
	"bytes"
	"encoding/binary"
	"encoding/xml"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"testing"
)

func TestAvatarPresetsAreUniqueValidSVGs(t *testing.T) {
	presets := AvatarPresets()
	if len(presets) < 16 {
		t.Fatalf("expected at least 16 presets, got %d", len(presets))
	}
	seen := make(map[string]struct{}, len(presets))
	for _, preset := range presets {
		if preset.ID == "" || preset.Name == "" {
			t.Fatalf("invalid preset: %+v", preset)
		}
		if _, duplicate := seen[preset.ID]; duplicate {
			t.Fatalf("duplicate preset ID %q", preset.ID)
		}
		seen[preset.ID] = struct{}{}
		svg, found := AvatarPresetSVG(preset.ID)
		if !found {
			t.Fatalf("preset %q has no image", preset.ID)
		}
		var document any
		if err := xml.Unmarshal(svg, &document); err != nil {
			t.Fatalf("preset %q is invalid SVG XML: %v", preset.ID, err)
		}
	}
	if _, found := AvatarPresetSVG("missing"); found {
		t.Fatal("unknown preset unexpectedly resolved")
	}
}

func TestNormalizeAvatarImageCenterCropsAndResizesPNG(t *testing.T) {
	source := image.NewNRGBA(image.Rect(0, 0, 800, 400))
	for y := 0; y < 400; y++ {
		for x := 0; x < 800; x++ {
			value := color.NRGBA{R: 20, G: 180, B: 90, A: 255}
			if x < 200 || x >= 600 {
				value = color.NRGBA{R: 240, G: 20, B: 20, A: 255}
			}
			source.SetNRGBA(x, y, value)
		}
	}
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, source); err != nil {
		t.Fatalf("encode input: %v", err)
	}
	normalized, err := NormalizeAvatarImage(encoded.Bytes())
	if err != nil {
		t.Fatalf("normalize image: %v", err)
	}
	result, format, err := image.Decode(bytes.NewReader(normalized))
	if err != nil {
		t.Fatalf("decode normalized image: %v", err)
	}
	if format != "png" || result.Bounds().Dx() != avatarOutputSize || result.Bounds().Dy() != avatarOutputSize {
		t.Fatalf("unexpected normalized image: format=%q bounds=%v", format, result.Bounds())
	}
	center := color.NRGBAModel.Convert(result.At(256, 256)).(color.NRGBA)
	if center.G < 150 || center.R > 50 {
		t.Fatalf("center crop did not preserve central image content: %+v", center)
	}
}

func TestNormalizeAvatarImageAcceptsJPEGAndStripsOriginalEncoding(t *testing.T) {
	source := image.NewRGBA(image.Rect(0, 0, 128, 192))
	for y := 0; y < 192; y++ {
		for x := 0; x < 128; x++ {
			source.Set(x, y, color.RGBA{R: 50, G: 80, B: 210, A: 255})
		}
	}
	var encoded bytes.Buffer
	if err := jpeg.Encode(&encoded, source, &jpeg.Options{Quality: 90}); err != nil {
		t.Fatalf("encode JPEG: %v", err)
	}
	normalized, err := NormalizeAvatarImage(encoded.Bytes())
	if err != nil {
		t.Fatalf("normalize JPEG: %v", err)
	}
	if !bytes.HasPrefix(normalized, []byte{0x89, 'P', 'N', 'G'}) {
		t.Fatal("normalized JPEG was not re-encoded as PNG")
	}
}

func TestNormalizeAvatarImageRejectsUnsafeInputs(t *testing.T) {
	if _, err := NormalizeAvatarImage([]byte("not an image")); err == nil {
		t.Fatal("invalid bytes were accepted")
	}
	small := image.NewRGBA(image.Rect(0, 0, 32, 32))
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, small); err != nil {
		t.Fatalf("encode small image: %v", err)
	}
	if _, err := NormalizeAvatarImage(encoded.Bytes()); err == nil {
		t.Fatal("undersized image was accepted")
	}
	if _, err := NormalizeAvatarImage(make([]byte, (2<<20)+1)); err == nil {
		t.Fatal("oversized image was accepted")
	}
}
func TestNormalizeAvatarImageAppliesJPEGEXIFOrientation(t *testing.T) {
	source := image.NewRGBA(image.Rect(0, 0, 80, 120))
	for y := 0; y < 120; y++ {
		value := color.RGBA{R: 230, G: 20, B: 20, A: 255}
		if y >= 60 {
			value = color.RGBA{R: 20, G: 40, B: 230, A: 255}
		}
		for x := 0; x < 80; x++ {
			source.SetRGBA(x, y, value)
		}
	}
	var encoded bytes.Buffer
	if err := jpeg.Encode(&encoded, source, &jpeg.Options{Quality: 100}); err != nil {
		t.Fatalf("encode JPEG: %v", err)
	}
	exif := []byte{
		0xff, 0xe1, 0, 0,
		'E', 'x', 'i', 'f', 0, 0,
		'I', 'I', 42, 0,
		8, 0, 0, 0,
		1, 0,
		0x12, 0x01, 3, 0, 1, 0, 0, 0, 6, 0, 0, 0,
		0, 0, 0, 0,
	}
	binary.BigEndian.PutUint16(exif[2:4], uint16(len(exif)-2))
	orientedJPEG := append([]byte{0xff, 0xd8}, exif...)
	orientedJPEG = append(orientedJPEG, encoded.Bytes()[2:]...)

	normalized, err := NormalizeAvatarImage(orientedJPEG)
	if err != nil {
		t.Fatalf("normalize oriented JPEG: %v", err)
	}
	result, err := png.Decode(bytes.NewReader(normalized))
	if err != nil {
		t.Fatalf("decode normalized image: %v", err)
	}
	left := color.NRGBAModel.Convert(result.At(20, 256)).(color.NRGBA)
	right := color.NRGBAModel.Convert(result.At(492, 256)).(color.NRGBA)
	if left.B < 180 || left.R > 80 || right.R < 180 || right.B > 80 {
		t.Fatalf("EXIF orientation was not applied: left=%+v right=%+v", left, right)
	}
}
