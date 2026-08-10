package playback

import (
	"bytes"
	"io"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const (
	ffmpegProgressFilename              = ".ffmpeg-progress"
	maximumFFmpegProgressBytes          = 64 << 10
	maximumFFmpegProgressStartupBytes   = 16 << 10
	maximumFFmpegProgressLineBytes      = 4 << 10
	maximumFFmpegProgressSeconds        = float64(100 * 366 * 24 * 60 * 60)
	maximumFFmpegProgressSpeed          = 1_000_000
	maximumFFmpegStartupDurationSeconds = float64(7 * 24 * 60 * 60)
)

type ffmpegProgress struct {
	encodedSeconds         float64
	speed                  float64
	startupDurationSeconds float64
	state                  string
	hasEncodedSeconds      bool
	hasSpeed               bool
	hasStartupDuration     bool
}

type ffmpegProgressSample struct {
	outTimeMicroseconds int64
	outTimeMilliseconds int64
	outTimeSeconds      float64
	speed               float64
	hasOutTimeUS        bool
	hasOutTimeMS        bool
	hasOutTime          bool
	hasSpeed            bool
}

func readFFmpegProgress(directory string) (ffmpegProgress, bool) {
	file, err := os.Open(filepath.Join(directory, ffmpegProgressFilename))
	if err != nil {
		return ffmpegProgress{}, false
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || info.Size() <= 0 {
		return ffmpegProgress{}, false
	}
	length := info.Size()
	if length > maximumFFmpegProgressBytes {
		length = maximumFFmpegProgressBytes
	}
	offset := info.Size() - length
	contents := make([]byte, int(length))
	read, err := file.ReadAt(contents, offset)
	if err != nil && err != io.EOF {
		return ffmpegProgress{}, false
	}
	contents = contents[:read]
	if offset > 0 {
		lineEnd := bytes.IndexByte(contents, '\n')
		if lineEnd < 0 {
			return ffmpegProgress{}, false
		}
		contents = contents[lineEnd+1:]
	}
	latest, found := parseFFmpegProgress(contents)
	if !found || offset == 0 {
		return latest, found
	}

	startupLength := info.Size()
	if startupLength > maximumFFmpegProgressStartupBytes {
		startupLength = maximumFFmpegProgressStartupBytes
	}
	startupContents := make([]byte, int(startupLength))
	startupRead, startupErr := file.ReadAt(startupContents, 0)
	if startupErr != nil && startupErr != io.EOF {
		return latest, true
	}
	startup, startupFound := parseFFmpegProgress(startupContents[:startupRead])
	if startupFound && startup.hasStartupDuration {
		latest.startupDurationSeconds = startup.startupDurationSeconds
		latest.hasStartupDuration = true
	}
	return latest, true
}

func parseFFmpegProgress(contents []byte) (ffmpegProgress, bool) {
	lastLineEnd := bytes.LastIndexByte(contents, '\n')
	if lastLineEnd < 0 {
		return ffmpegProgress{}, false
	}
	contents = contents[:lastLineEnd+1]
	var current ffmpegProgressSample
	var latest ffmpegProgress
	var startupDurationSeconds float64
	found := false
	hasStartupDuration := false
	for len(contents) > 0 {
		lineEnd := bytes.IndexByte(contents, '\n')
		line := contents[:lineEnd]
		contents = contents[lineEnd+1:]
		if len(line) > 0 && line[len(line)-1] == '\r' {
			line = line[:len(line)-1]
		}
		if len(line) == 0 || len(line) > maximumFFmpegProgressLineBytes {
			continue
		}
		key, rawValue, ok := bytes.Cut(line, []byte{'='})
		if !ok {
			continue
		}
		value := strings.TrimSpace(string(rawValue))
		switch string(key) {
		case "out_time_us":
			current.hasOutTimeUS = false
			if parsed, ok := parseFFmpegProgressMicroseconds(value); ok {
				current.outTimeMicroseconds, current.hasOutTimeUS = parsed, true
			}
		case "out_time_ms":
			// Despite its historical name, FFmpeg reports this field in microseconds.
			current.hasOutTimeMS = false
			if parsed, ok := parseFFmpegProgressMicroseconds(value); ok {
				current.outTimeMilliseconds, current.hasOutTimeMS = parsed, true
			}
		case "out_time":
			current.hasOutTime = false
			if parsed, ok := parseFFmpegProgressClock(value); ok {
				current.outTimeSeconds, current.hasOutTime = parsed, true
			}
		case "speed":
			current.hasSpeed = false
			if parsed, ok := parseFFmpegProgressSpeed(value); ok {
				current.speed, current.hasSpeed = parsed, true
			}
		case "progress":
			if value == "continue" || value == "end" {
				completed := completedFFmpegProgress(current, value)
				if !hasStartupDuration && completed.hasEncodedSeconds && completed.hasSpeed && completed.speed > 0 {
					startup := completed.encodedSeconds / completed.speed
					if startup >= 0 && startup <= maximumFFmpegStartupDurationSeconds && !math.IsNaN(startup) && !math.IsInf(startup, 0) {
						startupDurationSeconds, hasStartupDuration = startup, true
					}
				}
				latest = completed
				found = true
			}
			current = ffmpegProgressSample{}
		}
	}
	latest.startupDurationSeconds = startupDurationSeconds
	latest.hasStartupDuration = found && hasStartupDuration
	return latest, found
}

func completedFFmpegProgress(sample ffmpegProgressSample, state string) ffmpegProgress {
	progress := ffmpegProgress{state: state, speed: sample.speed, hasSpeed: sample.hasSpeed}
	switch {
	case sample.hasOutTimeUS:
		progress.encodedSeconds = float64(sample.outTimeMicroseconds) / 1_000_000
		progress.hasEncodedSeconds = true
	case sample.hasOutTimeMS:
		progress.encodedSeconds = float64(sample.outTimeMilliseconds) / 1_000_000
		progress.hasEncodedSeconds = true
	case sample.hasOutTime:
		progress.encodedSeconds = sample.outTimeSeconds
		progress.hasEncodedSeconds = true
	}
	return progress
}

func parseFFmpegProgressMicroseconds(value string) (int64, bool) {
	parsed, err := strconv.ParseInt(value, 10, 64)
	maximum := int64(maximumFFmpegProgressSeconds * 1_000_000)
	return parsed, err == nil && parsed >= 0 && parsed <= maximum
}

func parseFFmpegProgressClock(value string) (float64, bool) {
	parts := strings.Split(value, ":")
	if len(parts) != 3 {
		return 0, false
	}
	hours, err := strconv.ParseUint(parts[0], 10, 32)
	if err != nil {
		return 0, false
	}
	minutes, err := strconv.ParseUint(parts[1], 10, 8)
	if err != nil || minutes >= 60 {
		return 0, false
	}
	seconds, err := strconv.ParseFloat(parts[2], 64)
	if err != nil || seconds < 0 || seconds >= 60 || math.IsNaN(seconds) || math.IsInf(seconds, 0) {
		return 0, false
	}
	result := float64(hours)*3600 + float64(minutes)*60 + seconds
	return result, result <= maximumFFmpegProgressSeconds
}

func parseFFmpegProgressSpeed(value string) (float64, bool) {
	value = strings.TrimSpace(strings.TrimSuffix(value, "x"))
	parsed, err := strconv.ParseFloat(value, 64)
	return parsed, err == nil && parsed >= 0 && parsed <= maximumFFmpegProgressSpeed && !math.IsNaN(parsed) && !math.IsInf(parsed, 0)
}
