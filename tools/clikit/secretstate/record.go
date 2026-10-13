// Package secretstate owns the serialized launcher secret contract and the
// values that are safe to project into a host process.
package secretstate

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"

	"github.com/kb-labs/clikit/diag"
)

const (
	SchemaVersion                = 2
	UnsupportedCode              = "KB_INSTALL_SECRET_STORE_UNSUPPORTED"
	BootstrapPasswordEnvironment = "GATEWAY_BOOTSTRAP_ADMIN_PASSWORD"
	BootstrapPasswordRequirement = "gateway.bootstrap.password"
)

var ErrUnsupportedFormat = errors.New("unsupported launcher secret state")

type BootstrapPasswordOrigin string

const (
	OriginSupplied  BootstrapPasswordOrigin = "supplied"
	OriginGenerated BootstrapPasswordOrigin = "generated"
)

type Record struct {
	schema       int
	generation   uint64
	requirements map[string]string
	bootstrap    bootstrapState
}

type bootstrapState struct {
	completed         string
	passwordOrigin    BootstrapPasswordOrigin
	passwordDisclosed bool
}

type wireRecord struct {
	Schema       int               `json:"schema"`
	Generation   uint64            `json:"generation"`
	Requirements map[string]string `json:"requirements"`
	Bootstrap    wireBootstrap     `json:"bootstrap"`
}

type wireBootstrap struct {
	Completed         string                  `json:"bootstrapCompleted,omitempty"`
	PasswordOrigin    BootstrapPasswordOrigin `json:"bootstrapPasswordOrigin,omitempty"`
	PasswordDisclosed bool                    `json:"bootstrapPasswordDisclosed,omitempty"`
}

type HostProjection struct {
	Values     map[string]string
	Suppressed []string
}

var environmentName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

func NewRecord() Record {
	return Record{schema: SchemaVersion, generation: 1, requirements: map[string]string{}}
}

func Decode(data []byte) (Record, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var wire wireRecord
	if err := decoder.Decode(&wire); err != nil {
		return Record{}, unsupported()
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return Record{}, unsupported()
	}
	if wire.Schema != SchemaVersion || wire.Generation == 0 || wire.Requirements == nil {
		return Record{}, unsupported()
	}
	return Record{
		schema:       wire.Schema,
		generation:   wire.Generation,
		requirements: wire.Requirements,
		bootstrap: bootstrapState{
			completed:         wire.Bootstrap.Completed,
			passwordOrigin:    wire.Bootstrap.PasswordOrigin,
			passwordDisclosed: wire.Bootstrap.PasswordDisclosed,
		},
	}, nil
}

func Encode(record Record) ([]byte, error) {
	if record.schema != SchemaVersion || record.requirements == nil {
		return nil, unsupported()
	}
	return json.Marshal(wireRecord{
		Schema:       record.schema,
		Generation:   record.generation,
		Requirements: record.requirements,
		Bootstrap: wireBootstrap{
			Completed:         record.bootstrap.completed,
			PasswordOrigin:    record.bootstrap.passwordOrigin,
			PasswordDisclosed: record.bootstrap.passwordDisclosed,
		},
	})
}

// Generation identifies the serialized base from which this record was read.
func (record Record) Generation() uint64 { return record.generation }

// AdvanceFrom accepts a commit only when it was based on the current record.
// It also preserves the irreversible lifecycle facts when deciding whether a
// candidate may replace the current state.
func (record *Record) AdvanceFrom(current Record) bool {
	if record.generation != current.generation {
		return false
	}
	if current.bootstrap.passwordDisclosed {
		if !record.bootstrap.passwordDisclosed {
			return false
		}
		if record.bootstrap.passwordOrigin != current.bootstrap.passwordOrigin {
			return false
		}
		if _, present := record.requirements[BootstrapPasswordRequirement]; present {
			return false
		}
		if _, present := record.requirements[BootstrapPasswordEnvironment]; present {
			return false
		}
	}
	if current.bootstrap.completed != "" && record.bootstrap.completed == "" {
		return false
	}
	record.generation = current.generation + 1
	return true
}

func (record Record) Requirement(name string) (string, bool) {
	value, ok := record.requirements[name]
	return value, ok
}

func (record Record) Requirements() map[string]string {
	values := make(map[string]string, len(record.requirements))
	for name, value := range record.requirements {
		values[name] = value
	}
	return values
}

func (record *Record) SetRequirement(name, value string) {
	if record.requirements == nil {
		record.requirements = map[string]string{}
	}
	record.requirements[name] = value
}

func (record *Record) SetBootstrapPassword(value string, origin BootstrapPasswordOrigin) {
	record.SetRequirement(BootstrapPasswordRequirement, value)
	record.SetRequirement(BootstrapPasswordEnvironment, value)
	record.bootstrap.passwordOrigin = origin
}

func (record *Record) MarkBootstrapCompleted(value string) {
	record.bootstrap.completed = value
}

func (record Record) BootstrapCompleted() (string, bool) {
	return record.bootstrap.completed, record.bootstrap.completed != ""
}

func (record *Record) MarkBootstrapDisclosed() {
	record.bootstrap.passwordDisclosed = true
	delete(record.requirements, BootstrapPasswordRequirement)
	delete(record.requirements, BootstrapPasswordEnvironment)
}

func (record Record) BootstrapPasswordDisclosed() bool {
	return record.bootstrap.passwordDisclosed
}

func (record Record) BootstrapPasswordOrigin() (BootstrapPasswordOrigin, bool) {
	return record.bootstrap.passwordOrigin, record.bootstrap.passwordOrigin != ""
}

func (record Record) DeleteRequirement(name string) {
	delete(record.requirements, name)
}

// Load reads one state record. A missing file means the installation has not
// stored launcher secrets; malformed or unknown records fail closed.
func Load(path string) (Record, bool, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return NewRecord(), false, nil
	}
	if err != nil {
		return Record{}, false, err
	}
	record, err := Decode(data)
	if err != nil {
		return Record{}, true, err
	}
	return record, true, nil
}

// HostEnvironment is the sole projection of launcher state into host env.
func HostEnvironment(path string) (HostProjection, error) {
	record, _, err := Load(path)
	if err != nil {
		return HostProjection{}, err
	}
	values := make(map[string]string, len(record.requirements))
	for name, value := range record.requirements {
		if environmentName.MatchString(name) {
			values[name] = value
		}
	}
	projection := HostProjection{Values: values}
	if record.bootstrap.completed != "" {
		delete(projection.Values, BootstrapPasswordEnvironment)
		projection.Suppressed = []string{BootstrapPasswordEnvironment}
	}
	return projection, nil
}

// RecordPath resolves the active version's shared launcher secret record.
func RecordPath(root string) string {
	return filepath.Join(root, ".kb", "v2", "secret-state.json")
}

// HostEnvironmentAtRoot loads the active version's shared launcher secrets.
func HostEnvironmentAtRoot(root string) (HostProjection, error) {
	return HostEnvironment(RecordPath(root))
}

func unsupported() error {
	entry, ok := diag.LookupCatalog(UnsupportedCode)
	if !ok {
		return diag.Wrap(ErrUnsupportedFormat, UnsupportedCode, "The platform secret state cannot be read safely.")
	}
	return diag.Wrap(ErrUnsupportedFormat, UnsupportedCode, entry.Message, diag.WithHint(entry.Hint))
}
