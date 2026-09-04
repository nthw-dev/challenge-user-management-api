package app_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/stretchr/testify/mock"

	"github.com/nthw-dev/user-management-api/internal/app"
	"github.com/nthw-dev/user-management-api/internal/app/apptest"
	"github.com/nthw-dev/user-management-api/internal/app/apptest/mocks"
	"github.com/nthw-dev/user-management-api/internal/domain/user"
)

const (
	accessTTL  = 15 * time.Minute
	refreshTTL = 168 * time.Hour
	decoyHash  = "$decoy-hash$"
)

// sha256Hex mirrors how the use case stores a refresh token, so a spec can hand the mock the hash of a raw token.
func sha256Hex(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

// authPorts is every outbound port AuthService talks to, as a mock. An unexpected call on any of them fails the spec,
// and every expectation is asserted when the spec ends — which is what makes "this must not be called" checkable.
type authPorts struct {
	repo    *mocks.UserRepository
	refresh *mocks.RefreshTokenRepository
	hasher  *mocks.PasswordHasher
	issuer  *mocks.TokenIssuer
	clock   *apptest.Clock
}

func newAuthPorts() authPorts {
	t := GinkgoT()
	return authPorts{
		repo:    mocks.NewUserRepository(t),
		refresh: mocks.NewRefreshTokenRepository(t),
		hasher:  mocks.NewPasswordHasher(t),
		issuer:  mocks.NewTokenIssuer(t),
		clock:   apptest.NewClock(),
	}
}

// service builds the AuthService. Construction hashes the timing decoy, which is the one Hash call every spec expects.
func (p authPorts) service() *app.AuthService {
	p.hasher.EXPECT().Hash(mock.AnythingOfType("string")).Return(decoyHash, nil).Once()

	svc, err := app.NewAuthService(p.repo, p.refresh, p.hasher, p.issuer, p.clock,
		app.AuthConfig{AccessTTL: accessTTL, RefreshTTL: refreshTTL})
	Expect(err).NotTo(HaveOccurred())
	return svc
}

var _ = Describe("AuthService.Login", func() {
	var (
		ctx    = context.Background()
		ports  authPorts
		svc    *app.AuthService
		seeded *user.User
	)

	BeforeEach(func() {
		ports = newAuthPorts()
		svc = ports.service()
		seeded = apptest.SeededUser()
	})

	When("the password is right", func() {
		It("compares against the stored hash, issues a session, and stores only the hash of the refresh token", func() {
			ports.repo.EXPECT().FindByEmail(mock.Anything, user.Email("natthawat@example.com")).Return(seeded, nil).Once()
			ports.hasher.EXPECT().Compare(seeded.PasswordHash, apptest.SeededPassword).Return(nil).Once()
			ports.issuer.EXPECT().Issue(seeded.ID, accessTTL).Return("access-token", nil).Once()

			var stored app.RefreshToken
			ports.refresh.EXPECT().Store(mock.Anything, mock.AnythingOfType("app.RefreshToken")).
				Run(func(_ context.Context, rt app.RefreshToken) { stored = rt }).
				Return(nil).Once()

			// The email arrives in mixed case: it must be normalized before the repository sees it.
			session, err := svc.Login(ctx, app.LoginInput{Email: "Natthawat@Example.com", Password: apptest.SeededPassword})

			Expect(err).NotTo(HaveOccurred())
			Expect(session.AccessToken).To(Equal("access-token"))
			Expect(session.ExpiresIn).To(Equal(accessTTL))
			Expect(session.User).To(BeIdenticalTo(seeded))
			Expect(session.RefreshToken).To(HaveLen(64), "32 random bytes, hex encoded")

			Expect(stored.UserID).To(Equal(seeded.ID))
			Expect(stored.TokenHash).To(Equal(sha256Hex(session.RefreshToken)), "the row holds the hash, never the raw token")
			Expect(stored.TokenHash).NotTo(Equal(session.RefreshToken))
			Expect(stored.CreatedAt).To(Equal(apptest.SeededAt))
			Expect(stored.ExpiresAt).To(Equal(apptest.SeededAt.Add(refreshTTL)))
			Expect(stored.RevokedAt).To(BeNil())
		})
	})

	When("the password is wrong", func() {
		It("answers ErrInvalidCredentials and issues nothing", func() {
			ports.repo.EXPECT().FindByEmail(mock.Anything, seeded.Email).Return(seeded, nil).Once()
			ports.hasher.EXPECT().Compare(seeded.PasswordHash, "wrong").Return(app.ErrPasswordMismatch).Once()

			_, err := svc.Login(ctx, app.LoginInput{Email: seeded.Email.String(), Password: "wrong"})

			Expect(err).To(MatchError(user.ErrInvalidCredentials))
			ports.issuer.AssertNotCalled(GinkgoT(), "Issue", mock.Anything, mock.Anything)
			ports.refresh.AssertNotCalled(GinkgoT(), "Store", mock.Anything, mock.Anything)
		})
	})

	When("the email is unknown", func() {
		It("answers the same ErrInvalidCredentials, and still runs one comparison so the timing matches a real login", func() {
			ports.repo.EXPECT().FindByEmail(mock.Anything, user.Email("nobody@example.com")).Return(nil, user.ErrNotFound).Once()
			// The decoy comparison is the timing guard — a fake cannot observe it, a mock can.
			ports.hasher.EXPECT().Compare(decoyHash, mock.AnythingOfType("string")).Return(app.ErrPasswordMismatch).Once()

			_, err := svc.Login(ctx, app.LoginInput{Email: "nobody@example.com", Password: "whatever"})

			Expect(err).To(MatchError(user.ErrInvalidCredentials))
		})
	})

	When("the email is malformed", func() {
		It("never asks the repository, and still runs the decoy comparison", func() {
			ports.hasher.EXPECT().Compare(decoyHash, mock.AnythingOfType("string")).Return(app.ErrPasswordMismatch).Once()

			_, err := svc.Login(ctx, app.LoginInput{Email: "not-an-email", Password: "whatever"})

			Expect(err).To(MatchError(user.ErrInvalidCredentials))
			Expect(err).NotTo(MatchError(user.ErrValidation{}), "which part was wrong must not be revealed")
			ports.repo.AssertNotCalled(GinkgoT(), "FindByEmail", mock.Anything, mock.Anything)
		})
	})

	When("the repository fails", func() {
		It("passes the failure up untouched — an outage must not look like a wrong password", func() {
			outage := errors.New("mongo: server selection timeout")
			ports.repo.EXPECT().FindByEmail(mock.Anything, mock.Anything).Return(nil, outage).Once()

			_, err := svc.Login(ctx, app.LoginInput{Email: seeded.Email.String(), Password: apptest.SeededPassword})

			Expect(err).To(MatchError(outage))
			Expect(err).NotTo(MatchError(user.ErrInvalidCredentials))
			ports.hasher.AssertNotCalled(GinkgoT(), "Compare", mock.Anything, mock.Anything)
		})
	})

	When("the comparison itself fails", func() {
		It("surfaces the infrastructure error instead of a 401", func() {
			corrupt := errors.New("bcrypt: hashedSecret too short")
			ports.repo.EXPECT().FindByEmail(mock.Anything, seeded.Email).Return(seeded, nil).Once()
			ports.hasher.EXPECT().Compare(seeded.PasswordHash, apptest.SeededPassword).Return(corrupt).Once()

			_, err := svc.Login(ctx, app.LoginInput{Email: seeded.Email.String(), Password: apptest.SeededPassword})

			Expect(err).To(MatchError(corrupt))
			Expect(err).NotTo(MatchError(user.ErrInvalidCredentials))
		})
	})

	When("the token issuer fails", func() {
		It("stores no refresh token", func() {
			ports.repo.EXPECT().FindByEmail(mock.Anything, seeded.Email).Return(seeded, nil).Once()
			ports.hasher.EXPECT().Compare(seeded.PasswordHash, apptest.SeededPassword).Return(nil).Once()
			ports.issuer.EXPECT().Issue(seeded.ID, accessTTL).Return("", errors.New("signing key unavailable")).Once()

			_, err := svc.Login(ctx, app.LoginInput{Email: seeded.Email.String(), Password: apptest.SeededPassword})

			Expect(err).To(MatchError(ContainSubstring("issue access token")))
			ports.refresh.AssertNotCalled(GinkgoT(), "Store", mock.Anything, mock.Anything)
		})
	})
})

var _ = Describe("AuthService.Refresh", func() {
	const raw = "raw-refresh-token-presented-by-the-caller"

	var (
		ctx    = context.Background()
		ports  authPorts
		svc    *app.AuthService
		seeded *user.User
		now    time.Time
		live   *app.RefreshToken
	)

	BeforeEach(func() {
		ports = newAuthPorts()
		svc = ports.service()
		seeded = apptest.SeededUser()
		now = ports.clock.Now()
		live = &app.RefreshToken{
			ID: "rt-old", UserID: seeded.ID, TokenHash: sha256Hex(raw),
			CreatedAt: now.Add(-time.Hour), ExpiresAt: now.Add(time.Hour),
		}
	})

	// expectLookup is the first half of every rotation: the token is found by its hash, and the user behind it is read.
	expectLookup := func() {
		ports.refresh.EXPECT().FindByHash(mock.Anything, sha256Hex(raw)).Return(live, nil).Once()
		ports.repo.EXPECT().FindByID(mock.Anything, seeded.ID).Return(seeded, nil).Once()
	}

	When("the token is live", func() {
		It("claims the old token first, and only then issues and stores the new one", func() {
			var calls []string
			expectLookup()
			ports.refresh.EXPECT().Revoke(mock.Anything, "rt-old", now).
				Run(func(context.Context, string, time.Time) { calls = append(calls, "Revoke") }).
				Return(nil).Once()
			ports.issuer.EXPECT().Issue(seeded.ID, accessTTL).Return("new-access", nil).Once()
			ports.refresh.EXPECT().Store(mock.Anything, mock.AnythingOfType("app.RefreshToken")).
				Run(func(context.Context, app.RefreshToken) { calls = append(calls, "Store") }).
				Return(nil).Once()

			session, err := svc.Refresh(ctx, raw)

			Expect(err).NotTo(HaveOccurred())
			Expect(session.AccessToken).To(Equal("new-access"))
			Expect(session.RefreshToken).NotTo(Equal(raw), "a new token every time")
			Expect(session.User).To(BeIdenticalTo(seeded))
			Expect(calls).To(Equal([]string{"Revoke", "Store"}), "the claim comes first, so two racing requests cannot both win")
		})
	})

	When("another request claimed the token first", func() {
		It("answers ErrUnauthorized without issuing anything — and without wiping the user's sessions", func() {
			expectLookup()
			// The compare-and-swap found the token already revoked: the other request won.
			ports.refresh.EXPECT().Revoke(mock.Anything, "rt-old", now).Return(app.ErrRefreshTokenNotFound).Once()

			_, err := svc.Refresh(ctx, raw)

			Expect(err).To(MatchError(user.ErrUnauthorized))
			ports.issuer.AssertNotCalled(GinkgoT(), "Issue", mock.Anything, mock.Anything)
			ports.refresh.AssertNotCalled(GinkgoT(), "Store", mock.Anything, mock.Anything)
			ports.refresh.AssertNotCalled(GinkgoT(), "RevokeAllForUser", mock.Anything, mock.Anything, mock.Anything)
		})
	})

	When("storing the new token fails", func() {
		It("has already revoked the old one — the caller must log in again, but no live token is left behind", func() {
			expectLookup()
			ports.refresh.EXPECT().Revoke(mock.Anything, "rt-old", now).Return(nil).Once()
			ports.issuer.EXPECT().Issue(seeded.ID, accessTTL).Return("new-access", nil).Once()
			ports.refresh.EXPECT().Store(mock.Anything, mock.Anything).Return(errors.New("write concern timeout")).Once()

			_, err := svc.Refresh(ctx, raw)

			Expect(err).To(MatchError(ContainSubstring("write concern timeout")))
			Expect(err).NotTo(MatchError(user.ErrUnauthorized), "an infrastructure error must not masquerade as a bad token")
		})
	})

	When("revoking the old token fails for any reason other than a lost claim", func() {
		It("reports the failure and issues nothing", func() {
			expectLookup()
			ports.refresh.EXPECT().Revoke(mock.Anything, "rt-old", now).Return(errors.New("connection reset")).Once()

			session, err := svc.Refresh(ctx, raw)

			Expect(err).To(MatchError(ContainSubstring("connection reset")))
			Expect(err).NotTo(MatchError(user.ErrUnauthorized))
			Expect(session).To(BeNil())
			ports.issuer.AssertNotCalled(GinkgoT(), "Issue", mock.Anything, mock.Anything)
			ports.refresh.AssertNotCalled(GinkgoT(), "Store", mock.Anything, mock.Anything)
		})
	})

	When("the token was already rotated", func() {
		var reused app.RefreshToken

		BeforeEach(func() {
			revokedAt := now.Add(-time.Minute)
			reused = *live
			reused.RevokedAt = &revokedAt
		})

		It("treats it as a leaked copy: wipes every session of that user and answers ErrUnauthorized", func() {
			ports.refresh.EXPECT().FindByHash(mock.Anything, sha256Hex(raw)).Return(&reused, nil).Once()
			ports.refresh.EXPECT().RevokeAllForUser(mock.Anything, seeded.ID, now).Return(nil).Once()

			_, err := svc.Refresh(ctx, raw)

			Expect(err).To(MatchError(user.ErrUnauthorized))
			ports.repo.AssertNotCalled(GinkgoT(), "FindByID", mock.Anything, mock.Anything)
			ports.issuer.AssertNotCalled(GinkgoT(), "Issue", mock.Anything, mock.Anything)
		})

		It("surfaces a failure to wipe the sessions instead of hiding it behind a 401", func() {
			ports.refresh.EXPECT().FindByHash(mock.Anything, sha256Hex(raw)).Return(&reused, nil).Once()
			ports.refresh.EXPECT().RevokeAllForUser(mock.Anything, seeded.ID, now).Return(errors.New("mongo: not primary")).Once()

			_, err := svc.Refresh(ctx, raw)

			Expect(err).To(MatchError(ContainSubstring("not primary")))
			Expect(err).NotTo(MatchError(user.ErrUnauthorized))
		})
	})

	When("the token has expired", func() {
		It("answers ErrUnauthorized and touches nothing else", func() {
			expired := *live
			expired.ExpiresAt = now // exactly now is no longer "before"
			ports.refresh.EXPECT().FindByHash(mock.Anything, sha256Hex(raw)).Return(&expired, nil).Once()

			_, err := svc.Refresh(ctx, raw)

			Expect(err).To(MatchError(user.ErrUnauthorized))
			// No other expectation was set — any other call would have failed the spec.
		})
	})

	DescribeTable("a token that cannot be used answers ErrUnauthorized, without saying why",
		func(presented string, lookup error) {
			if presented != "" {
				ports.refresh.EXPECT().FindByHash(mock.Anything, sha256Hex(presented)).Return(nil, lookup).Once()
			}

			_, err := svc.Refresh(ctx, presented)

			Expect(err).To(MatchError(user.ErrUnauthorized))
		},
		Entry("a value that was never issued", "garbage-value", app.ErrRefreshTokenNotFound),
		Entry("an empty value — the repository is not even asked", "", nil),
	)

	When("the lookup fails", func() {
		It("passes the infrastructure error through untouched", func() {
			outage := errors.New("mongo: server selection timeout")
			ports.refresh.EXPECT().FindByHash(mock.Anything, sha256Hex(raw)).Return(nil, outage).Once()

			_, err := svc.Refresh(ctx, raw)

			Expect(err).To(MatchError(outage))
			Expect(err).NotTo(MatchError(user.ErrUnauthorized))
		})
	})

	When("the user behind the token no longer exists", func() {
		It("answers ErrUnauthorized and issues nothing", func() {
			ports.refresh.EXPECT().FindByHash(mock.Anything, sha256Hex(raw)).Return(live, nil).Once()
			ports.repo.EXPECT().FindByID(mock.Anything, seeded.ID).Return(nil, user.ErrNotFound).Once()

			_, err := svc.Refresh(ctx, raw)

			Expect(err).To(MatchError(user.ErrUnauthorized))
			ports.issuer.AssertNotCalled(GinkgoT(), "Issue", mock.Anything, mock.Anything)
			ports.refresh.AssertNotCalled(GinkgoT(), "Store", mock.Anything, mock.Anything)
		})
	})
})
