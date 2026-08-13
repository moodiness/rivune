package playback

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

const (
	hardwareProbeTimeout         = 10 * time.Second
	softwareToneMapMaximumHeight = 1080
	softwareToneMapFilter        = "zscale=t=linear:npl=100,format=gbrpf32le,zscale=p=bt709,tonemap=hable:desat=0,zscale=t=bt709:m=bt709:r=tv,format=yuv420p"
	// Each decode fixture contains one generated black intra frame. HEVC Main 10
	// remains separate because the hybrid probe also verifies download/readback.
	hybridProbeHEVCBase64     = "AAAAAUABDAH//wIgAAADAJAAAAMAAAMAHpWUCQAAAAFCAQECIAAAAwCQAAADAAADAB6gECAgTZZWVKTC8BahIgEggAAAAwCAAAADAIQAAAABRAHAc8GJAAABKAGsTISUSMr///vIFv4S4tBWpZBLiAR6ZJpAB8tP3AAAAAECAdAJeIJkSsCfqwloDDgPuA=="
	decodeProbeH264Base64     = "AAAAAWdCwAraEJsBEAAAAwAQAAADACjxImoAAAABaM4PyAAAAWWIhDomKAAJAsnJyddddddddddddeA="
	decodeProbeHEVCMainBase64 = "AAAAAUABDAH//wFgAAADAJAAAAMAAAMAHpWUCQAAAAFCAQEBYAAAAwCQAAADAAADAB6gIIEFllZUpMLwFoCAAAADAIAAAAMAhAAAAAFEAcBzwIkAAAEoAaxO1x//9d6cr+r4"
	decodeProbeAV1Base64      = "REtJRgAAIABBVjAxQABAAAEAAAABAAAA/////wAAAAAYAAAAAAAAAAAAAAASAAoGGBV//aAIMgwYAAAAUAAACgV3ZIA="
)

type FFmpegOptions struct {
	HardwareAcceleration string
	VideoDevice          string
	MaximumReadRate      float64
	PreferredVideoCodec  string
	QualityPreset        string
	Logger               *slog.Logger
}

type videoEncoderKind string

type videoToneMapBackend string

const (
	videoEncoderSoftware videoEncoderKind = "software"
	videoEncoderVAAPI    videoEncoderKind = "vaapi"
	videoEncoderQSV      videoEncoderKind = "qsv"
	videoEncoderNVENC    videoEncoderKind = "nvenc"
	videoEncoderAMF      videoEncoderKind = "amf"

	videoToneMapSoftware videoToneMapBackend = "software"
	videoToneMapHybrid   videoToneMapBackend = "hybrid"
	videoToneMapVAAPI    videoToneMapBackend = "vaapi"
	videoToneMapVulkan   videoToneMapBackend = "vulkan"
)

var transcodeVideoCodecs = [...]string{"h264", "hevc", "av1"}

type TranscodeCapabilities struct {
	HardwareAcceleration string   `json:"hardwareAcceleration"`
	DecodeCodecs         []string `json:"decodeCodecs"`
	EncodeCodecs         []string `json:"encodeCodecs"`
	HEVCMain10           bool     `json:"hevcMain10"`
	PreferredVideoCodec  string   `json:"preferredVideoCodec"`
	QualityPreset        string   `json:"qualityPreset"`
}

type videoEncoder struct {
	kind           videoEncoderKind
	device         string
	toneMapBackend videoToneMapBackend
	decodeCodecs   map[string]bool
	hevcMain10     bool
	encodeCodecs   map[string]bool
}

func detectVideoEncoder(ffmpegPath string, options FFmpegOptions) (videoEncoder, error) {
	mode := strings.ToLower(strings.TrimSpace(options.HardwareAcceleration))
	encodeProbe := func(candidate videoEncoder, codec string, toneMap bool) error {
		return probeVideoEncoderCodec(ffmpegPath, candidate, codec, toneMap)
	}
	decodeProbe := func(candidate videoEncoder, codec string) error {
		return probeVideoDecoderCodec(ffmpegPath, candidate, codec)
	}
	main10Probe := func(candidate videoEncoder) error {
		return probeVideoEncoderMain10(ffmpegPath, candidate)
	}
	if mode == "" || mode == "auto" {
		for _, candidate := range automaticVideoEncoders(options.VideoDevice) {
			candidate = detectVideoEncoderCapabilities(candidate, encodeProbe, decodeProbe)
			candidate = detectVideoEncoderMain10(candidate, main10Probe)
			if len(candidate.encodeCodecs) != 0 {
				return detectHardwareToneMap(ffmpegPath, candidate), nil
			}
		}
		software := detectVideoEncoderCapabilities(videoEncoder{kind: videoEncoderSoftware}, encodeProbe, decodeProbe)
		return detectVideoEncoderMain10(software, main10Probe), nil
	}
	return detectExplicitVideoEncoder(mode, options.VideoDevice, encodeProbe, decodeProbe, func(candidate videoEncoder) videoEncoder {
		return detectHardwareToneMap(ffmpegPath, candidate)
	}, main10Probe)
}

