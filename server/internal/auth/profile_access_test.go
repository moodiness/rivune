package auth

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

func TestProfileAccessibleAtInclusiveDateBounds(t *testing.T) {
	access := ProfileAccess{Enabled: true, AvailableFrom: new("2026-08-01"), AvailableUntil: new("2026-08-03"), AccessTimezone: "America/New_York"}
	for _, instant := range []time.Time{
		time.Date(2026, 8, 1, 4, 0, 0, 0, time.UTC),
		time.Date(2026, 8, 4, 3, 59, 59, 0, time.UTC),
	} {
		if !ProfileAccessibleAt(access, instant) {
			t.Fatalf("expected %s to be inside inclusive dates", instant)
		}
	}
	if ProfileAccessibleAt(access, time.Date(2026, 8, 1, 3, 59, 59, 0, time.UTC)) || ProfileAccessibleAt(access, time.Date(2026, 8, 4, 4, 0, 0, 0, time.UTC)) {
		t.Fatal("date access extended outside the local inclusive bounds")
	}
}

func TestProfileAccessibleAtNormalAndOvernightHours(t *testing.T) {
	instant := func(hour, minute int) time.Time { return time.Date(2026, 8, 1, hour, minute, 0, 0, time.UTC) }
	normal := ProfileAccess{Enabled: true, AccessStartTime: new("08:00"), AccessEndTime: new("20:00"), AccessTimezone: "UTC"}
	if !ProfileAccessibleAt(normal, instant(8, 0)) || !ProfileAccessibleAt(normal, instant(19, 59)) || ProfileAccessibleAt(normal, instant(20, 0)) {
		t.Fatal("normal access window was not start-inclusive and end-exclusive")
	}
	overnight := ProfileAccess{Enabled: true, AccessStartTime: new("20:00"), AccessEndTime: new("08:00"), AccessTimezone: "UTC"}
	if !ProfileAccessibleAt(overnight, instant(20, 0)) || !ProfileAccessibleAt(overnight, instant(7, 59)) || ProfileAccessibleAt(overnight, instant(8, 0)) || ProfileAccessibleAt(overnight, instant(12, 0)) {
		t.Fatal("overnight access window was evaluated incorrectly")
	}
}

func TestExistingGrantStopsAuthorizingAfterScheduleBoundary(t *testing.T) {
	now := time.Date(2026, 8, 1, 20, 0, 0, 0, time.UTC)
	expires := now.Add(time.Hour)
	access := ProfileAccess{Enabled: true, AccessStartTime: new("08:00"), AccessEndTime: new("20:00"), AccessTimezone: "UTC"}
	if profileGrantAccessible(access, &expires, now) {
		t.Fatal("existing grant remained authorized at the end-exclusive schedule boundary")
	}
	inside := now.Add(-time.Minute)
	if !profileGrantAccessible(access, &expires, inside) {
		t.Fatal("existing grant was rejected while profile access was available")
	}
}

func TestReconcileProfileGrantClearsUnavailablePrincipal(t *testing.T) {
	now := time.Date(2026, 8, 2, 0, 0, 0, 0, time.UTC)
	profileID := "profile-id"
	expires := now.Add(time.Hour)
	principal := Principal{ActiveProfileID: &profileID, ProfileGrantExpiresAt: &expires, ActiveProfileCanManage: true}
	access := ProfileAccess{Enabled: true, AvailableUntil: new("2026-08-01"), AccessTimezone: "UTC"}

	if !reconcileProfileGrant(&principal, access, now) {
		t.Fatal("unavailable grant was not marked for persistent clearing")
	}
	if principal.ActiveProfileID != nil || principal.ProfileGrantExpiresAt != nil || principal.ActiveProfileCanManage {
		t.Fatal("unavailable grant or its derived management capability remained on the authenticated principal")
	}
}

func TestValidateProfileAccessRejectsInvalidRestrictions(t *testing.T) {
	cases := []ProfileAccess{
		{Enabled: true, AvailableFrom: new("2026-08-02"), AvailableUntil: new("2026-08-01"), AccessTimezone: "UTC"},
		{Enabled: true, AccessStartTime: new("08:00"), AccessTimezone: "UTC"},
		{Enabled: true, AccessStartTime: new("08:00"), AccessEndTime: new("08:00"), AccessTimezone: "UTC"},
		{Enabled: true, AccessTimezone: "Not/A_Real_Zone"},
	}
	for _, value := range cases {
		if err := ValidateProfileAccess(value); err == nil {
			t.Fatalf("expected invalid access restrictions to fail: %+v", value)
		}
	}
}

type profileAuthorizationTestQuerier struct {
	args []any
}

func (querier *profileAuthorizationTestQuerier) QueryRow(_ context.Context, _ string, args ...any) pgx.Row {
	querier.args = args
	return profileAuthorizationTestRow{authorized: true}
}

type profileAuthorizationTestRow struct {
	authorized bool
}

func (row profileAuthorizationTestRow) Scan(destinations ...any) error {
	*destinations[0].(*bool) = row.authorized
	return nil
}

func TestProfileAuthorizationPassesScopeAndCategoryToCentralGuard(t *testing.T) {
	categoryID := "category-a"
	principal := Principal{
		UserID: "administrator-id", Role: "admin",
		AuthorizationScope: AuthorizationScopeCategory, CategoryID: &categoryID,
	}
	querier := &profileAuthorizationTestQuerier{}
	authorized, err := CanManageProfiles(
		context.Background(), querier, principal,
		[]string{"2d554099-8950-4f85-b3aa-a8c7b157413e"},
	)
	if err != nil || !authorized {
		t.Fatalf("authorize categorized profile management: authorized=%v err=%v", authorized, err)
	}
	if len(querier.args) != 6 || querier.args[2] != false ||
		querier.args[3] != AuthorizationScopeCategory || querier.args[4] != categoryID ||
		querier.args[5] != true {
		t.Fatalf("paired administrator guard omitted category scope: %+v", querier.args)
	}

	globalQuerier := &profileAuthorizationTestQuerier{}
	global := Principal{UserID: "administrator-id", Role: "admin", AuthorizationScope: AuthorizationScopeGlobalAdministrator}
	if _, err := CanAccessProfiles(
		context.Background(), globalQuerier, global,
		[]string{"2d554099-8950-4f85-b3aa-a8c7b157413e"},
	); err != nil {
		t.Fatalf("authorize global profile access: %v", err)
	}
	if len(globalQuerier.args) != 6 || globalQuerier.args[2] != true || globalQuerier.args[5] != false {
		t.Fatalf("global administrator guard did not use explicit global authority: %+v", globalQuerier.args)
	}
}

func TestProfileAuthorizationRejectsMalformedIdentifiersWithoutDatabaseEnumeration(t *testing.T) {
	querier := &profileAuthorizationTestQuerier{}
	authorized, err := CanAccessProfiles(
		context.Background(), querier,
		Principal{Role: "admin", AuthorizationScope: AuthorizationScopeGlobalAdministrator},
		[]string{"not-a-profile-id"},
	)
	if err != nil || authorized {
		t.Fatalf("malformed profile identifier authorization = %v, %v; want false, nil", authorized, err)
	}
	if querier.args != nil {
		t.Fatal("malformed profile identifier reached the database")
	}
}
