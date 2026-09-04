//go:build integration

package integration

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// TestIntegrationSpecs runs the Ginkgo specs of this package on the same MongoDB container TestMain starts.
//
// The table-driven tests next door check each repository method on its own; the specs tell a story that runs
// through the whole system — router, use cases, bcrypt, JWT and both repositories — in the order a real client would.
func TestIntegrationSpecs(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "integration: the real system on real MongoDB")
}
