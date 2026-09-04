package mongostore

import (
	"context"
	"errors"
	"fmt"
	"regexp"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"

	"github.com/nthw-dev/user-management-api/internal/app"
	"github.com/nthw-dev/user-management-api/internal/domain/user"
)

// noPassword is the projection used by reads that do not need the password hash.
// We do not fetch what we do not use — and a value that should never leak never leaves the database in the first place.
var noPassword = bson.M{fieldPasswordHash: 0}

type UserRepo struct{ col *mongo.Collection }

var _ app.UserRepository = (*UserRepo)(nil)

func NewUserRepo(col *mongo.Collection) *UserRepo { return &UserRepo{col: col} }

// EnsureIndexes is called at boot, before the port starts accepting requests. The command is idempotent,
// so re-running it on every container restart is safe. Each repository owns the indexes of its own collection.
//
// Caveat: on a large collection, building indexes at boot stalls the deploy. A real system should move this into a migration step.
func (r *UserRepo) EnsureIndexes(ctx context.Context) error {
	_, err := r.col.Indexes().CreateMany(ctx, []mongo.IndexModel{
		{
			// Enforces email uniqueness at the storage layer — this is the source of truth for it,
			// not a check-then-insert query, which loses the race between two requests.
			Keys:    bson.D{{Key: fieldEmail, Value: 1}},
			Options: options.Index().SetUnique(true).SetName("uniq_email"),
		},
	})
	if err != nil {
		return fmt.Errorf("create users indexes: %w", err)
	}
	return nil
}

func (r *UserRepo) Create(ctx context.Context, u *user.User) error {
	ctx, cancel := context.WithTimeout(ctx, opTimeout)
	defer cancel()

	res, err := r.col.InsertOne(ctx, toUserDocument(u))
	if mongo.IsDuplicateKeyError(err) {
		// Translate the driver's errors into the language of the domain,
		// so the layers above have no way of knowing MongoDB is behind this.
		return user.ErrEmailTaken
	}
	if err != nil {
		return fmt.Errorf("insert user: %w", err)
	}

	oid, ok := res.InsertedID.(primitive.ObjectID)
	if !ok {
		return fmt.Errorf("insert user: unexpected _id type %T", res.InsertedID)
	}
	u.ID = oid.Hex()
	return nil
}

func (r *UserRepo) FindByID(ctx context.Context, id string) (*user.User, error) {
	oid, err := objectID(id)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, opTimeout)
	defer cancel()

	var doc userDocument
	err = r.col.FindOne(ctx, bson.M{fieldID: oid}, options.FindOne().SetProjection(noPassword)).Decode(&doc)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return nil, user.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("find user by id: %w", err)
	}
	return doc.toDomain(), nil
}

// FindByEmail is the one read that must also return the password hash, because it is used at login.
func (r *UserRepo) FindByEmail(ctx context.Context, email user.Email) (*user.User, error) {
	ctx, cancel := context.WithTimeout(ctx, opTimeout)
	defer cancel()

	var doc userDocument
	err := r.col.FindOne(ctx, bson.M{fieldEmail: email.String()}).Decode(&doc)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return nil, user.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("find user by email: %w", err)
	}
	return doc.toDomain(), nil
}

