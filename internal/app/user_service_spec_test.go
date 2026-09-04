package app_test

import (
	"context"
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

var _ = Describe("UserService", func() {
	var (
		ctx     = context.Background()
		repo    *mocks.UserRepository
		refresh *mocks.RefreshTokenRepository
		hasher  *mocks.PasswordHasher
		clock   *apptest.Clock
		now     time.Time
		svc     *app.UserService
		seeded  *user.User
	)

	BeforeEach(func() {
		repo = mocks.NewUserRepository(GinkgoT())
		refresh = mocks.NewRefreshTokenRepository(GinkgoT())
		hasher = mocks.NewPasswordHasher(GinkgoT())
		clock = apptest.NewClock()
		now = clock.Now()
		svc = app.NewUserService(repo, refresh, hasher, clock)
		seeded = apptest.SeededUser()
	})

	Describe("Create", func() {
		It("hashes the password and hands the repository an entity that has already passed the invariants", func() {
			hasher.EXPECT().Hash(apptest.SeededPassword).Return("$hashed$", nil).Once()
			repo.EXPECT().Create(mock.Anything, mock.MatchedBy(func(u *user.User) bool {
				return u.ID == "" && // the repository assigns the id
					u.Name == "Somchai" &&
					u.Email == user.Email("somchai@example.com") && // normalized before it gets here
					u.PasswordHash == "$hashed$" &&
					u.CreatedAt.Equal(now) && u.UpdatedAt.Equal(now)
			})).Return(nil).Once()

			u, err := svc.Create(ctx, app.CreateUserInput{Name: "Somchai", Email: "Somchai@Example.com", Password: apptest.SeededPassword})

			Expect(err).NotTo(HaveOccurred())
			Expect(u.Email.String()).To(Equal("somchai@example.com"))
			Expect(u.PasswordHash).NotTo(ContainSubstring(apptest.SeededPassword))
		})

		It("passes the unique index's verdict through as ErrEmailTaken, with no query beforehand", func() {
			hasher.EXPECT().Hash(apptest.SeededPassword).Return("$hashed$", nil).Once()
			repo.EXPECT().Create(mock.Anything, mock.Anything).Return(user.ErrEmailTaken).Once()

			_, err := svc.Create(ctx, app.CreateUserInput{Name: "Somchai", Email: "taken@example.com", Password: apptest.SeededPassword})

			Expect(err).To(MatchError(user.ErrEmailTaken))
			repo.AssertNotCalled(GinkgoT(), "FindByEmail", mock.Anything, mock.Anything)
		})

		It("touches neither the hasher nor the repository when the input fails validation", func() {
			_, err := svc.Create(ctx, app.CreateUserInput{Name: "Somchai", Email: "not-an-email", Password: apptest.SeededPassword})

			Expect(err).To(MatchError(user.ErrValidation{}))
			// No expectation was set on either mock: a call would have failed the spec.
		})

		It("does not write when hashing fails", func() {
			hasher.EXPECT().Hash(apptest.SeededPassword).Return("", errors.New("bcrypt: cost out of range")).Once()

			_, err := svc.Create(ctx, app.CreateUserInput{Name: "Somchai", Email: "a@example.com", Password: apptest.SeededPassword})

			Expect(err).To(MatchError(ContainSubstring("cost out of range")))
			repo.AssertNotCalled(GinkgoT(), "Create", mock.Anything, mock.Anything)
		})
	})

	Describe("Get", func() {
		It("passes the id straight through and returns what the repository returns", func() {
			repo.EXPECT().FindByID(mock.Anything, apptest.SeededID).Return(seeded, nil).Once()

			got, err := svc.Get(ctx, apptest.SeededID)

			Expect(err).NotTo(HaveOccurred())
			Expect(got).To(BeIdenticalTo(seeded))
		})

		It("rejects an empty id before the repository is asked", func() {
			_, err := svc.Get(ctx, "")

			var invalid user.ErrValidation
			Expect(errors.As(err, &invalid)).To(BeTrue())
			Expect(invalid.Field).To(Equal("id"))
		})

		It("passes an infrastructure error through untouched", func() {
			outage := errors.New("mongo: connection reset")
			repo.EXPECT().FindByID(mock.Anything, apptest.SeededID).Return(nil, outage).Once()

			_, err := svc.Get(ctx, apptest.SeededID)

			Expect(err).To(MatchError(outage))
		})
	})

	Describe("List", func() {
		It("resolves the filter before the repository sees it, and echoes the limit that was applied", func() {
			repo.EXPECT().List(mock.Anything, app.ListQuery{Limit: app.DefaultListLimit, Cursor: "cursor-1", Query: "som"}).
				Return(app.Page{Users: []user.User{*seeded}, NextCursor: "cursor-2"}, nil).Once()

			page, err := svc.List(ctx, app.ListFilter{Cursor: "cursor-1", Query: "som"})

			Expect(err).NotTo(HaveOccurred())
			Expect(page.Limit).To(Equal(app.DefaultListLimit), "the default is filled in by the use case, not the repository")
			Expect(page.NextCursor).To(Equal("cursor-2"), "the repository's cursor is passed through opaque")
			Expect(page.HasMore()).To(BeTrue())
			Expect(page.Users).To(HaveLen(1))
		})

		It("passes a limit that was sent as it is", func() {
			limit := 5
			repo.EXPECT().List(mock.Anything, app.ListQuery{Limit: 5}).Return(app.Page{}, nil).Once()

			page, err := svc.List(ctx, app.ListFilter{Limit: &limit})

			Expect(err).NotTo(HaveOccurred())
			Expect(page.Limit).To(Equal(5))
			Expect(page.HasMore()).To(BeFalse())
		})

		DescribeTable("never asks the repository for a limit that is out of range",
			func(limit int) {
				_, err := svc.List(ctx, app.ListFilter{Limit: &limit})

				var invalid user.ErrValidation
				Expect(errors.As(err, &invalid)).To(BeTrue())
				Expect(invalid.Field).To(Equal("limit"))
				repo.AssertNotCalled(GinkgoT(), "List", mock.Anything, mock.Anything)
			},
			Entry("zero", 0),
			Entry("negative", -1),
			Entry("above the maximum", app.MaxListLimit+1),
		)

		It("passes a repository failure through untouched", func() {
			outage := errors.New("mongo: cursor timed out")
			repo.EXPECT().List(mock.Anything, mock.Anything).Return(app.Page{}, outage).Once()

			_, err := svc.List(ctx, app.ListFilter{})

			Expect(err).To(MatchError(outage))
		})
	})

	Describe("Update", func() {
		It("reads the current user, applies the change through the domain, and writes a patch holding only the fields sent", func() {
			newEmail := "New@Example.com"
			repo.EXPECT().FindByID(mock.Anything, apptest.SeededID).Return(seeded, nil).Once()
			repo.EXPECT().Update(mock.Anything, apptest.SeededID, mock.MatchedBy(func(p app.UpdatePatch) bool {
				return p.Name == nil && // not sent, so not in the patch
					p.Email != nil && p.Email.String() == "new@example.com" && // normalized by the domain
					p.UpdatedAt.Equal(now.UTC())
			})).Return(seeded, nil).Once()

			_, err := svc.Update(ctx, apptest.SeededID, apptest.SeededID, app.UpdateUserInput{Email: &newEmail})

			Expect(err).NotTo(HaveOccurred())
		})

		It("writes both fields when both were sent", func() {
			name, email := "Renamed", "renamed@example.com"
			repo.EXPECT().FindByID(mock.Anything, apptest.SeededID).Return(seeded, nil).Once()
			repo.EXPECT().Update(mock.Anything, apptest.SeededID, mock.MatchedBy(func(p app.UpdatePatch) bool {
				return p.Name != nil && *p.Name == "Renamed" && p.Email != nil && p.Email.String() == "renamed@example.com"
			})).Return(seeded, nil).Once()

			_, err := svc.Update(ctx, apptest.SeededID, apptest.SeededID, app.UpdateUserInput{Name: &name, Email: &email})

			Expect(err).NotTo(HaveOccurred())
		})

		It("does not write when the domain rejects the change", func() {
			bad := "not-an-email"
			repo.EXPECT().FindByID(mock.Anything, apptest.SeededID).Return(seeded, nil).Once()

			_, err := svc.Update(ctx, apptest.SeededID, apptest.SeededID, app.UpdateUserInput{Email: &bad})

			Expect(err).To(MatchError(user.ErrValidation{}))
			repo.AssertNotCalled(GinkgoT(), "Update", mock.Anything, mock.Anything, mock.Anything)
		})

		It("does not even read when nothing was sent", func() {
			_, err := svc.Update(ctx, apptest.SeededID, apptest.SeededID, app.UpdateUserInput{})

			var invalid user.ErrValidation
			Expect(errors.As(err, &invalid)).To(BeTrue())
			Expect(invalid.Field).To(Equal("body"))
		})

		It("passes ErrNotFound from the read through", func() {
			name := "x"
			repo.EXPECT().FindByID(mock.Anything, apptest.SeededID).Return(nil, user.ErrNotFound).Once()

			_, err := svc.Update(ctx, apptest.SeededID, apptest.SeededID, app.UpdateUserInput{Name: &name})

			Expect(err).To(MatchError(user.ErrNotFound))
		})

		It("refuses another user's row before reading it, so the answer cannot reveal whether the row exists", func() {
			name := "hijacked"

			_, err := svc.Update(ctx, "someone-else", apptest.SeededID, app.UpdateUserInput{Name: &name})

			Expect(err).To(MatchError(user.ErrForbidden))
			repo.AssertNotCalled(GinkgoT(), "FindByID", mock.Anything, mock.Anything)
		})

		It("reports a bad name and a bad email together, and writes neither", func() {
			name, email := "   ", "not-an-email"
			repo.EXPECT().FindByID(mock.Anything, apptest.SeededID).Return(seeded, nil).Once()

			_, err := svc.Update(ctx, apptest.SeededID, apptest.SeededID, app.UpdateUserInput{Name: &name, Email: &email})

			var all user.ValidationErrors
			Expect(errors.As(err, &all)).To(BeTrue())
			Expect(all).To(HaveLen(2))
			Expect(all[0].Field).To(Equal("name"))
			Expect(all[1].Field).To(Equal("email"))
			repo.AssertNotCalled(GinkgoT(), "Update", mock.Anything, mock.Anything, mock.Anything)
		})
	})

	Describe("Delete", func() {
		It("revokes every refresh token after the row is gone, not before", func() {
			var calls []string
			repo.EXPECT().Delete(mock.Anything, apptest.SeededID).
				Run(func(context.Context, string) { calls = append(calls, "Delete") }).Return(nil).Once()
			refresh.EXPECT().RevokeAllForUser(mock.Anything, apptest.SeededID, now).
				Run(func(context.Context, string, time.Time) { calls = append(calls, "RevokeAllForUser") }).Return(nil).Once()

			Expect(svc.Delete(ctx, apptest.SeededID, apptest.SeededID)).To(Succeed())
			Expect(calls).To(Equal([]string{"Delete", "RevokeAllForUser"}))
		})

		It("leaves the refresh tokens alone when there was no user to delete", func() {
			repo.EXPECT().Delete(mock.Anything, apptest.SeededID).Return(user.ErrNotFound).Once()

			Expect(svc.Delete(ctx, apptest.SeededID, apptest.SeededID)).To(MatchError(user.ErrNotFound))
			refresh.AssertNotCalled(GinkgoT(), "RevokeAllForUser", mock.Anything, mock.Anything, mock.Anything)
		})

		It("surfaces a failure to revoke — the row is gone, the caller should know the cleanup did not finish", func() {
			outage := errors.New("mongo: connection reset")
			repo.EXPECT().Delete(mock.Anything, apptest.SeededID).Return(nil).Once()
			refresh.EXPECT().RevokeAllForUser(mock.Anything, apptest.SeededID, mock.Anything).Return(outage).Once()

			Expect(svc.Delete(ctx, apptest.SeededID, apptest.SeededID)).To(MatchError(outage))
		})

		It("refuses another user's row before the repository is asked", func() {
			Expect(svc.Delete(ctx, "someone-else", apptest.SeededID)).To(MatchError(user.ErrForbidden))
			repo.AssertNotCalled(GinkgoT(), "Delete", mock.Anything, mock.Anything)
		})

		It("rejects an empty id before the repository is asked", func() {
			Expect(svc.Delete(ctx, apptest.SeededID, "")).To(MatchError(user.ErrValidation{}))
		})
	})

	Describe("Count", func() {
		It("is a straight pass-through", func() {
			repo.EXPECT().Count(mock.Anything).Return(42, nil).Once()

			Expect(svc.Count(ctx)).To(BeEquivalentTo(42))
		})
	})
})
