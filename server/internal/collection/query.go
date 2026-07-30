package collection

import (
	"encoding/json"
	"fmt"
)

const sharedCollectionFields = `
	pc.id::text, pc.title, COALESCE(pc.backdrop_image_url, ''), pc.hero_enabled,
	pc.pin_to_top, pc.focus_glow_enabled, pc.view_mode, pc.folder_cover_shape, pc.folders::text,
	ARRAY(
		SELECT assignment.profile_id::text
		FROM collection_profile_access assignment
		JOIN profiles p ON p.id = assignment.profile_id
		WHERE assignment.collection_id = pc.id
		ORDER BY lower(p.name), p.id
	)
`

const sharedCollectionTail = `, pc.version, pc.created_at, pc.updated_at`

const collectionByIDQuery = `
	SELECT ` + sharedCollectionFields + `, pc.position` + sharedCollectionTail + `
	FROM profile_collections pc
	WHERE pc.id = $1::uuid
`

func scanSharedCollection(scanner rowScanner) (Collection, error) {
	var value Collection
	var foldersJSON string
	if err := scanner.Scan(
		&value.ID, &value.Title, &value.BackdropImageURL, &value.HeroEnabled, &value.PinToTop,
		&value.FocusGlowEnabled, &value.ViewMode, &value.FolderCoverShape,
		&foldersJSON, &value.ProfileIDs, &value.Position, &value.Version, &value.CreatedAt, &value.UpdatedAt,
	); err != nil {
		return Collection{}, err
	}
	if err := json.Unmarshal([]byte(foldersJSON), &value.Folders); err != nil {
		return Collection{}, fmt.Errorf("decode stored collection folders: %w", err)
	}
	if value.Folders == nil {
		value.Folders = []Folder{}
	}
	return value, nil
}
