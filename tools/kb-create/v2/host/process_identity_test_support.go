//go:build kb_create_test_support

package host

// SetProcessIdentityReaderForTest replaces the identity reader in a test-built
// supervisor so a detached lifecycle can exercise the undetermined result.
func SetProcessIdentityReaderForTest(runner *ProcessRunner, reader func(int) (string, error)) {
	runner.identityReader = reader
}
