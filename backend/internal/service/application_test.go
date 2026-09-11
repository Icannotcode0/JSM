package service

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Icannotcode0/job-app-manager/backend/internal/common/metrics"
	"github.com/Icannotcode0/job-app-manager/backend/internal/domain"
	"github.com/Icannotcode0/job-app-manager/backend/internal/store"
	"go.mongodb.org/mongo-driver/v2/bson"
)

type fakeAppStore struct {
	created domain.Application
	listQ   domain.ListApplicationsQuery
	err     error
}

func (f *fakeAppStore) Create(_ context.Context, app domain.Application) (domain.Application, error) {
	f.created = app
	return app, f.err
}
func (f *fakeAppStore) Get(_ context.Context, _, _ bson.ObjectID) (domain.Application, error) {
	return domain.Application{}, f.err
}
func (f *fakeAppStore) List(_ context.Context, _ bson.ObjectID, q domain.ListApplicationsQuery) ([]domain.Application, int64, error) {
	f.listQ = q
	return []domain.Application{}, 0, f.err
}
func (f *fakeAppStore) Update(_ context.Context, _, _ bson.ObjectID, _ bson.M) (domain.Application, error) {
	return domain.Application{}, f.err
}
func (f *fakeAppStore) Delete(_ context.Context, _, _ bson.ObjectID) error { return f.err }

func newApps() (*applications, *fakeAppStore) {
	fake := &fakeAppStore{}
	return NewApplications(&store.Store{Applications: fake}), fake
}

func validUserID() string { return bson.NewObjectID().Hex() }

/* ---------- create validation -------------------------------------------- */

func TestCreateRequiresCompanyAndTitle(t *testing.T) {
	svc, _ := newApps()
	uid := validUserID()

	for name, req := range map[string]domain.CreateApplicationRequest{
		"no company": {PositionTitle: "Engineer"},
		"no title":   {CompanyName: "Acme"},
		"whitespace": {CompanyName: "   ", PositionTitle: "Engineer"},
	} {
		t.Run(name, func(t *testing.T) {
			var invalid metrics.InvalidInputError
			if _, err := svc.Create(context.Background(), uid, req); !errors.As(err, &invalid) {
				t.Errorf("got %v, want metrics.InvalidInputError", err)
			}
		})
	}
}

func TestCreateRejectsBadStatus(t *testing.T) {
	svc, _ := newApps()
	var invalid metrics.InvalidInputError
	_, err := svc.Create(context.Background(), validUserID(), domain.CreateApplicationRequest{
		CompanyName: "Acme", PositionTitle: "Engineer", Status: "hired",
	})
	if !errors.As(err, &invalid) {
		t.Fatalf("got %v, want metrics.InvalidInputError", err)
	}
}

func TestCreateDefaultsStatusToApplied(t *testing.T) {
	svc, fake := newApps()
	if _, err := svc.Create(context.Background(), validUserID(), domain.CreateApplicationRequest{
		CompanyName: "Acme", PositionTitle: "Engineer",
	}); err != nil {
		t.Fatal(err)
	}
	if fake.created.Status != "applied" {
		t.Errorf("status = %q, want applied", fake.created.Status)
	}
}

// job_link is stored and later rendered as an href. javascript:, data:, and
// file: URLs would each be live XSS or a pointer at local disk.
func TestCreateRejectsDangerousJobLinkSchemes(t *testing.T) {
	svc, _ := newApps()
	uid := validUserID()

	for _, link := range []string{
		"javascript:alert(1)",
		"data:text/html,<script>alert(1)</script>",
		"file:///etc/passwd",
		"JavaScript:alert(1)", // scheme comparison must be case-insensitive
	} {
		t.Run(link, func(t *testing.T) {
			var invalid metrics.InvalidInputError
			_, err := svc.Create(context.Background(), uid, domain.CreateApplicationRequest{
				CompanyName: "Acme", PositionTitle: "Engineer", JobLink: link,
			})
			if !errors.As(err, &invalid) {
				t.Errorf("scheme accepted: got %v", err)
			}
		})
	}
}

func TestCreateAcceptsHTTPLinks(t *testing.T) {
	svc, _ := newApps()
	uid := validUserID()
	for _, link := range []string{"http://x.example/j/1", "https://x.example/j/1", ""} {
		if _, err := svc.Create(context.Background(), uid, domain.CreateApplicationRequest{
			CompanyName: "Acme", PositionTitle: "Engineer", JobLink: link,
		}); err != nil {
			t.Errorf("%q rejected: %v", link, err)
		}
	}
}

