package domain

import (
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
)

// Application is the canonical shape of a job application, stored in the
// "applications" collection, scoped to the user who created it.
type Application struct {
	ID             bson.ObjectID     `bson:"_id,omitempty" json:"id"`
	UserID         bson.ObjectID     `bson:"user_id" json:"user_id"`
	CompanyName    string            `bson:"company_name" json:"company_name"`
	PositionTitle  string            `bson:"position_title" json:"position_title"`
	Status         string            `bson:"status" json:"status"` // "applied", "phone_screen", "onsite", "offer", "rejected", "withdrawn"
	Location       []string          `bson:"location" json:"location"`
	JobLink        string            `bson:"job_link" json:"job_link"`
	JobDescription string            `bson:"job_description" json:"job_description"`
	Tags           []string          `bson:"tags" json:"tags"`
	Notes          string            `bson:"notes" json:"notes"`
	Compensation   *CompensationInfo `bson:"compensation,omitempty" json:"compensation,omitempty"`
	Resumes        []ResumeVersion   `bson:"resumes,omitempty" json:"resumes,omitempty"`
	AppliedAt      time.Time         `bson:"applied_at" json:"applied_at"`
	NextFollowUpAt *time.Time        `bson:"next_follow_up_at,omitempty" json:"next_follow_up_at,omitempty"`
	CreatedAt      time.Time         `bson:"created_at" json:"created_at"`
	UpdatedAt      time.Time         `bson:"updated_at" json:"updated_at"`
}

// CompensationInfo holds the compensation details for an Application.
type CompensationInfo struct {
	BaseSalary int64    `bson:"base_salary" json:"base_salary"`
	Bonus      int64    `bson:"bonus" json:"bonus"`
	Equity     string   `bson:"equity" json:"equity"` // free text, e.g. "$40k RSU over 4yr"
	Benefits   []string `bson:"benefits" json:"benefits"`
}

// ResumeVersion records one resume file uploaded against an Application.
type ResumeVersion struct {
	ID         bson.ObjectID `bson:"_id,omitempty" json:"id"`
	Label      string        `bson:"label" json:"label"` // e.g. "Backend-focused v2"
	StorageKey string        `bson:"storage_key" json:"-"`
	FileName   string        `bson:"file_name" json:"file_name"`
	UploadedAt time.Time     `bson:"uploaded_at" json:"uploaded_at"`
}
