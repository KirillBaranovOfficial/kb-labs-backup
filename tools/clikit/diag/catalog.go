package diag

import (
	_ "embed"
	"encoding/json"
)

// CatalogEntry is the platform-owned metadata used to render a typed error.
type CatalogEntry struct {
	Code      string `json:"code"`
	Area      string `json:"area"`
	Stage     string `json:"stage"`
	Severity  string `json:"severity"`
	Retryable bool   `json:"retryable"`
	Message   string `json:"message"`
	Hint      string `json:"hint"`
}

// error-catalog.json is kept byte-for-byte in sync with the platform source
// catalog by TestGoErrorCodesExistInPlatformCatalog.
//
//go:embed error-catalog.json
var embeddedCatalog []byte

var catalogEntries = loadCatalog(embeddedCatalog)

func loadCatalog(content []byte) map[string]CatalogEntry {
	var catalog struct {
		Codes []CatalogEntry `json:"codes"`
	}
	if err := json.Unmarshal(content, &catalog); err != nil {
		panic("clikit: embedded platform error catalog is invalid: " + err.Error())
	}
	entries := make(map[string]CatalogEntry, len(catalog.Codes))
	for _, entry := range catalog.Codes {
		entries[entry.Code] = entry
	}
	return entries
}

// LookupCatalog returns platform metadata for a typed error code.
func LookupCatalog(code string) (CatalogEntry, bool) {
	entry, ok := catalogEntries[code]
	return entry, ok
}
