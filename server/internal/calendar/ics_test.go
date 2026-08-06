package calendar

import (
	"bytes"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func TestSerializeICSProducesStableEscapedAllDayEvents(t *testing.T) {
	season, episode := 2, 3
	updated := time.Date(2026, time.August, 4, 14, 5, 6, 987000000, time.FixedZone("CEST", 2*60*60))
	events := []Event{
		{
			ID: "movie-id", MediaType: "movie", Title: "L'été, puis; l'hiver\\final\n🎬",
			ReleaseDate: "2026-08-06", UpdatedAt: updated,
		},
		{
			ID: "episode-id", MediaType: "episode", SeriesTitle: "Série 🎭", Title: "Épisode final",
			SeasonNumber: &season, EpisodeNumber: &episode, ReleaseDate: "2026-08-07", UpdatedAt: updated,
		},
	}

	got := string(SerializeICS(events))
	for _, want := range []string{
		"BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//Rivune//Calendar//EN\r\nCALSCALE:GREGORIAN\r\n",
		"UID:movie-id@rivune\r\n",
		"DTSTAMP:20260804T120506Z\r\nLAST-MODIFIED:20260804T120506Z\r\n",
		"DTSTART;VALUE=DATE:20260806\r\nDTEND;VALUE=DATE:20260807\r\n",
		"SUMMARY:L'été\\, puis\\; l'hiver\\\\final\\n🎬\r\n",
		"UID:episode-id@rivune\r\n",
		"DTSTART;VALUE=DATE:20260807\r\nDTEND;VALUE=DATE:20260808\r\n",
		"SUMMARY:Série 🎭 - S02E03 - Épisode final\r\n",
		"END:VCALENDAR\r\n",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("serialized calendar lacks %q:\n%q", want, got)
		}
	}
	if strings.Contains(strings.ReplaceAll(got, "\r\n", ""), "\n") {
		t.Fatalf("serialized calendar contains a bare LF: %q", got)
	}
}

func TestSerializeICSSanitizesForbiddenControlsBeforeEscapingText(t *testing.T) {
	updated := time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)
	forbiddenControls := []byte{0x00, 0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08, 0x0b, 0x0c, 0x0e, 0x0f, 0x10, 0x11, 0x12, 0x13, 0x14, 0x15, 0x16, 0x17, 0x18, 0x19, 0x1a, 0x1b, 0x1c, 0x1d, 0x1e, 0x1f, 0x7f}
	payload := SerializeICS([]Event{{
		ID: "id\x00\x7f", MediaType: "movie", ReleaseDate: "2026-01-02", UpdatedAt: updated,
		Title: "before" + string(forbiddenControls) + "\tmiddle\r\nnext\rafter\nend",
	}})

	for _, forbidden := range forbiddenControls {
		if bytes.Contains(payload, []byte{forbidden}) {
			t.Fatalf("serialized calendar contains forbidden control byte 0x%02x: %q", forbidden, payload)
		}
	}
	if !bytes.Contains(payload, []byte("UID:id@rivune\r\n")) {
		t.Fatalf("UID controls were not removed: %q", payload)
	}
	if !bytes.Contains(payload, []byte("SUMMARY:before\tmiddle\\nnext\\nafter\\nend\r\n")) {
		t.Fatalf("TAB or escaped newlines were not preserved: %q", payload)
	}
}

func TestSerializeICSFoldsAt75OctetsWithoutSplittingUTF8(t *testing.T) {
	updated := time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)
	payload := SerializeICS([]Event{{
		ID: "folded", MediaType: "movie", ReleaseDate: "2026-01-02", UpdatedAt: updated,
		Title: strings.Repeat("é🎬", 30) + ",tail",
	}})

	lines := bytes.Split(payload, []byte("\r\n"))
	var sawContinuation bool
	for _, line := range lines {
		if len(line) > 75 {
			t.Fatalf("folded content line has %d octets: %q", len(line), line)
		}
		if !utf8.Valid(line) {
			t.Fatalf("folding split a UTF-8 sequence: %x", line)
		}
		if len(line) > 0 && line[0] == ' ' {
			sawContinuation = true
		}
	}
	if !sawContinuation {
		t.Fatal("long UTF-8 summary was not folded")
	}
}

func TestSerializeICSUIDAndTimestampsDoNotDependOnCredentialOrRenderTime(t *testing.T) {
	event := Event{
		ID: "stable-title", MediaType: "movie", Title: "Stable", ReleaseDate: "2026-05-01",
		UpdatedAt: time.Date(2025, time.December, 31, 23, 59, 58, 0, time.UTC),
	}
	first := SerializeICS([]Event{event})
	second := SerializeICS([]Event{event})
	if !bytes.Equal(first, second) {
		t.Fatal("identical title snapshots produced different calendar credentials")
	}
	if !bytes.Contains(first, []byte("UID:stable-title@rivune\r\nDTSTAMP:20251231T235958Z\r\nLAST-MODIFIED:20251231T235958Z\r\n")) {
		t.Fatalf("UID or timestamps were not derived from stable title data: %q", first)
	}
}
