package mutation

import (
	"os"
	"testing"
)

// readSelf reads this suite's own source, so the suite can assert
// things about its own shape.
func readSelf(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile("mutation_test.go")
	if err != nil {
		t.Fatalf("reading the mutation suite: %v", err)
	}
	return string(b)
}