type videoEncoderProbe func(videoEncoder, string, bool) error
type videoDecoderProbe func(videoEncoder, string) error
type videoMain10Probe func(videoEncoder) error

func detectExplicitVideoEncoder(mode, device string, encodeProbe videoEncoderProbe, decodeProbe videoDecoderProbe, detectToneMap func(videoEncoder) videoEncoder, main10Probes ...videoMain10Probe) (videoEncoder, error) {
	candidate := videoEncoder{device: device}
	switch mode {
	case string(videoEncoderSoftware):
		candidate.kind = videoEncoderSoftware
		candidate.device = ""
	case string(videoToneMapHybrid):
		candidate.kind = videoEncoderVAAPI
		candidate.toneMapBackend = videoToneMapHybrid
	case string(videoEncoderVAAPI):
		candidate.kind = videoEncoderVAAPI
	case string(videoEncoderQSV):
		candidate.kind = videoEncoderQSV
	case string(videoEncoderNVENC):
		candidate.kind = videoEncoderNVENC
		candidate.device = ""
	case string(videoEncoderAMF):
		candidate.kind = videoEncoderAMF
		candidate.device = ""
	default:
		return videoEncoder{}, fmt.Errorf("unsupported hardware acceleration mode %q", mode)
	}

	hybridH264Ready := false
	if mode == string(videoToneMapHybrid) {
		probeCandidate := candidate.withEncodeCodec("h264").withDecodeCodec("hevc")
		if err := encodeProbe(probeCandidate, "h264", true); err != nil {
			return videoEncoder{}, fmt.Errorf("initialize %s video encoder: %w", mode, err)
		}
		hybridH264Ready = true
	}
	var lastProbeError error
	candidate = detectVideoEncoderCapabilities(candidate, func(candidate videoEncoder, codec string, toneMap bool) error {
		// The required hybrid probe already encoded H264 after Main10 decode,
		// CPU readback/tone mapping, and VAAPI upload. Reuse that stronger proof:
		// the generic lavfi upload probe fails on some AMD drivers.
		if hybridH264Ready && normalizedTranscodeCodec(codec) == "h264" {
			return nil
		}
		err := encodeProbe(candidate, codec, toneMap)
		if err != nil {
			lastProbeError = err
		}
		return err
	}, decodeProbe)
	if len(main10Probes) > 0 {
		candidate = detectVideoEncoderMain10(candidate, main10Probes[0])
	}
	if len(candidate.encodeCodecs) == 0 {
		if lastProbeError != nil {
			return videoEncoder{}, fmt.Errorf("initialize %s video encoder: %w", mode, lastProbeError)
		}
		return videoEncoder{}, fmt.Errorf("initialize %s video encoder: no functional H264, HEVC, or AV1 encoder", mode)
	}
	if mode == string(videoToneMapHybrid) || mode == string(videoEncoderSoftware) {
		return candidate, nil
	}
	return detectToneMap(candidate), nil
}

func detectVideoEncoderCapabilities(candidate videoEncoder, encodeProbe videoEncoderProbe, decodeProbe videoDecoderProbe) videoEncoder {
	candidate.encodeCodecs = make(map[string]bool, len(transcodeVideoCodecs))
	candidate.decodeCodecs = make(map[string]bool, len(transcodeVideoCodecs))
	for _, codec := range transcodeVideoCodecs {
		probeCandidate := candidate.withEncodeCodec(codec)
		if err := encodeProbe(probeCandidate, codec, false); err == nil {
			candidate.encodeCodecs[codec] = true
		}
	}
	for _, codec := range transcodeVideoCodecs {
		probeCandidate := candidate.withDecodeCodec(codec)
		if err := decodeProbe(probeCandidate, codec); err == nil {
			candidate.decodeCodecs[codec] = true
		}
	}
	return candidate
}

