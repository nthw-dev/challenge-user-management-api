package worker_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// TestWorker runs the Ginkgo specs of this package. The worker is driven by time, so what its specs assert is
// asynchronous by nature — Gomega's Eventually and Consistently say "within a bound" and "for the whole window"
// without a sleep, which is the reason the specs live here rather than in the table-driven file.
func TestWorker(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "worker: the user counter")
}
