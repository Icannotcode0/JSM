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

	// EmailVerified records whether the address has been proved to belong to
	// this user. Added before there is a verification flow because the
	// alternative is backfilling every existing document later and then
	// deciding what a missing value means for accounts that predate the field.
	//
	// Zero value is false, which is the safe default: an account is unverified
	// until something proves otherwise. Local installs mark it true at signup
	// (see config.RequireEmailVerification) so the local-only promise holds
	// without a mail transport.
	EmailVerified   bool       `bson:"email_verified" json:"email_verified"`
	EmailVerifiedAt *time.Time `bson:"email_verified_at,omitempty" json:"email_verified_at,omitempty"`

	CreatedAt time.Time `bson:"created_at" json:"created_at"`
	UpdatedAt time.Time `bson:"updated_at" json:"updated_at"`
}

type SignUpRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
	Name     string `json:"name"`
}
