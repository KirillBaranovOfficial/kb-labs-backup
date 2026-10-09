package contracts

type UpdateSummary struct {
	Components      []UpdateComponent    `json:"components"`
	AddedPackages   []string             `json:"addedPackages"`
	RemovedPackages []string             `json:"removedPackages"`
	DownloadBytes   int64                `json:"downloadBytes"`
	ReleaseNotes    []UpdateReleaseNotes `json:"releaseNotes"`
}

type UpdateComponent struct {
	ID          string `json:"id"`
	Package     string `json:"package"`
	FromVersion string `json:"fromVersion"`
	ToVersion   string `json:"toVersion"`
}

type UpdateReleaseNotes struct {
	Version string `json:"version"`
	Notes   string `json:"notes"`
}