func detectVideoEncoderMain10(candidate videoEncoder, probe videoMain10Probe) videoEncoder {
	candidate.hevcMain10 = false
	if probe == nil {
		return candidate
	}
	probeCandidate := candidate.withDecodeCodec("hevc").withEncodeCodec("hevc")
	if err := probe(probeCandidate); err == nil {
		candidate.hevcMain10 = true
	}
	return candidate
}

func (encoder videoEncoder) withEncodeCodec(codec string) videoEncoder {
	codec = normalizedTranscodeCodec(codec)
	encoder.encodeCodecs = map[string]bool{codec: true}
	return encoder
}

func (encoder videoEncoder) withDecodeCodec(codec string) videoEncoder {
	codec = normalizedTranscodeCodec(codec)
	encoder.decodeCodecs = map[string]bool{codec: true}
	return encoder
}
func automaticVideoEncoderKinds(vendor string, nvidiaAvailable, renderDeviceAvailable bool) []videoEncoderKind {
	kinds := make([]videoEncoderKind, 0, 3)
	if nvidiaAvailable {
		kinds = append(kinds, videoEncoderNVENC)
	}
	if !renderDeviceAvailable {
		return kinds
	}
	switch strings.ToLower(strings.TrimSpace(vendor)) {
	case "0x1002":
		kinds = append(kinds, videoEncoderVAAPI)
	case "0x8086":
		kinds = append(kinds, videoEncoderQSV, videoEncoderVAAPI)
	default:
		kinds = append(kinds, videoEncoderQSV, videoEncoderVAAPI)
	}
	return kinds
}

func automaticWindowsVideoEncoderKinds() []videoEncoderKind {
	return []videoEncoderKind{videoEncoderAMF, videoEncoderQSV, videoEncoderNVENC}
}
func videoEncoderPlatformProbeError(kind videoEncoderKind, windows bool) error {
	if windows && kind == videoEncoderVAAPI {
		return fmt.Errorf("VAAPI is not available on Windows")
	}
	if !windows && kind == videoEncoderAMF {
		return fmt.Errorf("AMF is only available on Windows")
	}
	return nil
}

func probeVideoEncoder(ffmpegPath string, encoder videoEncoder, toneMap bool) error {
	return probeVideoEncoderCodec(ffmpegPath, encoder, "h264", toneMap)
}

func probeVideoEncoderCodec(ffmpegPath string, encoder videoEncoder, codec string, toneMap bool) error {
	if err := platformVideoEncoderProbeError(encoder.normalizedKind()); err != nil {
		return err
	}
	arguments, input, err := videoEncoderProbeArguments(encoder, codec, "balanced", toneMap)
	if err != nil {
		return err
	}
	probeContext, cancel := context.WithTimeout(context.Background(), hardwareProbeTimeout)
	defer cancel()
	command := exec.CommandContext(probeContext, ffmpegPath, arguments...)
	configureMediaCommand(command)
	if len(input) > 0 {
		command.Stdin = bytes.NewReader(input)
	}
	diagnostic := newDiagnosticBuffer()
	command.Stdout = nil
	command.Stderr = diagnostic
	if err := command.Run(); err != nil {
		if errors.Is(probeContext.Err(), context.DeadlineExceeded) {
			return errors.New("hardware encoder probe timed out")
		}
		return fmt.Errorf("hardware encoder probe: %w: %s", err, diagnostic.String())
	}
	return nil
}

