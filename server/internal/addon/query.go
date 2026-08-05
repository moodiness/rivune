package addon

import "fmt"

const addonForProfileQuery = `
	SELECT pa.id::text, pa.transport_url, pa.manifest::text, COALESCE(access.position, 0),
	       ARRAY(
	           SELECT assignment.profile_id::text
	           FROM addon_profile_access assignment
	           JOIN profiles p ON p.id = assignment.profile_id
	           WHERE assignment.addon_id = pa.id
	           ORDER BY lower(p.name), p.id
	       ),
	       pa.installed_at, pa.updated_at
	FROM profile_addons pa
	JOIN addon_profile_access access
	  ON access.addon_id = pa.id AND access.profile_id = $2::uuid
	WHERE pa.id = $1::uuid
`

const addonForManagementQuery = `
	SELECT pa.id::text, pa.transport_url, pa.manifest::text, COALESCE(access.position, 0),
	       ARRAY(
	           SELECT assignment.profile_id::text
	           FROM addon_profile_access assignment
	           JOIN profiles p ON p.id = assignment.profile_id
	           WHERE assignment.addon_id = pa.id
	           ORDER BY lower(p.name), p.id
	       ),
	       pa.installed_at, pa.updated_at
	FROM profile_addons pa
	LEFT JOIN addon_profile_access access
	  ON access.addon_id = pa.id AND access.profile_id = $2::uuid
	WHERE pa.id = $1::uuid
`

type rowScanner interface {
	Scan(...any) error
}

func queryAddon(scanner rowScanner) (InstalledAddon, error) {
	var installed InstalledAddon
	if err := scanner.Scan(
		&installed.ID, &installed.transportURL, &installed.Manifest, &installed.Position,
		&installed.ProfileIDs, &installed.InstalledAt, &installed.UpdatedAt,
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
