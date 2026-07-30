package auth

import "testing"

func TestDeviceUserCodesUseUnambiguousAlphabet(t *testing.T) {
	for range 100 {
		code, err := newDeviceUserCode()
		if err != nil {
			t.Fatalf("generate user code: %v", err)
		}
		if len(code) != deviceUserCodeLength {
			t.Fatalf("expected %d characters, got %q", deviceUserCodeLength, code)
		}
		for _, character := range code {
			if !containsRune(deviceUserCodeAlphabet, character) {
				t.Fatalf("generated ambiguous character %q in %q", character, code)
			}
		}
	}
}

func TestDeviceUserCodeFormattingAndNormalization(t *testing.T) {
	if formatted := formatDeviceUserCode("ABCDEFGH"); formatted != "ABCD-EFGH" {
		t.Fatalf("unexpected formatted code %q", formatted)
	}
	for _, input := range []string{"abcd-efgh", "ABCD EFGH", "  ABCDEFGH  "} {
		if normalized := normalizeDeviceUserCode(input); normalized != "ABCDEFGH" {
			t.Fatalf("unexpected normalization %q for %q", normalized, input)
		}
	}
}

func containsRune(value string, target rune) bool {
	for _, character := range value {
		if character == target {
			return true
		}
	}
	return false
}