func probeVideoDecoderCodec(ffmpegPath string, encoder videoEncoder, codec string) error {
	if err := platformVideoEncoderProbeError(encoder.normalizedKind()); err != nil {
		return err
	}
	arguments, input, err := videoDecoderProbeArguments(encoder, codec)
	if err != nil {
		return err
	}
	probeContext, cancel := context.WithTimeout(context.Background(), hardwareProbeTimeout)
	defer cancel()
	command := exec.CommandContext(probeContext, ffmpegPath, arguments...)
	configureMediaCommand(command)
	command.Stdin = bytes.NewReader(input)
	diagnostic := newDiagnosticBuffer()
	command.Stdout = nil
	command.Stderr = diagnostic
	if err := command.Run(); err != nil {
		if errors.Is(probeContext.Err(), context.DeadlineExceeded) {
			return errors.New("video decoder probe timed out")
		}
		return fmt.Errorf("video decoder probe: %w: %s", err, diagnostic.String())
	}
	return nil
}
func probeVideoEncoderMain10(ffmpegPath string, encoder videoEncoder) error {
	if err := platformVideoEncoderProbeError(encoder.normalizedKind()); err != nil {
		return err
	}
	arguments, input, err := videoEncoderMain10ProbeArguments(encoder)
	if err != nil {
		return err
	}
	probeContext, cancel := context.WithTimeout(context.Background(), hardwareProbeTimeout)
	defer cancel()
	command := exec.CommandContext(probeContext, ffmpegPath, arguments...)
	configureMediaCommand(command)
	command.Stdin = bytes.NewReader(input)
	diagnostic := newDiagnosticBuffer()
	command.Stdout = nil
	command.Stderr = diagnostic
	if err := command.Run(); err != nil {
		if errors.Is(probeContext.Err(), context.DeadlineExceeded) {
			return errors.New("hardware HEVC Main10 probe timed out")
		}
		return fmt.Errorf("hardware HEVC Main10 probe: %w: %s", err, diagnostic.String())
	}
	return nil
}

func videoDecoderProbeArguments(encoder videoEncoder, codec string) ([]string, []byte, error) {
	codec = normalizedTranscodeCodec(codec)
	var format, fixture string
	switch codec {
	case "h264":
		format, fixture = "h264", decodeProbeH264Base64
	case "hevc":
		format, fixture = "hevc", decodeProbeHEVCMainBase64
	case "av1":
		format, fixture = "ivf", decodeProbeAV1Base64
	default:
		return nil, nil, fmt.Errorf("unsupported decoder probe codec %q", codec)
	}
	input, err := base64.StdEncoding.DecodeString(fixture)
	if err != nil {
		return nil, nil, fmt.Errorf("decode %s decoder probe fixture: %w", codec, err)
	}
	asset := storedAsset{
		Kind: processingTranscode,
		Decision: &PlaybackDecision{Source: &PlaybackDecisionSource{
			VideoCodec: codec,
			Height:     64,
		}},
	}
	arguments := []string{"-nostdin", "-hide_banner", "-loglevel", "error"}
	if encoder.normalizedKind() != videoEncoderSoftware {
		arguments = append(arguments, encoder.globalArguments()...)
		decodeArguments := encoder.hardwareDecodeArguments(asset)
		if len(decodeArguments) == 0 {
			return nil, nil, fmt.Errorf("%s decoder probe is unavailable for %s", encoder.normalizedKind(), codec)
		}
		arguments = append(arguments, decodeArguments...)
	}
	arguments = append(arguments, "-f", format, "-i", "pipe:0", "-map", "0:v:0", "-frames:v", "1", "-an", "-f", "null", "-")
	return arguments, input, nil
}
func videoEncoderMain10ProbeArguments(encoder videoEncoder) ([]string, []byte, error) {
	input, err := base64.StdEncoding.DecodeString(hybridProbeHEVCBase64)
	if err != nil {
		return nil, nil, fmt.Errorf("decode HEVC Main10 probe fixture: %w", err)
	}
	asset := storedAsset{
		Kind:          processingTranscode,
		VideoBitDepth: 10,
		Decision: &PlaybackDecision{
			Source: &PlaybackDecisionSource{VideoCodec: "hevc", Height: 128},
			Target: &PlaybackDecisionTarget{VideoCodec: "hevc", Height: 128, VideoBitDepth: 10},
		},
	}
	arguments := []string{"-nostdin", "-hide_banner", "-loglevel", "error"}
	if encoder.normalizedKind() != videoEncoderSoftware {
		arguments = append(arguments, encoder.globalArguments()...)
		decodeArguments := encoder.hardwareDecodeArguments(asset)
		if len(decodeArguments) == 0 {
			return nil, nil, fmt.Errorf("%s HEVC Main10 hardware decoder path is unavailable", encoder.normalizedKind())
		}
		arguments = append(arguments, decodeArguments...)
	}
	arguments = append(arguments, "-f", "hevc", "-i", "pipe:0", "-map", "0:v:0")
	codecArguments, err := encoder.codecArguments("hevc", "balanced", 1, true)
	if err != nil {
		return nil, nil, err
	}
	arguments = append(arguments, codecArguments...)
	arguments = append(arguments, "-frames:v", "1", "-an", "-f", "null", "-")
	return arguments, input, nil
}

