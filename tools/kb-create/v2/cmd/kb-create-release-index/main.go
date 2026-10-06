// kb-create-release-index is the publish-time release-index sealer. It
// turns a normalized manifest export into a sealed immutable release index;
// it is not an installer and never reads a user's platform directory.
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/kb-labs/clikit/diag"
	"github.com/kb-labs/create/v2/catalog"
	"github.com/kb-labs/create/v2/contracts"
	"github.com/kb-labs/create/v2/release"
	"github.com/kb-labs/create/v2/scenario"
)

func main() {
	input := flag.String("input", "", "normalized V2 release-index export JSON")
	output := flag.String("output", "", "sealed immutable release-index JSON")
	manifestRoot := flag.String("manifest-root", "", "staging root containing exact V2 package manifests")
	flag.Parse()
	if err := run(*input, *output, *manifestRoot); err != nil {
		failure, envelopeErr := sealingFailureEnvelope(err)
		if envelopeErr != nil {
			fmt.Fprintf(os.Stderr, "kb-create-release-index: failed to format diagnostic: %v\n", envelopeErr)
			os.Exit(2)
		}
		_ = json.NewEncoder(os.Stderr).Encode(map[string]any{"ok": false, "error": failure})
		os.Exit(2)
	}
}

type sealingFailure struct {
	gate, rule, hint string
	err              error
}

func (failure *sealingFailure) Error() string { return failure.err.Error() }
func (failure *sealingFailure) Unwrap() error { return failure.err }

func sealingFailureEnvelope(err error) (diag.Envelope, error) {
	var failure *sealingFailure
	if !errors.As(err, &failure) {
		failure = &sealingFailure{
			gate: "release-index validation", rule: err.Error(),
			hint: "Fix the release inputs named in the cause, then rerun release-index preparation.",
			err:  err,
		}
	}
	diagnostic := contracts.NewDiag(contracts.CodeReleaseIndexSealFailed, "could not seal V2 release index", failure.hint, failure.err)
	diagnostic.Cause = failure.rule
	diagnostic.Meta = map[string]any{"gate": failure.gate, "rule": failure.rule}
	return diag.ToEnvelope(diagnostic)
}

func run(input, output, manifestRoot string) error {
	if input == "" || output == "" || manifestRoot == "" {
		return fmt.Errorf("--input, --output and --manifest-root are required")
	}
	data, err := os.ReadFile(input)
	if err != nil {
		return err
	}
	var source catalog.Catalog
	if err := json.Unmarshal(data, &source); err != nil {
		return err
	}
	source, err = release.EnrichWithManifests(source, manifestRoot)
	if err != nil {
		hint := "Fix the manifest named in the cause, then rerun release-index preparation."
		var rule *release.ManifestRuleError
		if errors.As(err, &rule) {
			hint = rule.Hint
		}
		return &sealingFailure{
			gate: "manifest validation", rule: err.Error(),
			hint: hint,
			err:  err,
		}
	}
	if err := scenario.ValidateCatalog(source); err != nil {
		return fmt.Errorf("built-in scenario validation: %w", err)
	}
	// Exports intentionally omit release-controlled fields. Schema and digest
	// are assigned here, after all manifest data has been collected.
	source.Schema, source.Digest = "", ""
	sealed, err := catalog.Seal(source)
	if err != nil {
		return err
	}
	encoded, err := json.MarshalIndent(sealed, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(output), 0o750); err != nil {
		return err
	}
	temporary := output + ".tmp"
	if err := os.WriteFile(temporary, append(encoded, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(temporary, output)
}
