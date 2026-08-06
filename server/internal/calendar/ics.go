package calendar

import (
	"bytes"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

const calendarProductID = "-//Rivune//Calendar//EN"

// SerializeICS returns a deterministic RFC 5545 calendar using CRLF line endings.
func SerializeICS(events []Event) []byte {
	var output bytes.Buffer
	writeICSLine(&output, "BEGIN:VCALENDAR")
	writeICSLine(&output, "VERSION:2.0")
	writeICSLine(&output, "PRODID:"+calendarProductID)
	writeICSLine(&output, "CALSCALE:GREGORIAN")
	for _, event := range events {
		date, err := time.Parse(time.DateOnly, event.ReleaseDate)
		if err != nil {
			continue
		}
		stamp := event.UpdatedAt.UTC().Format("20060102T150405Z")
		writeICSLine(&output, "BEGIN:VEVENT")
		writeICSLine(&output, "UID:"+escapeICSText(event.ID+"@rivune"))
		writeICSLine(&output, "DTSTAMP:"+stamp)
		writeICSLine(&output, "LAST-MODIFIED:"+stamp)
		writeICSLine(&output, "DTSTART;VALUE=DATE:"+date.Format("20060102"))
		writeICSLine(&output, "DTEND;VALUE=DATE:"+date.AddDate(0, 0, 1).Format("20060102"))
		writeICSLine(&output, "SUMMARY:"+escapeICSText(eventSummary(event)))
		writeICSLine(&output, "END:VEVENT")
	}
	writeICSLine(&output, "END:VCALENDAR")
	return output.Bytes()
}

func eventSummary(event Event) string {
	if event.MediaType != "episode" {
		return event.Title
	}
	code := ""
	if event.SeasonNumber != nil && event.EpisodeNumber != nil {
		code = fmt.Sprintf("S%02dE%02d", *event.SeasonNumber, *event.EpisodeNumber)
	}
	parts := make([]string, 0, 3)
	for _, part := range []string{event.SeriesTitle, code, event.Title} {
		if part != "" {
			parts = append(parts, part)
		}
	}
	return strings.Join(parts, " - ")
}

func escapeICSText(value string) string {
	value = strings.Map(func(character rune) rune {
		if character <= '\x08' || character == '\x0b' || character == '\x0c' ||
			(character >= '\x0e' && character <= '\x1f') || character == '\x7f' {
			return -1
		}
		return character
	}, value)
	value = strings.ReplaceAll(value, "\\", "\\\\")
	value = strings.ReplaceAll(value, "\r\n", "\\n")
	value = strings.ReplaceAll(value, "\r", "\\n")
	value = strings.ReplaceAll(value, "\n", "\\n")
	value = strings.ReplaceAll(value, ";", "\\;")
	return strings.ReplaceAll(value, ",", "\\,")
}

func writeICSLine(output *bytes.Buffer, line string) {
	first := true
	for len(line) > 0 {
		limit := 75
		if !first {
			output.WriteByte(' ')
			limit--
		}
		length := utf8SafePrefix(line, limit)
		output.WriteString(line[:length])
		output.WriteString("\r\n")
		line = line[length:]
		first = false
	}
	if first {
		output.WriteString("\r\n")
	}
}

func utf8SafePrefix(value string, maximumBytes int) int {
	if len(value) <= maximumBytes {
		return len(value)
	}
	end := maximumBytes
	for end > 0 && !utf8.RuneStart(value[end]) {
		end--
	}
	if end == 0 {
		_, size := utf8.DecodeRuneInString(value)
		return size
	}
	return end
}