func TestCreateEscapesHTML(t *testing.T) {
	svc, fake := newApps()
	if _, err := svc.Create(context.Background(), validUserID(), domain.CreateApplicationRequest{
		CompanyName:   "<script>alert(1)</script>",
		PositionTitle: "<img src=x onerror=alert(1)>",
	}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(fake.created.CompanyName, "<script>") {
		t.Errorf("HTML stored raw: %q", fake.created.CompanyName)
	}
	if strings.Contains(fake.created.PositionTitle, "<img") {
		t.Errorf("HTML stored raw: %q", fake.created.PositionTitle)
	}
}

func TestCreateNormalisesLists(t *testing.T) {
	svc, fake := newApps()
	if _, err := svc.Create(context.Background(), validUserID(), domain.CreateApplicationRequest{
		CompanyName:   "Acme",
		PositionTitle: "Engineer",
		Tags:          []string{"go", " go ", "", "backend", "go"},
		Location:      []string{" Remote ", ""},
	}); err != nil {
		t.Fatal(err)
	}
	if got := fake.created.Tags; len(got) != 2 {
		t.Errorf("tags = %v, want de-duplicated and trimmed to 2", got)
	}
	if got := fake.created.Location; len(got) != 1 || got[0] != "Remote" {
		t.Errorf("location = %v, want [Remote]", got)
	}
}

func TestCreateCapsOverlongFields(t *testing.T) {
	svc, fake := newApps()
	if _, err := svc.Create(context.Background(), validUserID(), domain.CreateApplicationRequest{
		CompanyName:   strings.Repeat("a", 5000),
		PositionTitle: "Engineer",
	}); err != nil {
		t.Fatal(err)
	}
	if len(fake.created.CompanyName) > 200 {
		t.Errorf("company_name not capped: %d chars", len(fake.created.CompanyName))
	}
}

// Resumes must be a non-nil slice, or it marshals to JSON null and the frontend
// throws on .length.
func TestCreateInitialisesResumesToEmptySlice(t *testing.T) {
	svc, fake := newApps()
	if _, err := svc.Create(context.Background(), validUserID(), domain.CreateApplicationRequest{
		CompanyName: "Acme", PositionTitle: "Engineer",
	}); err != nil {
		t.Fatal(err)
	}
	if fake.created.Resumes == nil {
		t.Error("Resumes is nil; it will serialise as null")
	}
}

/* ---------- list bounds --------------------------------------------------- */

func TestListBoundsPaging(t *testing.T) {
	svc, fake := newApps()
	uid := validUserID()

	cases := []struct {
		name             string
		in               domain.ListApplicationsQuery
		wantPage, wantSz int
	}{
		{"defaults", domain.ListApplicationsQuery{}, 1, 20},
		{"negative page", domain.ListApplicationsQuery{Page: -5}, 1, 20},
		{"oversized page_size clamped", domain.ListApplicationsQuery{Page: 1, PageSize: 100000}, 1, 200},
		{"explicit", domain.ListApplicationsQuery{Page: 3, PageSize: 50}, 3, 50},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := svc.List(context.Background(), uid, tc.in); err != nil {
				t.Fatal(err)
			}
			if fake.listQ.Page != tc.wantPage || fake.listQ.PageSize != tc.wantSz {
				t.Errorf("page=%d size=%d, want page=%d size=%d",
					fake.listQ.Page, fake.listQ.PageSize, tc.wantPage, tc.wantSz)
			}
		})
	}
}

func TestListRejectsUnknownStatusFilter(t *testing.T) {
	svc, _ := newApps()
	var invalid metrics.InvalidInputError
	if _, err := svc.List(context.Background(), validUserID(), domain.ListApplicationsQuery{
		Status: "bogus",
	}); !errors.As(err, &invalid) {
		t.Errorf("got %v, want metrics.InvalidInputError", err)
	}
}

/* ---------- id handling --------------------------------------------------- */

// A malformed application ID reports not-found rather than a validation error:
// telling a prober which IDs are well-formed is information they shouldn't get.
func TestMalformedApplicationIDIsNotFound(t *testing.T) {
	svc, _ := newApps()
	uid := validUserID()

	if _, err := svc.Get(context.Background(), uid, "not-an-objectid"); !errors.Is(err, metrics.ErrApplicationNotFound) {
		t.Errorf("Get: got %v, want metrics.ErrApplicationNotFound", err)
	}
	if err := svc.Delete(context.Background(), uid, "not-an-objectid"); !errors.Is(err, metrics.ErrApplicationNotFound) {
		t.Errorf("Delete: got %v, want metrics.ErrApplicationNotFound", err)
	}
}
