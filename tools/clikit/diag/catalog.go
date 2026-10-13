package diag

//go:generate go run ./gen

// CatalogEntry is the platform-owned metadata used to render a typed error.
type CatalogEntry struct {
	Code            string `json:"code"`
	HTTPStatus      int    `json:"httpStatus,omitempty"`
	Area            string `json:"area"`
	Stage           string `json:"stage"`
	Severity        string `json:"severity"`
	Retryable       bool   `json:"retryable"`
	ProcessExitCode int    `json:"processExitCode,omitempty"`
	Message         string `json:"message"`
	Hint            string `json:"hint"`
}

// LookupCatalog returns platform metadata for a typed error code.
func LookupCatalog(code string) (CatalogEntry, bool) {
	entry, ok := catalogEntries[code]
	return entry, ok
}