func videoEncoderProbeArguments(encoder videoEncoder, codec, quality string, toneMap bool) ([]string, []byte, error) {
	arguments := []string{"-hide_banner", "-loglevel", "error"}
	if !toneMap || encoder.toneMapBackend != videoToneMapHybrid {
		arguments = append([]string{"-nostdin"}, arguments...)
	}
	arguments = append(arguments, encoder.globalArguments()...)
	var input []byte
	if toneMap && encoder.toneMapBackend == videoToneMapHybrid {
		var err error
		input, err = base64.StdEncoding.DecodeString(hybridProbeHEVCBase64)
		if err != nil {
			return nil, nil, fmt.Errorf("decode hybrid video probe: %w", err)
		}
		asset := storedAsset{
			Kind:          processingTranscode,
			ToneMap:       true,
			VideoBitDepth: 10,
			Decision: &PlaybackDecision{Source: &PlaybackDecisionSource{
				VideoCodec: "h265",
				Height:     128,
			}},
		}
		arguments = append(arguments, encoder.hardwareDecodeArguments(asset)...)
		arguments = append(arguments, "-f", "hevc", "-i", "pipe:0", "-vf", encoder.hybridToneMapFilter(asset))
	} else {
		arguments = append(arguments, "-f", "lavfi", "-i", "color=c=black:s=256x256:r=1")
		if filter := encoder.filter(toneMap); filter != "" {
			arguments = append(arguments, "-vf", filter)
		}
	}
	codecArguments, err := encoder.codecArguments(codec, quality, 1, false)
	if err != nil {
		return nil, nil, err
	}
	arguments = append(arguments, codecArguments...)
	arguments = append(arguments, "-frames:v", "1", "-an", "-f", "null", "-")
	return arguments, input, nil
}

func detectHardwareToneMap(ffmpegPath string, encoder videoEncoder) videoEncoder {
	return detectHardwareToneMapWithProbe(encoder, videoDeviceVendor(encoder.device), func(candidate videoEncoder) error {
		return probeVideoEncoder(ffmpegPath, candidate, true)
	})
}

func detectHardwareToneMapWithProbe(encoder videoEncoder, vendor string, probe func(videoEncoder) error) videoEncoder {
	if encoder.normalizedKind() != videoEncoderVAAPI {
		return encoder
	}
	backends := [...]videoToneMapBackend{videoToneMapVAAPI, videoToneMapVulkan}
	if strings.EqualFold(strings.TrimSpace(vendor), "0x1002") {
		backends[0], backends[1] = backends[1], backends[0]
	}
	for _, backend := range backends {
		candidate := encoder
		candidate.toneMapBackend = backend
		if err := probe(candidate); err == nil {
			return candidate
		}
	}
	return encoder
}

func (encoder videoEncoder) normalizedKind() videoEncoderKind {
	if encoder.kind == "" {
		return videoEncoderSoftware
	}
	return encoder.kind
}
func normalizedTranscodeCodec(codec string) string {
	codec = canonicalTargetVideoCodec(codec)
	if codec == "auto" {
		return ""
	}
	return codec
}

func (encoder videoEncoder) supportsEncode(codec string) bool {
	codec = normalizedTranscodeCodec(codec)
	if codec == "" {
		return false
	}
	if encoder.encodeCodecs == nil {
		return encoder.normalizedKind() == videoEncoderSoftware || codec == "h264"
	}
	return encoder.encodeCodecs[codec]
}

func (encoder videoEncoder) supportsDecode(codec string) bool {
	codec = normalizedTranscodeCodec(codec)
	if codec == "" {
		return false
	}
	if encoder.decodeCodecs == nil {
		return false
	}
	return encoder.decodeCodecs[codec]
}

func (encoder videoEncoder) supportedEncodeCodecs() []string {
	return encoder.supportedCodecs(encoder.supportsEncode)
}

func (encoder videoEncoder) supportedDecodeCodecs() []string {
	return encoder.supportedCodecs(encoder.supportsDecode)
}

