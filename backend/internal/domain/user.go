package domain

import (
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
)

// User is the canonical shape of a user, stored in the "users" collection.
type User struct {
	ID           bson.ObjectID `bson:"_id,omitempty" json:"id"`
	Email        string        `bson:"email" json:"email"`
	PasswordHash string        `bson:"password_hash" json:"-"` // never serialize the hash
	Name         string        `bson:"name" json:"name"`
	CreatedAt    time.Time     `bson:"created_at" json:"created_at"`
	UpdatedAt    time.Time     `bson:"updated_at" json:"updated_at"`
}
