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
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
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
	// Meta carries structured context (paths, hosts, per-action failures, …).
	Meta          map[string]any `json:"meta,omitempty"`
	CorrelationID string         `json:"correlationId,omitempty"`

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
	for _, o := range opts {
		o(d)
	}
	if entry, ok := LookupCatalog(code); ok {
		d.Hint = entry.Hint
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
		normalized.Hint = entry.Hint
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

func newCorrelationID() string {
	var rawID [16]byte
	if _, err := rand.Read(rawID[:]); err != nil {
		return fmt.Sprintf("%x", time.Now().UnixNano())
	}
	return hex.EncodeToString(rawID[:])
}
