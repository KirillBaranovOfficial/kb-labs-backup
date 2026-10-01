package contracts

// Requirement declares a platform capability required by an artifact.
type Requirement struct {
	Capability string `json:"capability"`
	RequiredBy string `json:"requiredBy,omitempty"`
}

// ConfigRequirement is the signed configuration projection for an artifact.
// It is carried through resolution so an installed package can persist the
// exact metadata used by planning and later doctor runs.
type ConfigRequirement struct {
	ID       string   `json:"id"`
	Path     string   `json:"path,omitempty"`
	Required bool     `json:"required,omitempty"`
	Optional bool     `json:"optional,omitempty"`
	Secret   bool     `json:"secret,omitempty"`
	Generate bool     `json:"generate,omitempty"`
	Default  string   `json:"default,omitempty"`
	Env      string   `json:"env,omitempty"`
	Services []string `json:"services,omitempty"`
	Hint     string   `json:"hint,omitempty"`
	Action   string   `json:"action,omitempty"`
}
