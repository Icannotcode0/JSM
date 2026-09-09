package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"net/url"
	"strings"
	"time"

	"github.com/Icannotcode0/job-app-manager/backend/internal/common/mongoWrap"
	"github.com/Icannotcode0/job-app-manager/backend/internal/domain"
	"github.com/Icannotcode0/job-app-manager/backend/internal/store"
	"go.mongodb.org/mongo-driver/v2/bson"
)

// ErrApplicationNotFound covers both "no such application" and "belongs to
// someone else" — the store can't tell them apart by design, and neither
// should the caller (API.md).
var ErrApplicationNotFound = errors.New("application not found")

// ErrInvalidInput carries a caller-facing reason. It is the only error here
// whose message is safe to return verbatim: every string in it is written
// below, never interpolated from the database.
type ErrInvalidInput struct{ Reason string }

func (e ErrInvalidInput) Error() string { return e.Reason }

func invalid(format string, args ...any) error {
	return ErrInvalidInput{Reason: fmt.Sprintf(format, args...)}
}

// Field caps. These exist because the browser extension will POST scraped page
// content here (DESIGN_GUIDE Part 6), and scraped content is attacker-shaped by
// definition — a job board can put anything in a posting. Applying them to the
// normal endpoints too means there's one validation path, not a "trusted" and
// an "untrusted" one that can drift.
const (
	maxCompanyName    = 200
	maxPositionTitle  = 200
	maxJobLink        = 2000
	maxJobDescription = 50_000
	maxNotes          = 20_000
	maxLocationItem   = 200
	maxLocations      = 20
	maxTagLength      = 50
	maxTags           = 30
	maxEquity         = 200
	maxBenefitItem    = 100
	maxBenefits       = 30
	maxSearchLength   = 200

	defaultPageSize = 20
	maxPageSize     = 200
)

type applications struct {
	store *store.Store
}

func NewApplications(s *store.Store) *applications {
	return &applications{store: s}
}

/* -------------------------------------------------------------------------
   Sanitising
   ------------------------------------------------------------------------- */

// clean normalises a single free-text field: trim, cap length, and escape any
// HTML so a scraped `<script>` is inert wherever it is later rendered.
//
// Escaping at write time is deliberate. The frontend uses textContent and would
// be safe on its own, but the store is also read by the extension and anything
// else added later, and a value that is safe in every consumer beats one that
// is safe only in the consumers that remember to escape.
func clean(value string, max int) string {
	value = strings.TrimSpace(value)
	value = html.EscapeString(value)
	if len(value) > max {
		value = value[:max]
	}
	return value
}

// cleanList trims, drops empties, de-duplicates, and caps both item length and
// list length.
func cleanList(values []string, maxItem, maxLen int) []string {
	out := make([]string, 0, len(values))
	seen := map[string]struct{}{}

	for _, v := range values {
		v = clean(v, maxItem)
		if v == "" {
			continue
		}
		if _, dup := seen[v]; dup {
			continue
		}
		seen[v] = struct{}{}
		out = append(out, v)
		if len(out) == maxLen {
			break
		}
	}
	return out
}

// validateJobLink enforces that a link is http(s) and nothing else.
//
// The rejected schemes are the point: `javascript:` and `data:` URLs stored
// here would become live XSS the moment anything renders this as an <a href>,
// and `file:` would turn a stored value into a pointer at the user's disk.
func validateJobLink(link string) (string, error) {
	link = strings.TrimSpace(link)
	if link == "" {
		return "", nil
	}
	if len(link) > maxJobLink {
		return "", invalid("job_link must be at most %d characters", maxJobLink)
	}

	parsed, err := url.Parse(link)
	if err != nil {
		return "", invalid("job_link is not a valid URL")
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", invalid("job_link must be an http or https URL")
	}
	if parsed.Host == "" {
		return "", invalid("job_link must include a host")
	}
	return link, nil
}

func cleanCompensation(c *domain.CompensationInfo) *domain.CompensationInfo {
	if c == nil {
		return nil
	}
	return &domain.CompensationInfo{
		BaseSalary: c.BaseSalary,
		Bonus:      c.Bonus,
		Equity:     clean(c.Equity, maxEquity),
		Benefits:   cleanList(c.Benefits, maxBenefitItem, maxBenefits),
	}
}

