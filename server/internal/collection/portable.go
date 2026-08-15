package collection

// ValidatePortableBudget applies the aggregate collection import limits used by
// the standalone collection importer.
func ValidatePortableBudget(values []SaveInput) error {
	if len(values) > maximumCollections {
		return invalid("a portable archive cannot contain more than 100 collections")
	}
	return validateImportDocumentBudget(ExportDocument{SchemaVersion: ExportSchemaVersion, Collections: values})
}

// NormalizePortable validates a collection after its portable add-on keys have
// been rebound to target-local add-on IDs. Runtime folder and source IDs are
// deliberately accepted so the archive importer can provide deterministic,
// target-local identities.
func NormalizePortable(input SaveInput) (SaveInput, error) {
	input.ProfileIDs = nil
	input.CategoryIDs = nil
	input.ExpectedVersion = 0
	return normalizeAndValidate(input, false)
}
