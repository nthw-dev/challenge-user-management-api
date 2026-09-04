//go:build integration

package integration

import (
	"fmt"
	"net/http"
	"sync"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.mongodb.org/mongo-driver/bson"
)

// These specs prove the claims the design rests on, with real concurrency against real MongoDB:
// that the unique index — not a check-then-insert — is what makes an email unique, and that claiming the old refresh
// token before issuing the new one is what makes a rotation race yield exactly one winner. A comment can assert either;
// only a test can show it.
var _ = Describe("under concurrent requests, on real MongoDB", func() {
	const (
		password = "Str0ng-Pass!"
		fanOut   = 20
	)
	var sys system

	BeforeEach(func() { sys = bootSystem() })

	// fire runs fn in fanOut goroutines released together, and tallies the status code each one saw.
	fire := func(fn func(i int) int) map[int]int {
		var (
			wg    sync.WaitGroup
			mu    sync.Mutex
			start = make(chan struct{})
			seen  = map[int]int{}
		)
		for i := 0; i < fanOut; i++ {
			wg.Add(1)
			go func(i int) {
				defer GinkgoRecover()
				defer wg.Done()
				<-start
				code := fn(i)
				mu.Lock()
				seen[code]++
				mu.Unlock()
			}(i)
		}
		close(start)
		wg.Wait()
		return seen
	}

	It("lets exactly one of twenty simultaneous registrations of one email through — the index is the arbiter", func() {
		const email = "race@example.com"

		seen := fire(func(int) int {
			return sys.call(http.MethodPost, "/api/v1/auth/register",
				`{"name":"Racer","email":"`+email+`","password":"`+password+`"}`, "").Code
		})

		Expect(seen).To(Equal(map[int]int{http.StatusCreated: 1, http.StatusConflict: fanOut - 1}),
			"one 201, the rest 409 — never a 500, never two winners")
		Expect(sys.userRows(bson.M{"email": email})).To(BeEquivalentTo(1))
	})

	It("lets exactly one of twenty simultaneous refreshes of one token win — the claim comes before the issue", func() {
		userID := sys.register("Racer", "refresh-race@example.com", password)
		session := sys.login("refresh-race@example.com", password)
		token, _ := session["refresh_token"].(string)

		seen := fire(func(int) int {
			return sys.call(http.MethodPost, "/api/v1/auth/refresh", `{"refresh_token":"`+token+`"}`, "").Code
		})

		Expect(seen).To(HaveKeyWithValue(http.StatusOK, 1), "exactly one winner")
		Expect(seen).To(HaveKeyWithValue(http.StatusUnauthorized, fanOut-1), "every loser is told the token is unusable")
		Expect(seen).NotTo(HaveKey(http.StatusInternalServerError))
		// The original row plus the one the winner stored: a loser never gets as far as storing anything.
		Expect(sys.refreshRows(userID, bson.M{})).To(BeEquivalentTo(2))
		// At most the winner's token is live. Not "exactly": a loser that reads the row after the winner revoked it
		// legitimately takes the reuse path and wipes the user's sessions, the winner's included — that is the design.
		Expect(sys.refreshRows(userID, bson.M{"revoked_at": bson.M{"$exists": false}})).To(BeNumerically("<=", 1))
	})

	It("lets exactly one of two users take the same new email at once — each patching their own account", func() {
		const wanted = "wanted@example.com"
		type actor struct{ id, access string }
		racers := make([]actor, 2)
		for i := range racers {
			email := fmt.Sprintf("patch-race-%d@example.com", i)
			id := sys.register("Racer", email, password)
			session := sys.login(email, password)
			access, _ := session["access_token"].(string)
			racers[i] = actor{id: id, access: access}
		}

		var (
			wg    sync.WaitGroup
			mu    sync.Mutex
			start = make(chan struct{})
			seen  = map[int]int{}
		)
		for _, r := range racers {
			wg.Add(1)
			go func(r actor) {
				defer GinkgoRecover()
				defer wg.Done()
				<-start
				code := sys.call(http.MethodPatch, "/api/v1/users/"+r.id, `{"email":"`+wanted+`"}`, r.access).Code
				mu.Lock()
				seen[code]++
				mu.Unlock()
			}(r)
		}
		close(start)
		wg.Wait()

		Expect(seen).To(Equal(map[int]int{http.StatusOK: 1, http.StatusConflict: 1}))
		Expect(sys.userRows(bson.M{"email": wanted})).To(BeEquivalentTo(1))
	})
})