// List uses keyset pagination rather than skip — the sort and the cursor both use _id alone, so the existing _id index suffices.
// skip(n) forces the database to walk past and discard n documents before it starts reading, so deep pages get steadily slower.
// Keyset instead jumps straight into the index, which makes every page equally fast.
func (r *UserRepo) List(ctx context.Context, q app.ListQuery) (app.Page, error) {
	filter := bson.M{}

	if q.Cursor != "" {
		// The cursor is the _id of the last item on the previous page.
		oid, err := primitive.ObjectIDFromHex(q.Cursor)
		if err != nil {
			return app.Page{}, user.ErrValidation{Field: "cursor", Reason: "invalid format"}
		}
		filter[fieldID] = bson.M{"$lt": oid}
	}

	if q.Query != "" {
		// Always escape first, otherwise a caller can send a regex that triggers catastrophic backtracking.
		pattern := regexp.QuoteMeta(q.Query)
		filter["$or"] = []bson.M{
			{fieldName: bson.M{"$regex": pattern, "$options": "i"}},
			{fieldEmail: bson.M{"$regex": pattern, "$options": "i"}},
		}
	}

	ctx, cancel := context.WithTimeout(ctx, opTimeout)
	defer cancel()

	opts := options.Find().
		SetSort(bson.D{{Key: fieldID, Value: -1}}).
		SetLimit(int64(q.Limit) + 1). // fetch one extra to learn whether a next page exists
		SetProjection(noPassword)

	cur, err := r.col.Find(ctx, filter, opts)
	if err != nil {
		return app.Page{}, fmt.Errorf("list users: %w", err)
	}
	defer func() { _ = cur.Close(ctx) }()

	var docs []userDocument
	if err := cur.All(ctx, &docs); err != nil {
		return app.Page{}, fmt.Errorf("decode users: %w", err)
	}

	page := app.Page{}
	if len(docs) > q.Limit {
		docs = docs[:q.Limit]
		page.NextCursor = docs[len(docs)-1].ID.Hex()
	}
	page.Users = make([]user.User, 0, len(docs))
	for _, d := range docs {
		page.Users = append(page.Users, *d.toDomain())
	}
	return page, nil
}

func (r *UserRepo) Update(ctx context.Context, id string, p app.UpdatePatch) (*user.User, error) {
	oid, err := objectID(id)
	if err != nil {
		return nil, err
	}

	set := bson.M{fieldUpdatedAt: p.UpdatedAt.UTC()}
	if p.Name != nil {
		set[fieldName] = *p.Name
	}
	if p.Email != nil {
		set[fieldEmail] = p.Email.String()
	}

	ctx, cancel := context.WithTimeout(ctx, opTimeout)
	defer cancel()

	// FindOneAndUpdate returns the post-update document in a single round trip, with no second read.
	opts := options.FindOneAndUpdate().
		SetReturnDocument(options.After).
		SetProjection(noPassword)

	var doc userDocument
	err = r.col.FindOneAndUpdate(ctx, bson.M{fieldID: oid}, bson.M{"$set": set}, opts).Decode(&doc)
	switch {
	case errors.Is(err, mongo.ErrNoDocuments):
		return nil, user.ErrNotFound
	case mongo.IsDuplicateKeyError(err):
		return nil, user.ErrEmailTaken
	case err != nil:
		return nil, fmt.Errorf("update user: %w", err)
	}
	return doc.toDomain(), nil
}

func (r *UserRepo) Delete(ctx context.Context, id string) error {
	oid, err := objectID(id)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, opTimeout)
	defer cancel()

	res, err := r.col.DeleteOne(ctx, bson.M{fieldID: oid})
	if err != nil {
		return fmt.Errorf("delete user: %w", err)
	}
	if res.DeletedCount == 0 {
		// Deleting the same one twice gives a 404 — telling the truth that the resource is gone.
		return user.ErrNotFound
	}
	return nil
}

// Count uses CountDocuments, which counts for real and is exact.
// On a very large collection, EstimatedDocumentCount is faster because it reads from metadata,
// but it can be off — at this project's size, accuracy is worth more.
func (r *UserRepo) Count(ctx context.Context) (int64, error) {
	ctx, cancel := context.WithTimeout(ctx, opTimeout)
	defer cancel()

	n, err := r.col.CountDocuments(ctx, bson.M{})
	if err != nil {
		return 0, fmt.Errorf("count users: %w", err)
	}
	return n, nil
}

func objectID(id string) (primitive.ObjectID, error) {
	oid, err := primitive.ObjectIDFromHex(id)
	if err != nil {
		// We do not reveal that an ObjectId is behind this — the id's type is a storage concern, not part of the API contract.
		return primitive.NilObjectID, user.ErrValidation{Field: "id", Reason: "invalid format"}
	}
	return oid, nil
}
