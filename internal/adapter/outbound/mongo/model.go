package mongostore

import (
	"time"

	"go.mongodb.org/mongo-driver/bson/primitive"

	"github.com/nthw-dev/user-management-api/internal/app"
	"github.com/nthw-dev/user-management-api/internal/domain/user"
)

// Field names as they appear in the documents — one constant per bson tag below, so a query, an index and a struct tag
// can never spell the same field three different ways. A test checks each constant against its tag.
const (
	fieldID           = "_id"
	fieldName         = "name"
	fieldEmail        = "email"
	fieldPasswordHash = "password_hash"
	fieldCreatedAt    = "created_at"
	fieldUpdatedAt    = "updated_at"
	fieldUserID       = "user_id"
	fieldTokenHash    = "token_hash"
	fieldExpiresAt    = "expires_at"
	fieldRevokedAt    = "revoked_at"
)

// userDocument is the shape of the data inside MongoDB.
//
// The mapping lives here and nowhere else, so the domain struct carries not a single bson tag.
// If we moved to another database, this is the file to change — not any file in the domain.
type userDocument struct {
	ID           primitive.ObjectID `bson:"_id,omitempty"`
	Name         string             `bson:"name"`
	Email        string             `bson:"email"`
	PasswordHash string             `bson:"password_hash,omitempty"`
	CreatedAt    time.Time          `bson:"created_at"`
	UpdatedAt    time.Time          `bson:"updated_at"`
}

func toUserDocument(u *user.User) userDocument {
	return userDocument{
		Name:         u.Name,
		Email:        u.Email.String(),
		PasswordHash: u.PasswordHash,
		CreatedAt:    u.CreatedAt.UTC(),
		UpdatedAt:    u.UpdatedAt.UTC(),
	}
}

func (d userDocument) toDomain() *user.User {
	return user.Hydrate(
		d.ID.Hex(),
		d.Name,
		user.Email(d.Email),
		d.PasswordHash,
		d.CreatedAt,
		d.UpdatedAt,
	)
}

type refreshTokenDocument struct {
	ID        primitive.ObjectID `bson:"_id,omitempty"`
	UserID    string             `bson:"user_id"`
	TokenHash string             `bson:"token_hash"`
	CreatedAt time.Time          `bson:"created_at"`
	ExpiresAt time.Time          `bson:"expires_at"`
	RevokedAt *time.Time         `bson:"revoked_at,omitempty"`
}

func toRefreshTokenDocument(rt app.RefreshToken) refreshTokenDocument {
	return refreshTokenDocument{
		UserID:    rt.UserID,
		TokenHash: rt.TokenHash,
		CreatedAt: rt.CreatedAt.UTC(),
		ExpiresAt: rt.ExpiresAt.UTC(),
		RevokedAt: rt.RevokedAt,
	}
}

func (d refreshTokenDocument) toDomain() app.RefreshToken {
	return app.RefreshToken{
		ID:        d.ID.Hex(),
		UserID:    d.UserID,
		TokenHash: d.TokenHash,
		CreatedAt: d.CreatedAt.UTC(),
		ExpiresAt: d.ExpiresAt.UTC(),
		RevokedAt: d.RevokedAt,
	}
}
