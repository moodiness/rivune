package addon

import "fmt"

const addonAssignmentColumns = `
	       ARRAY(
	           SELECT assignment.profile_id::text
	           FROM addon_profile_access assignment
	           JOIN profiles assigned_profile ON assigned_profile.id = assignment.profile_id
	           WHERE assignment.addon_id = pa.id
	           ORDER BY lower(assigned_profile.name), assigned_profile.id
	       ),
	       ARRAY(
	           SELECT assignment.category_id::text
	           FROM addon_category_access assignment
	           JOIN access_categories assigned_category ON assigned_category.id = assignment.category_id
	           WHERE assignment.addon_id = pa.id
	           ORDER BY lower(assigned_category.name), assigned_category.id
	       )`

const addonForProfileQuery = `
	SELECT pa.id::text, pa.transport_url, pa.manifest::text, pa.enabled,
	       COALESCE(profile_order.position, explicit_access.position, category_access.position, 0),` + addonAssignmentColumns + `,
	       pa.installed_at, pa.updated_at
	FROM profile_addons pa
	JOIN profiles active_profile ON active_profile.id = $2::uuid
	LEFT JOIN addon_profile_access explicit_access
	  ON explicit_access.addon_id = pa.id AND explicit_access.profile_id = active_profile.id
	LEFT JOIN addon_category_access category_access
	  ON category_access.addon_id = pa.id AND category_access.category_id = active_profile.category_id
	LEFT JOIN addon_profile_order profile_order
	  ON profile_order.addon_id = pa.id AND profile_order.profile_id = active_profile.id
	WHERE pa.id = $1::uuid
	  AND pa.enabled
	  AND (explicit_access.addon_id IS NOT NULL OR category_access.addon_id IS NOT NULL)
`

const addonForManagementQuery = `
	SELECT pa.id::text, pa.transport_url, pa.manifest::text, pa.enabled,
	       COALESCE(profile_order.position, explicit_access.position, category_access.position, 0),` + addonAssignmentColumns + `,
	       pa.installed_at, pa.updated_at
	FROM profile_addons pa
	JOIN profiles active_profile ON active_profile.id = $2::uuid
	LEFT JOIN addon_profile_access explicit_access
	  ON explicit_access.addon_id = pa.id AND explicit_access.profile_id = active_profile.id
	LEFT JOIN addon_category_access category_access
	  ON category_access.addon_id = pa.id AND category_access.category_id = active_profile.category_id
	LEFT JOIN addon_profile_order profile_order
	  ON profile_order.addon_id = pa.id AND profile_order.profile_id = active_profile.id
	WHERE pa.id = $1::uuid
`

type rowScanner interface {
	Scan(...any) error
}

func queryAddon(scanner rowScanner) (InstalledAddon, error) {
	var installed InstalledAddon
	if err := scanner.Scan(
		&installed.ID, &installed.transportURL, &installed.Manifest, &installed.Enabled, &installed.Position,
		&installed.ProfileIDs, &installed.CategoryIDs, &installed.InstalledAt, &installed.UpdatedAt,
	); err != nil {
		return InstalledAddon{}, err
	}
	manifest, _, err := ParseManifest(installed.Manifest)
	if err != nil {
		return InstalledAddon{}, fmt.Errorf("parse stored addon manifest: %w", err)
	}
	installed.parsedManifest = manifest
	return installed, nil
}
