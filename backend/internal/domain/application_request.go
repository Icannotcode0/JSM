package domain

import (
	"encoding/json"
	"time"
)

// ApplicationStatuses is the closed set Status may hold. Declared here rather
// than as a comment on the field so validation has something to check against.
var ApplicationStatuses = []string{
	"applied",
	"phone_screen",
	"onsite",
	"offer",
	"rejected",
	"withdrawn",
}

func IsValidApplicationStatus(status string) bool {
	for _, s := range ApplicationStatuses {
		if s == status {
			return true
		}
	}
	return false
}

// CreateApplicationRequest is the body of POST /applications.
//
// AppliedAt is a pointer so an omitted date can default to "now" rather than
// to the zero time, which would otherwise store year 1.
type CreateApplicationRequest struct {
	CompanyName    string            `json:"company_name"`
	PositionTitle  string            `json:"position_title"`
	Status         string            `json:"status"`
	Location       []string          `json:"location"`
	JobLink        string            `json:"job_link"`
	JobDescription string            `json:"job_description"`
	Tags           []string          `json:"tags"`
	Notes          string            `json:"notes"`
	Compensation   *CompensationInfo `json:"compensation"`
	AppliedAt      *time.Time        `json:"applied_at"`
	NextFollowUpAt *time.Time        `json:"next_follow_up_at"`
}

// UpdateApplicationRequest is the body of PATCH /applications/{id}. Every field
// is a pointer so the handler can tell "the client didn't mention this" (nil,
// leave it alone) from "the client set this to empty" (non-nil, apply it). With
// plain values a PATCH of {"notes":"x"} would silently blank every other field.
type UpdateApplicationRequest struct {
	CompanyName    *string    `json:"company_name"`
	PositionTitle  *string    `json:"position_title"`
	Status         *string    `json:"status"`
	Location       *[]string  `json:"location"`
	JobLink        *string    `json:"job_link"`
	JobDescription *string    `json:"job_description"`
	Tags           *[]string  `json:"tags"`
	Notes          *string    `json:"notes"`
	AppliedAt      *time.Time `json:"applied_at"`

	// NextFollowUpAt and Compensation are both nullable *in the document*, so a
	// single pointer can't distinguish "absent" from "explicitly null" — both
	// decode to nil. RawMessage keeps that distinction: nil means the key was
	// absent, []byte("null") means the client is clearing the field.
	NextFollowUpAt json.RawMessage `json:"next_follow_up_at"`
	Compensation   json.RawMessage `json:"compensation"`
}

// ListApplicationsQuery holds the (already parsed and bounded) query string of
// GET /applications.
type ListApplicationsQuery struct {
	Status   string
	Tag      string
	Search   string
	Page     int
	PageSize int
}

// ApplicationPage is the paginated envelope GET /applications returns.
type ApplicationPage struct {
	Applications []Application `json:"applications"`
	Page         int           `json:"page"`
	PageSize     int           `json:"page_size"`
	Total        int64         `json:"total"`
}