/* -------------------------------------------------------------------------
   Create
   ------------------------------------------------------------------------- */

func (a *applications) Create(
	ctx context.Context,
	userID string,
	req domain.CreateApplicationRequest,
) (domain.Application, error) {
	uid, err := bson.ObjectIDFromHex(userID)
	if err != nil {
		return domain.Application{}, invalid("invalid user id")
	}

	company := clean(req.CompanyName, maxCompanyName)
	if company == "" {
		return domain.Application{}, invalid("company_name is required")
	}
	position := clean(req.PositionTitle, maxPositionTitle)
	if position == "" {
		return domain.Application{}, invalid("position_title is required")
	}

	// An omitted status means the application was just sent.
	status := strings.TrimSpace(req.Status)
	if status == "" {
		status = "applied"
	}
	if !domain.IsValidApplicationStatus(status) {
		return domain.Application{}, invalid("status must be one of: %s", strings.Join(domain.ApplicationStatuses, ", "))
	}

	link, err := validateJobLink(req.JobLink)
	if err != nil {
		return domain.Application{}, err
	}

	appliedAt := time.Now().UTC()
	if req.AppliedAt != nil {
		appliedAt = req.AppliedAt.UTC()
	}

	app := domain.Application{
		UserID:         uid,
		CompanyName:    company,
		PositionTitle:  position,
		Status:         status,
		Location:       cleanList(req.Location, maxLocationItem, maxLocations),
		JobLink:        link,
		JobDescription: clean(req.JobDescription, maxJobDescription),
		Tags:           cleanList(req.Tags, maxTagLength, maxTags),
		Notes:          clean(req.Notes, maxNotes),
		Compensation:   cleanCompensation(req.Compensation),
		Resumes:        []domain.ResumeVersion{},
		AppliedAt:      appliedAt,
		NextFollowUpAt: req.NextFollowUpAt,
	}

	created, err := a.store.Applications.Create(ctx, app)
	if err != nil {
		return domain.Application{}, err
	}
	return created, nil
}

/* -------------------------------------------------------------------------
   Read
   ------------------------------------------------------------------------- */

func (a *applications) Get(ctx context.Context, userID, id string) (domain.Application, error) {
	uid, appID, err := parseIDs(userID, id)
	if err != nil {
		return domain.Application{}, err
	}

	app, err := a.store.Applications.Get(ctx, appID, uid)
	if err != nil {
		return domain.Application{}, mapStoreErr(err)
	}
	return app, nil
}

func (a *applications) List(
	ctx context.Context,
	userID string,
	query domain.ListApplicationsQuery,
) (domain.ApplicationPage, error) {
	uid, err := bson.ObjectIDFromHex(userID)
	if err != nil {
		return domain.ApplicationPage{}, invalid("invalid user id")
	}

	if query.Status != "" && !domain.IsValidApplicationStatus(query.Status) {
		return domain.ApplicationPage{}, invalid("status must be one of: %s", strings.Join(domain.ApplicationStatuses, ", "))
	}
	if len(query.Search) > maxSearchLength {
		return domain.ApplicationPage{}, invalid("q must be at most %d characters", maxSearchLength)
	}

	// Bound paging here rather than trusting the query string: page_size is
	// what decides how many documents a single request can pull into memory.
	if query.Page < 1 {
		query.Page = 1
	}
	if query.PageSize < 1 {
		query.PageSize = defaultPageSize
	}
	if query.PageSize > maxPageSize {
		query.PageSize = maxPageSize
	}

	apps, total, err := a.store.Applications.List(ctx, uid, query)
	if err != nil {
		return domain.ApplicationPage{}, err
	}

	return domain.ApplicationPage{
		Applications: apps,
		Page:         query.Page,
		PageSize:     query.PageSize,
		Total:        total,
	}, nil
}

/* -------------------------------------------------------------------------
   Update
   ------------------------------------------------------------------------- */

