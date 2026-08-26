package portable

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/moodiness/rivune/server/internal/auth"
	"github.com/moodiness/rivune/server/internal/runtimesettings"
)

var archiveUUIDPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[1-5][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

func (s *Service) Create(ctx context.Context, captured auth.Principal, input CreateInput) (ImportReport, error) {
	input.CategoryID = strings.ToLower(strings.TrimSpace(input.CategoryID))
	if !archiveUUIDPattern.MatchString(input.CategoryID) {
		return ImportReport{}, ErrInvalidDocument
	}
	if err := Validate(input.Archive, time.Now().UTC()); err != nil {
		return ImportReport{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return ImportReport{}, fmt.Errorf("begin profile archive creation: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	principal, err := s.authorizeGlobal(ctx, tx, captured)
	if err != nil {
		return ImportReport{}, err
	}
	var profileID string
	if err := tx.QueryRow(ctx, `
		INSERT INTO profiles (category_id,name,description,is_child,avatar_preset,enabled,access_timezone)
		SELECT id,$2,$3,$4,'aurora',true,'UTC' FROM access_categories WHERE id=$1::uuid
		RETURNING id::text
	`, input.CategoryID, input.Archive.Identity.Name, input.Archive.Identity.Description, input.Archive.Identity.IsChild).Scan(&profileID); errors.Is(err, pgx.ErrNoRows) {
		return ImportReport{}, ErrInvalidDocument
	} else if err != nil {
		return ImportReport{}, fmt.Errorf("create archived profile: %w", err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO profile_settings (profile_id) VALUES ($1::uuid)`, profileID); err != nil {
		return ImportReport{}, fmt.Errorf("create archived profile settings: %w", err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO user_profile_access (user_id,profile_id,can_manage) VALUES ($1::uuid,$2::uuid,false)`, principal.UserID, profileID); err != nil {
		return ImportReport{}, fmt.Errorf("grant archived profile creator access: %w", err)
	}
	if err := lockImportResources(ctx, tx, profileID, input.Archive); err != nil {
		return ImportReport{}, err
	}
	if err := validateTargetSettings(ctx, tx, input.Archive); err != nil {
		return ImportReport{}, err
	}
	report, err := s.importDocumentTx(ctx, tx, profileID, input.Archive, "create")
	if err != nil {
		return ImportReport{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return ImportReport{}, fmt.Errorf("commit profile archive creation: %w", err)
	}
	return report, nil
}

func (s *Service) authorizeGlobal(ctx context.Context, tx pgx.Tx, captured auth.Principal) (auth.Principal, error) {
	runtime := runtimesettings.Load(ctx, s.runtimeSettings)
	principal, authorized, err := auth.ReloadAndLockPrincipal(ctx, tx, captured, time.Now().UTC(), runtime.Location)
	if err != nil {
		var databaseError *pgconn.PgError
		if errors.As(err, &databaseError) && databaseError.Code == "40001" {
			return auth.Principal{}, ErrForbidden
		}
		return auth.Principal{}, fmt.Errorf("revalidate profile archive creator: %w", err)
	}
	if !authorized || !principal.IsGlobalAdministrator() {
		return auth.Principal{}, ErrForbidden
	}
	return principal, nil
}

func (s *Service) importDocumentTx(ctx context.Context, tx pgx.Tx, profileID string, document Document, mode string) (ImportReport, error) {
	report := ImportReport{Mode: mode, ProfileID: profileID}
	identityChanged, avatarChanged, err := importIdentity(ctx, tx, profileID, document.Identity)
	if err != nil { return ImportReport{}, err }
	report.Sections = append(report.Sections, section("identity", 1, identityChanged), section("avatar", 1, avatarChanged))
	if changed, err := importSettings(ctx, tx, profileID, document); err != nil { return ImportReport{}, err } else { report.Sections = append(report.Sections, section("settings", 1, changed)) }
	addonIDs, addonManifests, created, changed, err := importAddons(ctx, tx, profileID, document)
	if err != nil { return ImportReport{}, err }
	report.Sections = append(report.Sections, counts("addons", len(document.Addons), created, changed))
	created, changed, err = importCollections(ctx, tx, profileID, document, addonIDs, addonManifests)
	if err != nil { return ImportReport{}, err }
	report.Sections = append(report.Sections, counts("collections", len(document.Collections), created, changed))
	titleIDs, created, changed, err := importTitles(ctx, tx, profileID, document, addonIDs)
	if err != nil { return ImportReport{}, err }
	report.Sections = append(report.Sections, counts("titles", len(document.Titles), created, changed))
	stateReports, err := importStates(ctx, tx, profileID, document, titleIDs)
	if err != nil { return ImportReport{}, err }
	report.Sections = append(report.Sections, stateReports...)
	dismissalReport, err := importContinueDismissals(ctx, tx, profileID, document, titleIDs)
	if err != nil { return ImportReport{}, err }
	report.Sections = append(report.Sections, dismissalReport)
	if len(document.TrackingPreferences) > 0 {
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, int64(0x524956554e454f42)); err != nil { return ImportReport{}, fmt.Errorf("lock tracking outbox preferences: %w", err) }
	}
	trackingReport, accounts, err := importTrackingPreferences(ctx, tx, profileID, document)
	if err != nil { return ImportReport{}, err }
	report.TrackingAccounts = accounts
	report.Sections = append(report.Sections, trackingReport)
	return report, nil
}
