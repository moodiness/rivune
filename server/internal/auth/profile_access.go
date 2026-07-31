package auth

import (
	"fmt"
	"regexp"
	"time"
)

var wallClockPattern = regexp.MustCompile(`^(?:[01][0-9]|2[0-3]):[0-5][0-9]$`)

type ProfileAccess struct {
	Enabled         bool
	AvailableFrom   *string
	AvailableUntil  *string
	AccessStartTime *string
	AccessEndTime   *string
	AccessTimezone  string
}

func ValidateProfileAccess(value ProfileAccess) error {
	var from, until time.Time
	var err error
	if value.AvailableFrom != nil {
		from, err = parseAccessDate(*value.AvailableFrom)
		if err != nil {
			return fmt.Errorf("availableFrom must be an ISO date (YYYY-MM-DD)")
		}
	}
	if value.AvailableUntil != nil {
		until, err = parseAccessDate(*value.AvailableUntil)
		if err != nil {
			return fmt.Errorf("availableUntil must be an ISO date (YYYY-MM-DD)")
		}
	}
	if value.AvailableFrom != nil && value.AvailableUntil != nil && from.After(until) {
		return fmt.Errorf("availableFrom must be on or before availableUntil")
	}
	if (value.AccessStartTime == nil) != (value.AccessEndTime == nil) {
		return fmt.Errorf("accessStartTime and accessEndTime must be provided together")
	}
	if value.AccessStartTime != nil {
		if !wallClockPattern.MatchString(*value.AccessStartTime) || !wallClockPattern.MatchString(*value.AccessEndTime) {
			return fmt.Errorf("access hours must use HH:MM in 24-hour time")
		}
		if *value.AccessStartTime == *value.AccessEndTime {
			return fmt.Errorf("accessStartTime and accessEndTime must be different")
		}
	}
	if value.AccessTimezone == "" {
		return fmt.Errorf("accessTimezone is required")
	}
	if _, err := time.LoadLocation(value.AccessTimezone); err != nil {
		return fmt.Errorf("accessTimezone must be a valid IANA timezone")
	}
	return nil
}

func ProfileAccessibleAt(value ProfileAccess, now time.Time) bool {
	if !value.Enabled {
		return false
	}
	location, err := time.LoadLocation(value.AccessTimezone)
	if err != nil {
		return false
	}
	local := now.In(location)
	date := local.Format(time.DateOnly)
	if value.AvailableFrom != nil && date < *value.AvailableFrom {
		return false
	}
	if value.AvailableUntil != nil && date > *value.AvailableUntil {
		return false
	}
	if value.AccessStartTime == nil || value.AccessEndTime == nil {
		return true
	}
	clock := local.Format("15:04")
	if *value.AccessStartTime < *value.AccessEndTime {
		return clock >= *value.AccessStartTime && clock < *value.AccessEndTime
	}
	return clock >= *value.AccessStartTime || clock < *value.AccessEndTime
}

func profileGrantAccessible(value ProfileAccess, expiresAt *time.Time, now time.Time) bool {
	return expiresAt != nil && expiresAt.After(now) && ProfileAccessibleAt(value, now)
}

func reconcileProfileGrant(principal *Principal, value ProfileAccess, now time.Time) bool {
	if principal.ActiveProfileID == nil || profileGrantAccessible(value, principal.ProfileGrantExpiresAt, now) {
		return false
	}
	principal.ActiveProfileID = nil
	principal.ProfileGrantExpiresAt = nil
	return true
}

func parseAccessDate(value string) (time.Time, error) {
	parsed, err := time.Parse(time.DateOnly, value)
	if err != nil || parsed.Format(time.DateOnly) != value {
		return time.Time{}, fmt.Errorf("invalid ISO date")
	}
	return parsed, nil
}
