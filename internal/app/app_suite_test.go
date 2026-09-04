package app_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// TestApp runs the Ginkgo specs of this package; `go test` picks them up next to the table-driven tests.
//
// The two styles split the work: the table-driven tests describe *state* through the in-memory fakes (what ends up
// stored, what comes back), while the specs describe *interaction* through mockery-generated mocks — the order of
// calls, the exact arguments a port receives, and the calls that must never happen. A fake cannot say "Revoke was not
// called"; a mock cannot say "the second page has no duplicates". Both are needed.
func TestApp(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "app: use cases and their ports")
}
