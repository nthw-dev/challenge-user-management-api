package mongostore

import (
	"context"
	"errors"
	"fmt"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"

	"github.com/nthw-dev/user-management-api/internal/app"
)

// RefreshTokenRepo stores only the hash of a refresh token,
// so that a leaked database is not a pile of immediately usable keys.
type RefreshTokenRepo struct{ col *mongo.Collection }

var _ app.RefreshTokenRepository = (*RefreshTokenRepo)(nil)

func NewRefreshTokenRepo(col *mongo.Collection) *RefreshTokenRepo {
	return &RefreshTokenRepo{col: col}
}

// EnsureIndexes is idempotent and runs at boot, like UserRepo.EnsureIndexes.
func (r *RefreshTokenRepo) EnsureIndexes(ctx context.Context) error {
	_, err := r.col.Indexes().CreateMany(ctx, []mongo.IndexModel{
		{
			Keys:    bson.D{{Key: fieldTokenHash, Value: 1}},
			Options: options.Index().SetUnique(true).SetName("uniq_token_hash"),
		},
		{
			Keys:    bson.D{{Key: fieldUserID, Value: 1}},
			Options: options.Index().SetName("user_id"),
		},
		{
			// Let MongoDB delete expired tokens itself, so this collection does not grow without bound.
			Keys:    bson.D{{Key: fieldExpiresAt, Value: 1}},
			Options: options.Index().SetName("ttl_expires_at").SetExpireAfterSeconds(0),
		},
	})
	if err != nil {
		return fmt.Errorf("create refresh_tokens indexes: %w", err)
	}
	return nil
}

func (r *RefreshTokenRepo) Store(ctx context.Context, rt app.RefreshToken) error {
	ctx, cancel := context.WithTimeout(ctx, opTimeout)
	defer cancel()

	_, err := r.col.InsertOne(ctx, toRefreshTokenDocument(rt))
	if mongo.IsDuplicateKeyError(err) {
		// Two tokens hashing alike means 256 random bits collided — practically impossible, so it is named rather than hidden.
		return fmt.Errorf("insert refresh token: hash collision: %w", err)
	}
	if err != nil {
		return fmt.Errorf("insert refresh token: %w", err)
	}
	return nil
}

func (r *RefreshTokenRepo) FindByHash(ctx context.Context, hash string) (*app.RefreshToken, error) {
	ctx, cancel := context.WithTimeout(ctx, opTimeout)
	defer cancel()

	var doc refreshTokenDocument
	err := r.col.FindOne(ctx, bson.M{fieldTokenHash: hash}).Decode(&doc)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return nil, app.ErrRefreshTokenNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("find refresh token: %w", err)
	}

	rt := doc.toDomain()
	return &rt, nil
}

// Revoke marks one live token as revoked. A token that does not exist, or was already revoked, answers ErrRefreshTokenNotFound —
// there was no live token to revoke, and the caller should know rather than assume it succeeded.
func (r *RefreshTokenRepo) Revoke(ctx context.Context, id string, now time.Time) error {
	oid, err := primitive.ObjectIDFromHex(id)
	if err != nil {
		return app.ErrRefreshTokenNotFound
	}
	ctx, cancel := context.WithTimeout(ctx, opTimeout)
	defer cancel()

	res, err := r.col.UpdateOne(ctx,
		bson.M{fieldID: oid, fieldRevokedAt: bson.M{"$exists": false}},
		bson.M{"$set": bson.M{fieldRevokedAt: now.UTC()}},
	)
	if err != nil {
		return fmt.Errorf("revoke refresh token: %w", err)
	}
	if res.MatchedCount == 0 {
		return app.ErrRefreshTokenNotFound
	}
	return nil
}

// RevokeAllForUser is called when we detect an already-rotated token being presented again,
// which means a copy has leaked — wiping every session is safer than leaving them alive.
// Matching nothing is a legitimate outcome here: the user may simply have no live sessions left.
func (r *RefreshTokenRepo) RevokeAllForUser(ctx context.Context, userID string, now time.Time) error {
	ctx, cancel := context.WithTimeout(ctx, opTimeout)
	defer cancel()

	_, err := r.col.UpdateMany(ctx,
		bson.M{fieldUserID: userID, fieldRevokedAt: bson.M{"$exists": false}},
		bson.M{"$set": bson.M{fieldRevokedAt: now.UTC()}},
	)
	if err != nil {
		return fmt.Errorf("revoke all refresh tokens: %w", err)
	}
	return nil
}
