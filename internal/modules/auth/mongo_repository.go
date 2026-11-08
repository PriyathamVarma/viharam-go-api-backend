package auth

import (
	"context"
	"errors"
	"strings"

	"github.com/viharam/go-api-backend/internal/platform/mongodb"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

type MongoRepository struct{ db *mongodb.Database }

func NewMongoRepository(db *mongodb.Database) *MongoRepository { return &MongoRepository{db: db} }

type mongoUser struct {
	ID           bson.ObjectID `bson:"_id,omitempty"`
	Name         string        `bson:"name"`
	Email        string        `bson:"email"`
	PasswordHash string        `bson:"passwordHash"`
	Type         string        `bson:"type"`
	PhoneNumber  string        `bson:"phoneNumber,omitempty"`
	Photo        string        `bson:"photo,omitempty"`
	Bio          string        `bson:"bio,omitempty"`
	IsVerified   bool          `bson:"isVerified"`
}

func (r *MongoRepository) Delete(ctx context.Context, id string) error {
	objectID, err := bson.ObjectIDFromHex(id)
	if err != nil {
		return ErrNotFound
	}
	result, err := r.db.Collection("users").DeleteOne(ctx, bson.M{"_id": objectID})
	if err != nil {
		return err
	}
	if result.DeletedCount == 0 {
		return ErrNotFound
	}
	return nil
}

func (r *MongoRepository) Create(ctx context.Context, input RegisterInput, passwordHash string) (User, error) {
	email := strings.ToLower(strings.TrimSpace(input.Email))
	var existing mongoUser
	if err := r.db.Collection("users").FindOne(ctx, bson.M{"email": email}).Decode(&existing); err == nil {
		return User{}, ErrEmailExists
	} else if !errors.Is(err, mongo.ErrNoDocuments) {
		return User{}, err
	}
	doc := mongoUser{Name: input.Name, Email: email, PasswordHash: passwordHash, Type: input.Type}
	result, err := r.db.Collection("users").InsertOne(ctx, doc)
	if err != nil {
		if mongo.IsDuplicateKeyError(err) {
			return User{}, ErrEmailExists
		}
		return User{}, err
	}
	if id, ok := result.InsertedID.(bson.ObjectID); ok {
		doc.ID = id
	}
	return toUser(doc), nil
}

func (r *MongoRepository) EnsureDemo(ctx context.Context, input RegisterInput, passwordHash string) (User, error) {
	email := strings.ToLower(strings.TrimSpace(input.Email))
	filter := bson.M{"email": email}
	update := bson.M{"$set": bson.M{
		"name": input.Name, "email": email, "passwordHash": passwordHash, "type": input.Type,
	}}
	if _, err := r.db.Collection("users").UpdateOne(ctx, filter, update, options.UpdateOne().SetUpsert(true)); err != nil {
		return User{}, err
	}
	user, _, err := r.FindByEmail(ctx, email)
	return user, err
}

func (r *MongoRepository) FindByEmail(ctx context.Context, email string) (User, string, error) {
	var doc mongoUser
	err := r.db.Collection("users").FindOne(ctx, bson.M{"email": strings.ToLower(strings.TrimSpace(email))}).Decode(&doc)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return User{}, "", ErrNotFound
	}
	if err != nil {
		return User{}, "", err
	}
	return toUser(doc), doc.PasswordHash, nil
}

func (r *MongoRepository) FindByID(ctx context.Context, id string) (User, error) {
	objectID, err := bson.ObjectIDFromHex(id)
	if err != nil {
		return User{}, ErrNotFound
	}
	var doc mongoUser
	err = r.db.Collection("users").FindOne(ctx, bson.M{"_id": objectID}).Decode(&doc)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return User{}, ErrNotFound
	}
	if err != nil {
		return User{}, err
	}
	return toUser(doc), nil
}

func (r *MongoRepository) FindOrCreateOAuth(ctx context.Context, provider, subject, name, email string) (User, error) {
	email = strings.ToLower(strings.TrimSpace(email))
	var doc mongoUser
	err := r.db.Collection("users").FindOne(ctx, bson.M{"email": email}).Decode(&doc)
	if errors.Is(err, mongo.ErrNoDocuments) {
		doc = mongoUser{Name: name, Email: email, PasswordHash: "oauth:" + provider + ":" + subject, Type: "Traveller"}
		result, insertErr := r.db.Collection("users").InsertOne(ctx, doc)
		if insertErr != nil {
			return User{}, insertErr
		}
		if id, ok := result.InsertedID.(bson.ObjectID); ok {
			doc.ID = id
		}
		return toUser(doc), nil
	}
	if err != nil {
		return User{}, err
	}
	if doc.Name == "" {
		doc.Name = name
	}
	if _, err := r.db.Collection("users").UpdateOne(ctx, bson.M{"_id": doc.ID}, bson.M{"$set": bson.M{"name": doc.Name, "oauthProvider": provider, "oauthSubject": subject}}); err != nil {
		return User{}, err
	}
	return toUser(doc), nil
}

func toUser(doc mongoUser) User {
	return User{ID: doc.ID.Hex(), Name: doc.Name, Email: doc.Email, Role: doc.Type, PhoneNumber: doc.PhoneNumber, PhotoURL: doc.Photo, Bio: doc.Bio, IsVerified: doc.IsVerified}
}
