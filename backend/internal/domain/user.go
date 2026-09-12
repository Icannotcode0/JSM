package domain

import (
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
)

// User is the canonical shape of a user, stored in the "users" collection.
type User struct {
	ID bson.ObjectID `bson:"_id,omitempty" json:"id"`

	// Email is the address exactly as the user typed it.
	//
	// RFC 5321 makes the local part case-sensitive, so this is the form to
	// display and the form to send mail to. It deliberately carries no
	// uniqueness constraint — matching is EmailNormalized's job.
	Email string `bson:"email" json:"email"`

	// EmailNormalized is the lowercased form, and the only one ever compared.
	//
	// It exists because the two jobs pull in opposite directions: preserving
	// what the user typed is correct for delivery, but matching on it means
	// Ada@x.com and ada@x.com are different accounts — so someone who signs up
	// as one and signs in as the other is told their credentials are wrong with
	// no way to discover why. Splitting the field lets each job be right.
	//
	// The unique index lives here, and every lookup goes through it.
	// json:"-" because it is an implementation detail of matching, not
	// something a client has any use for.
	EmailNormalized string `bson:"email_normalized" json:"-"`

	PasswordHash string `bson:"password_hash" json:"-"` // never serialize the hash
	Name         string `bson:"name" json:"name"`

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