func (encoder videoEncoder) supportedCodecs(supports func(string) bool) []string {
	codecs := make([]string, 0, len(transcodeVideoCodecs))
	for _, codec := range transcodeVideoCodecs {
		if supports(codec) {
			codecs = append(codecs, codec)
		}
	}
	return codecs
}

func (encoder videoEncoder) transcodeCapabilities(preferred, quality string) TranscodeCapabilities {
	preferred = strings.ToLower(strings.TrimSpace(preferred))
	if preferred == "h265" {
		preferred = "hevc"
	}
	if preferred != "auto" && normalizedTranscodeCodec(preferred) == "" {
		preferred = "auto"
	}
	if preferred == "" {
		preferred = "auto"
	}
	quality = normalizedTranscodeQuality(quality)
	hardwareAcceleration := string(encoder.normalizedKind())
	if encoder.toneMapBackend == videoToneMapHybrid {
		hardwareAcceleration = string(videoToneMapHybrid)
	}
	return TranscodeCapabilities{
		HardwareAcceleration: hardwareAcceleration,
		DecodeCodecs:         encoder.supportedDecodeCodecs(),
		EncodeCodecs:         encoder.supportedEncodeCodecs(),
		HEVCMain10:           encoder.hevcMain10,
		PreferredVideoCodec:  preferred,
		QualityPreset:        quality,
	}
}

func normalizedTranscodeQuality(quality string) string {
	switch strings.ToLower(strings.TrimSpace(quality)) {
	case "speed":
		return "speed"
	case "quality":
		return "quality"
	default:
		return "balanced"
	}
}

func (encoder videoEncoder) globalArguments() []string {
	return videoEncoderGlobalArguments(encoder, windowsMediaPlatform)
}

func videoEncoderGlobalArguments(encoder videoEncoder, windows bool) []string {
	switch encoder.normalizedKind() {
	case videoEncoderVAAPI:
		if encoder.toneMapBackend == videoToneMapVulkan {
			return []string{
				"-init_hw_device", "drm=dr:" + encoder.device,
				"-init_hw_device", "vaapi=hw@dr",
				"-init_hw_device", "vulkan=vk@dr",
				"-filter_hw_device", "hw",
			}
		}
		return []string{"-init_hw_device", "vaapi=hw:" + encoder.device, "-filter_hw_device", "hw"}
	case videoEncoderQSV:
		if windows {
			return []string{"-init_hw_device", "qsv=hw:hw,child_device_type=d3d11va", "-filter_hw_device", "hw"}
		}
		return []string{"-init_hw_device", "vaapi=va:" + encoder.device, "-init_hw_device", "qsv=hw@va", "-filter_hw_device", "hw"}
	case videoEncoderAMF:
		if windows {
			return []string{"-init_hw_device", "d3d11va=hw", "-filter_hw_device", "hw"}
		}
	}
	return nil
}

func (encoder videoEncoder) usesHardwareToneMap() bool {
	return encoder.toneMapBackend == videoToneMapVAAPI || encoder.toneMapBackend == videoToneMapVulkan
}

func (encoder videoEncoder) normalizedToneMapBackend() videoToneMapBackend {
	if encoder.toneMapBackend == videoToneMapHybrid || encoder.usesHardwareToneMap() {
		return encoder.toneMapBackend
	}
	return videoToneMapSoftware
}

func (encoder videoEncoder) hardwareDecodeSafe(asset storedAsset) bool {
	if asset.Kind != processingTranscode || asset.SubtitleTrackIndex != nil ||
		encoder.normalizedKind() == videoEncoderSoftware || asset.Decision == nil || asset.Decision.Source == nil {
		return false
	}
	codec := normalizedTranscodeCodec(asset.Decision.Source.VideoCodec)
	main10Path := codec == "hevc" && asset.VideoBitDepth > 8 && targetVideoBitDepth(asset) >= 10 && encoder.hevcMain10
	if !encoder.supportsDecode(codec) && !main10Path {
		return false
	}
	if asset.VideoBitDepth > 8 && targetVideoBitDepth(asset) <= 8 && !asset.ToneMap {
		return false
	}
	if asset.ToneMap && !encoder.usesHardwareToneMap() {
		return encoder.normalizedKind() == videoEncoderVAAPI && codec == "hevc" && asset.VideoBitDepth == 10
	}
	needsScale := asset.TargetHeight > 0 && asset.Decision.Source.Height > asset.TargetHeight
	return !needsScale || encoder.normalizedKind() == videoEncoderVAAPI || encoder.normalizedKind() == videoEncoderQSV
}

