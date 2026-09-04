package worker_test

import (
	"context"
	"errors"
	"sync/atomic"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/stretchr/testify/mock"

	"github.com/nthw-dev/user-management-api/internal/adapter/inbound/worker"
	"github.com/nthw-dev/user-management-api/internal/app/apptest"
	"github.com/nthw-dev/user-management-api/internal/app/apptest/mocks"
)

var _ = Describe("UserCounter", func() {
	const interval = 5 * time.Millisecond

	var (
		users *mocks.UserUseCase
		seen  chan int64 // what the observer received, in order
	)

	// observe hands a value to the spec without ever blocking the worker — a full buffer drops, never stalls.
	observe := func(n int64) {
		select {
		case seen <- n:
		default:
		}
	}

	// start runs the worker in the background and returns the channel its Run result lands on, plus a way to stop it.
	start := func(w *worker.UserCounter) (done <-chan error, stop context.CancelFunc) {
		ctx, cancel := context.WithCancel(context.Background())
		DeferCleanup(cancel)
		ch := make(chan error, 1)
		go func() { ch <- w.Run(ctx) }()
		return ch, cancel
	}

	BeforeEach(func() {
		users = mocks.NewUserUseCase(GinkgoT())
		seen = make(chan int64, 1000)
	})

	It("counts on every tick and hands each number to the observer", func() {
		users.EXPECT().Count(mock.Anything).Return(42, nil)
		done, stop := start(worker.NewUserCounter(users, apptest.DiscardLogger(), interval, observe))

		Eventually(seen).Should(Receive(Equal(int64(42))))
		Eventually(seen).Should(Receive(Equal(int64(42))), "and again on the next round")

		stop()
		Eventually(done).Should(Receive(BeNil()), "shutting down on command is not a failure")
	})

	It("keeps counting after a failed round, and emits nothing for the round that failed", func() {
		users.EXPECT().Count(mock.Anything).Return(0, errors.New("mongo is down")).Once()
		users.EXPECT().Count(mock.Anything).Return(7, nil)
		done, stop := start(worker.NewUserCounter(users, apptest.DiscardLogger(), interval, observe))

		var first int64
		Eventually(seen).Should(Receive(&first))
		Expect(first).To(Equal(int64(7)), "the failed round must not have produced a value")

		stop()
		Eventually(done).Should(Receive(BeNil()))
	})

	It("emits nothing at all while every count fails, and still does not stop", func() {
		users.EXPECT().Count(mock.Anything).Return(0, errors.New("mongo is down"))
		done, stop := start(worker.NewUserCounter(users, apptest.DiscardLogger(), interval, observe))

		Consistently(seen, 50*time.Millisecond).ShouldNot(Receive())
		Consistently(done).ShouldNot(Receive(), "a statistics job must not take the service down")

		stop()
		Eventually(done).Should(Receive(BeNil()))
	})

	It("stops as soon as the context is cancelled, without waiting out the interval", func() {
		// No expectation on Count: with an interval of an hour, a call would mean the loop ticked when it should not have.
		done, stop := start(worker.NewUserCounter(users, apptest.DiscardLogger(), time.Hour, observe))

		stop()

		Eventually(done).WithTimeout(time.Second).Should(Receive(BeNil()))
	})

	It("bounds every round with its own deadline, and tolerates having no observer", func() {
		var remaining atomic.Int64 // nanoseconds left on the deadline the count was given
		users.EXPECT().Count(mock.Anything).RunAndReturn(func(ctx context.Context) (int64, error) {
			if deadline, ok := ctx.Deadline(); ok {
				remaining.Store(int64(time.Until(deadline)))
			}
			return 1, nil
		})
		done, stop := start(worker.NewUserCounter(users, apptest.DiscardLogger(), interval, nil))

		Eventually(remaining.Load).Should(BeNumerically(">", 0), "each count must carry a deadline")
		Expect(remaining.Load()).To(BeNumerically("<=", int64(5*time.Second)), "and a short one, so a stalled round cannot overlap the next")

		stop()
		Eventually(done).Should(Receive(BeNil()))
	})
})
