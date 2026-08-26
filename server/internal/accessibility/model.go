package accessibility

import "errors"

var (
	ErrActiveProfileRequired = errors.New("the requested profile is not the active profile")
	ErrInvalidInput          = errors.New("invalid accessibility preferences")
	ErrConflict              = errors.New("accessibility preference revision conflict")
)

type Preferences struct {
	ReducedMotion    string `json:"reducedMotion"`
	HighContrast     string `json:"highContrast"`
	TextScale        int    `json:"textScale"`
	Captions         string `json:"captions"`
	AudioDescription bool   `json:"audioDescription"`
	FocusIndicators  string `json:"focusIndicators"`
}

type Document struct {
	Revision int64 `json:"revision"`
	Preferences
}

type UpdateInput struct {
	Revision int64 `json:"revision"`
	Preferences
}

func Defaults() Preferences {
	return Preferences{
		ReducedMotion: "system", HighContrast: "system", TextScale: 100,
		Captions: "system", AudioDescription: false, FocusIndicators: "standard",
	}
}

func valid(preferences Preferences) bool {
	return oneOf(preferences.ReducedMotion, "system", "reduce", "no-preference") &&
		oneOf(preferences.HighContrast, "system", "more", "standard") &&
		(preferences.TextScale == 100 || preferences.TextScale == 115 || preferences.TextScale == 130) &&
		oneOf(preferences.Captions, "system", "on", "off") &&
		oneOf(preferences.FocusIndicators, "standard", "enhanced")
}

func oneOf(value string, allowed ...string) bool {
	for _, candidate := range allowed {
		if value == candidate {
			return true
		}
	}
	return false
}