// hardwareFramesSafe is deliberately conservative: these decoder, filter,
// and encoder paths share a documented hardware frame type. Subtitle
// composition and software tone mapping require CPU frames.
func (encoder videoEncoder) hardwareFramesSafe(asset storedAsset) bool {
	return encoder.hardwareDecodeSafe(asset) && (!asset.ToneMap || encoder.usesHardwareToneMap())
}

func (encoder videoEncoder) hardwareDecodeArguments(asset storedAsset) []string {
	if !encoder.hardwareDecodeSafe(asset) {
		return nil
	}
	switch encoder.normalizedKind() {
	case videoEncoderVAAPI:
		return []string{"-hwaccel", "vaapi", "-hwaccel_device", "hw", "-hwaccel_output_format", "vaapi"}
	case videoEncoderQSV:
		return []string{"-hwaccel", "qsv", "-hwaccel_device", "hw", "-hwaccel_output_format", "qsv"}
	case videoEncoderNVENC:
		return []string{"-hwaccel", "cuda", "-hwaccel_output_format", "cuda"}
	case videoEncoderAMF:
		return []string{"-hwaccel", "d3d11va", "-hwaccel_device", "hw", "-hwaccel_output_format", "d3d11"}
	default:
		return nil
	}
}

func (encoder videoEncoder) hybridToneMapFilter(asset storedAsset) string {
	if !asset.ToneMap || encoder.hardwareFramesSafe(asset) || !encoder.hardwareDecodeSafe(asset) {
		return ""
	}
	filters := []string{"hwdownload", "format=p010le"}
	if asset.TargetHeight > 0 && asset.Decision != nil && asset.Decision.Source != nil && asset.Decision.Source.Height > asset.TargetHeight {
		filters = append(filters, "scale=-2:"+strconv.Itoa(asset.TargetHeight))
	}
	filters = append(filters, softwareToneMapFilter, "format=nv12", "hwupload")
	return strings.Join(filters, ",")
}

func (encoder videoEncoder) hardwareFilter(asset storedAsset) string {
	if !encoder.hardwareFramesSafe(asset) {
		return ""
	}
	if asset.ToneMap && encoder.toneMapBackend == videoToneMapVulkan {
		height := 0
		if asset.TargetHeight > 0 && asset.Decision.Source.Height > asset.TargetHeight {
			height = asset.TargetHeight
		}
		return vulkanToneMapFilter(height)
	}
	filters := make([]string, 0, 2)
	if asset.ToneMap {
		filters = append(filters, "tonemap_vaapi=format=nv12:matrix=bt709:primaries=bt709:transfer=bt709")
	}
	if asset.TargetHeight > 0 && asset.Decision.Source.Height > asset.TargetHeight {
		height := strconv.Itoa(asset.TargetHeight)
		switch encoder.normalizedKind() {
		case videoEncoderVAAPI:
			filters = append(filters, "scale_vaapi=w=-2:h="+height+":format=nv12")
		case videoEncoderQSV:
			filters = append(filters, "scale_qsv=w=-2:h="+height+":format=nv12")
		}
	}
	return strings.Join(filters, ",")
}

func vulkanToneMapFilter(targetHeight int) string {
	libplacebo := "libplacebo=upscaler=none:downscaler=none:format=nv12:tonemapping=bt.2390:peak_detect=false:color_primaries=bt709:color_trc=bt709:colorspace=bt709:range=tv"
	if targetHeight > 0 {
		libplacebo += ":w=-2:h=" + strconv.Itoa(targetHeight)
	}
	return strings.Join([]string{
		"hwmap=derive_device=vulkan:mode=read+direct",
		"format=vulkan",
		libplacebo,
		"hwmap=derive_device=vaapi:mode=read+direct",
		"format=vaapi",
	}, ",")
}

