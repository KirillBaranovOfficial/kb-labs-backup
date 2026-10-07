// Package diag is the structured-diagnostic core shared by the KB Labs Go
// launcher tools. It mirrors the platform's TypeScript error contract
// (core/config/src/errors/kb-error.ts: KbError {code, message, hint, meta})
// and adds an explicit Reason, so every failure can say WHAT happened
// (Message), WHY (Reason) and WHAT TO DO / WHERE TO LOOK (Hint).
//
// A *Diag satisfies the error interface, so it flows through cobra RunE and
// any error path unchanged; the top-level handler renders it per output mode.
package diag

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"runtime/debug"
	"time"
)

// Diag is a structured diagnostic. The zero value is not useful — construct
// with New or Wrap.
type Diag struct {
	// Code is a stable KB_* platform error identifier.
	Code string `json:"code"`
	// Message is the human-readable WHAT (one line, no trailing punctuation).
	Message string `json:"message"`
	// Reason is the WHY — the underlying cause, in plain language.
	Reason string `json:"reason,omitempty"`
	// Hint is the WHAT-TO-DO / WHERE-TO-LOOK from the platform error catalog.
	Hint string `json:"hint,omitempty"`
	// Cause is safe explanatory text for machine output. It is not printed by
	// the human renderer; wrapped Go errors remain available only via Unwrap.
	Cause string `json:"-"`
	// Meta carries structured context (paths, hosts, per-action failures, …).
	Meta            map[string]any `json:"meta,omitempty"`
	Actions         []Action       `json:"-"`
	Docs            string         `json:"-"`
	CorrelationID   string         `json:"correlationId,omitempty"`
	ProcessExitCode int            `json:"-"`

	// cause is the wrapped error; surfaced via Unwrap, never serialized.
	cause error
}

// Error satisfies the error interface using Message.
func (d *Diag) Error() string {
	if d == nil {
		return ""
	}
	return d.Message
}

// Unwrap exposes the wrapped cause for errors.Is/As.
func (d *Diag) Unwrap() error {
	if d == nil {
		return nil
	}
	return d.cause
}

// Opt mutates a Diag during construction.
type Opt func(*Diag)

// WithReason sets the WHY.
func WithReason(reason string) Opt { return func(d *Diag) { d.Reason = reason } }

// WithHint preserves a command-specific next step while retaining the
// catalog's remaining canonical envelope fields.
func WithHint(hint string) Opt { return func(d *Diag) { d.Hint = hint } }

// WithProcessExitCode preserves an operation's process status across wrappers.
func WithProcessExitCode(code int) Opt { return func(d *Diag) { d.ProcessExitCode = code } }

// WithCause sets a safe cause for the canonical machine error envelope.
func WithCause(cause string) Opt { return func(d *Diag) { d.Cause = cause } }

// WithMeta merges structured context into Meta.
func WithMeta(meta map[string]any) Opt {
	return func(d *Diag) {
		if d.Meta == nil {
			d.Meta = make(map[string]any, len(meta))
		}
		for k, v := range meta {
			d.Meta[k] = v
		}
	}
}

// WithCorrelationID attaches the ID that joins a safe result to its log entry.
func WithCorrelationID(id string) Opt { return func(d *Diag) { d.CorrelationID = id } }

// New builds a Diag and uses the platform catalog hint for a known code.
func New(code, message string, opts ...Opt) *Diag {
	d := &Diag{Code: code, Message: message}
	if entry, ok := LookupCatalog(code); ok {
		d.Hint = entry.Hint
	}
	for _, o := range opts {
		o(d)
	}
	return d
}

// Wrap builds a Diag wrapping an underlying error (preserved for Unwrap).
func Wrap(err error, code, message string, opts ...Opt) *Diag {
	d := New(code, message, opts...)
	d.cause = err
	return d
}

// FromError converts an untyped failure at a CLI boundary into a safe
// platform error and writes its stack only to the structured log.
func FromError(err error) *Diag {
	return FromErrorWithLogger(err, slog.New(slog.NewJSONHandler(os.Stderr, nil)))
}

// FromErrorWithLogger normalizes an untyped boundary failure and sends its
// raw error and stack to the supplied structured logger only.
func FromErrorWithLogger(err error, logger *slog.Logger) *Diag {
	if err == nil {
		return nil
	}
	if logger == nil {
		logger = slog.New(slog.NewJSONHandler(os.Stderr, nil))
	}
	var typed *Diag
	if errors.As(err, &typed) && typed != nil {
		entry, known := LookupCatalog(typed.Code)
		if !known {
			correlationID := newCorrelationID()
			logger.Error("CLI command failed unexpectedly",
				"correlationId", correlationID, "error", typed.Error(), "stack", string(debug.Stack()))
			return New("KB_RUNTIME_UNEXPECTED", "An unexpected error occurred.", WithCorrelationID(correlationID))
		}
		normalized := *typed
		if normalized.Hint == "" {
			normalized.Hint = entry.Hint
		}
		if normalized.CorrelationID == "" {
			normalized.CorrelationID = newCorrelationID()
		}
		return &normalized
	}
	correlationID := newCorrelationID()
	logger.Error("CLI command failed unexpectedly",
		"correlationId", correlationID, "error", err.Error(), "stack", string(debug.Stack()))
	return New("KB_RUNTIME_UNEXPECTED", "An unexpected error occurred.", WithCorrelationID(correlationID))
}

// MarshalJSON serializes diagnostics through the canonical platform envelope.
func (d *Diag) MarshalJSON() ([]byte, error) {
	envelope, err := ToEnvelope(d)
	if err != nil {
		return nil, err
	}
	return json.Marshal(envelope)
}

// UnmarshalJSON accepts only the canonical platform error envelope.
func (d *Diag) UnmarshalJSON(data []byte) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	for _, field := range []string{"code", "area", "stage", "severity", "retryable", "message", "hint"} {
		if _, exists := fields[field]; !exists {
			return fmt.Errorf("canonical error envelope is missing required field %q", field)
		}
	}
	var envelope Envelope
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&envelope); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return fmt.Errorf("decode canonical error envelope trailing data: %v", err)
	}
	entry, ok := LookupCatalog(envelope.Code)
	if !ok {
		return fmt.Errorf("error envelope code %q is absent from the platform catalog", envelope.Code)
	}
	if envelope.Area != entry.Area || envelope.Stage != entry.Stage || envelope.Severity != entry.Severity || envelope.Retryable != entry.Retryable {
		return fmt.Errorf("error envelope metadata for %q does not match the platform catalog", envelope.Code)
	}
	if envelope.Message == "" || envelope.Hint == "" {
		return fmt.Errorf("error envelope message and hint are required")
	}
	*d = *New(envelope.Code, envelope.Message, WithHint(envelope.Hint), WithCause(envelope.Cause), WithCorrelationID(envelope.CorrelationID))
	if len(envelope.Details) > 0 {
		d.Meta = make(map[string]any, len(envelope.Details))
		for key, value := range envelope.Details {
			d.Meta[key] = value
		}
	}
	d.Actions = append([]Action(nil), envelope.Actions...)
	d.Docs = envelope.Docs
	return nil
}

func newCorrelationID() string {
	var rawID [16]byte
	if _, err := rand.Read(rawID[:]); err != nil {
		return fmt.Sprintf("%x", time.Now().UnixNano())
	}
	return hex.EncodeToString(rawID[:])
}