// Update applies only the fields the client actually sent. Anything absent from
// the request body is left untouched — see UpdateApplicationRequest for why
// every field is a pointer.
func (a *applications) Update(
	ctx context.Context,
	userID, id string,
	req domain.UpdateApplicationRequest,
) (domain.Application, error) {
	uid, appID, err := parseIDs(userID, id)
	if err != nil {
		return domain.Application{}, err
	}

	set := bson.M{}

	if req.CompanyName != nil {
		company := clean(*req.CompanyName, maxCompanyName)
		if company == "" {
			return domain.Application{}, invalid("company_name cannot be empty")
		}
		set["company_name"] = company
	}
	if req.PositionTitle != nil {
		position := clean(*req.PositionTitle, maxPositionTitle)
		if position == "" {
			return domain.Application{}, invalid("position_title cannot be empty")
		}
		set["position_title"] = position
	}
	if req.Status != nil {
		status := strings.TrimSpace(*req.Status)
		if !domain.IsValidApplicationStatus(status) {
			return domain.Application{}, invalid("status must be one of: %s", strings.Join(domain.ApplicationStatuses, ", "))
		}
		set["status"] = status
	}
	if req.JobLink != nil {
		link, err := validateJobLink(*req.JobLink)
		if err != nil {
			return domain.Application{}, err
		}
		set["job_link"] = link
	}
	if req.JobDescription != nil {
		set["job_description"] = clean(*req.JobDescription, maxJobDescription)
	}
	if req.Notes != nil {
		set["notes"] = clean(*req.Notes, maxNotes)
	}
	if req.Location != nil {
		set["location"] = cleanList(*req.Location, maxLocationItem, maxLocations)
	}
	if req.Tags != nil {
		set["tags"] = cleanList(*req.Tags, maxTagLength, maxTags)
	}
	if req.AppliedAt != nil {
		set["applied_at"] = req.AppliedAt.UTC()
	}

	if err := applyNullable(set, "next_follow_up_at", req.NextFollowUpAt, func(raw json.RawMessage) (any, error) {
		var t time.Time
		if err := json.Unmarshal(raw, &t); err != nil {
			return nil, invalid("next_follow_up_at must be an RFC3339 timestamp or null")
		}
		return t.UTC(), nil
	}); err != nil {
		return domain.Application{}, err
	}

	if err := applyNullable(set, "compensation", req.Compensation, func(raw json.RawMessage) (any, error) {
		var c domain.CompensationInfo
		if err := json.Unmarshal(raw, &c); err != nil {
			return nil, invalid("compensation must be an object or null")
		}
		return cleanCompensation(&c), nil
	}); err != nil {
		return domain.Application{}, err
	}

	app, err := a.store.Applications.Update(ctx, appID, uid, set)
	if err != nil {
		return domain.Application{}, mapStoreErr(err)
	}
	return app, nil
}

// applyNullable handles the three states a JSON field can be in when the field
// is itself nullable: absent (leave alone), null (clear it), or a value.
func applyNullable(
	set bson.M,
	field string,
	raw json.RawMessage,
	parse func(json.RawMessage) (any, error),
) error {
	if raw == nil {
		return nil // key absent — not part of this patch
	}
	if string(raw) == "null" {
		set[field] = nil
		return nil
	}
	value, err := parse(raw)
	if err != nil {
		return err
	}
	set[field] = value
	return nil
}

/* -------------------------------------------------------------------------
   Delete
   ------------------------------------------------------------------------- */

func (a *applications) Delete(ctx context.Context, userID, id string) error {
	uid, appID, err := parseIDs(userID, id)
	if err != nil {
		return err
	}
	if err := a.store.Applications.Delete(ctx, appID, uid); err != nil {
		return mapStoreErr(err)
	}
	return nil
}

/* -------------------------------------------------------------------------
   Shared
   ------------------------------------------------------------------------- */

// parseIDs converts both hex IDs at once.
//
// A malformed application ID returns not-found rather than a validation error:
// "that isn't a valid ObjectID" and "no such application" are the same fact
// from the caller's side, and distinguishing them tells a prober which IDs are
// well-formed.
func parseIDs(userID, id string) (uid, appID bson.ObjectID, err error) {
	uid, err = bson.ObjectIDFromHex(userID)
	if err != nil {
		return uid, appID, invalid("invalid user id")
	}
	appID, err = bson.ObjectIDFromHex(id)
	if err != nil {
		return uid, appID, ErrApplicationNotFound
	}
	return uid, appID, nil
}

func mapStoreErr(err error) error {
	if errors.Is(err, mongoWrap.ErrApplicationNotFound) {
		return ErrApplicationNotFound
	}
	return err
}