func (encoder videoEncoder) filter(toneMap bool) string {
	if toneMap && encoder.normalizedKind() == videoEncoderVAAPI {
		switch encoder.toneMapBackend {
		case videoToneMapVAAPI:
			return "format=p010,hwupload,tonemap_vaapi=format=nv12:matrix=bt709:primaries=bt709:transfer=bt709"
		case videoToneMapVulkan:
			return "format=p010,setparams=range=tv:color_primaries=bt2020:color_trc=smpte2084:colorspace=bt2020nc,hwupload," + vulkanToneMapFilter(0)
		}
	}
	filter := ""
	if toneMap {
		filter = softwareToneMapFilter
	}
	switch encoder.normalizedKind() {
	case videoEncoderVAAPI:
		if filter != "" {
			filter += ","
		}
		return filter + "format=nv12,hwupload"
	case videoEncoderQSV:
		if filter != "" {
			filter += ","
		}
		return filter + "format=nv12,hwupload=extra_hw_frames=64"
	default:
		return filter
	}
}

func (encoder videoEncoder) codecArguments(codec, quality string, threads int, main10 bool) ([]string, error) {
	codec = normalizedTranscodeCodec(codec)
	if codec == "" {
		return nil, fmt.Errorf("unsupported transcode video codec")
	}
	if !encoder.supportsEncode(codec) {
		return nil, fmt.Errorf("%s video encoder does not support %s", encoder.normalizedKind(), codec)
	}
	quality = normalizedTranscodeQuality(quality)
	profile := "main"
	if codec == "h264" {
		profile = "high"
	} else if codec == "hevc" && main10 {
		profile = "main10"
	}
	encoderName := ""
	switch encoder.normalizedKind() {
	case videoEncoderVAAPI:
		encoderName = codec + "_vaapi"
		qualityLevel := "4"
		if quality == "speed" {
			qualityLevel = "7"
		} else if quality == "quality" {
			qualityLevel = "1"
		}
		return []string{"-c:v", encoderName, "-profile:v", profile, "-quality", qualityLevel}, nil
	case videoEncoderQSV:
		encoderName = codec + "_qsv"
		preset := "medium"
		if quality == "speed" {
			preset = "veryfast"
		} else if quality == "quality" {
			preset = "slow"
		}
		return []string{"-c:v", encoderName, "-profile:v", profile, "-preset", preset, "-look_ahead", "0"}, nil
	case videoEncoderNVENC:
		encoderName = codec + "_nvenc"
		preset := "p4"
		if quality == "speed" {
			preset = "p2"
		} else if quality == "quality" {
			preset = "p6"
		}
		arguments := make([]string, 0, 14)
		arguments = append(arguments, "-c:v", encoderName)
		// FFmpeg's AV1 NVENC wrapper exposes only the Main profile and does not
		// accept the profile option on every supported FFmpeg release.
		if codec != "av1" {
			arguments = append(arguments, "-profile:v", profile)
		}
		return append(arguments, "-preset", preset, "-tune", "ll", "-rc", "vbr", "-spatial_aq", "1", "-zerolatency", "1"), nil
	case videoEncoderAMF:
		encoderName = codec + "_amf"
		return []string{"-c:v", encoderName, "-profile:v", profile, "-quality", quality}, nil
	default:
		if threads < 1 {
			return nil, fmt.Errorf("positive software transcode thread count is required")
		}
		preset, crf := "superfast", "18"
		if quality == "speed" {
			preset, crf = "ultrafast", "23"
		} else if quality == "quality" {
			preset, crf = "medium", "16"
		}
		switch codec {
		case "h264":
			encoderName = "libx264"
		case "hevc":
			encoderName = "libx265"
			if quality == "speed" {
				crf = "28"
			} else if quality == "balanced" {
				crf = "23"
			} else {
				crf = "19"
			}
		case "av1":
			encoderName = "libsvtav1"
			if quality == "speed" {
				preset, crf = "10", "35"
			} else if quality == "balanced" {
				preset, crf = "8", "30"
			} else {
				preset, crf = "6", "25"
			}
		}
		pixelFormat := "yuv420p"
		arguments := []string{"-threads", strconv.Itoa(threads), "-c:v", encoderName}
		if codec == "hevc" && main10 {
			pixelFormat = "yuv420p10le"
			arguments = append(arguments, "-profile:v", profile)
		}
		arguments = append(arguments, "-preset", preset, "-crf", crf, "-pix_fmt", pixelFormat)
		if codec != "av1" {
			arguments = append(arguments, "-tune", "zerolatency")
		}
		return arguments, nil
	}
}
