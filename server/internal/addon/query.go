package addon

import "fmt"

const addonByIDQuery = `
	SELECT pa.id::text, pa.transport_url, pa.manifest::text, pa.position,
	       ARRAY(
	           SELECT access.profile_id::text
	           FROM addon_profile_access access
	           JOIN profiles p ON p.id = access.profile_id
	           WHERE access.addon_id = pa.id
	           ORDER BY lower(p.name), p.id
	       ),
	       pa.installed_at, pa.updated_at
	FROM profile_addons pa
	WHERE pa.id = $1::uuid
`

type rowScanner interface {
	Scan(...any) error
}

func queryAddon(scanner rowScanner) (InstalledAddon, error) {
	var installed InstalledAddon
	if err := scanner.Scan(
		&installed.ID, &installed.TransportURL, &installed.Manifest, &installed.Position,
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
