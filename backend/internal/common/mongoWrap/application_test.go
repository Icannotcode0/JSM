package mongoWrap

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/Icannotcode0/job-app-manager/backend/internal/domain"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// The store layer's whole job is translating to Mongo queries, so testing it
// against a fake driver would only assert that the mock matches the code. These
// run against a real server and skip when there isn't one, so `go test ./...`
// stays green on a machine with no database.
//
// Each test gets its own throwaway database, dropped on cleanup, so a failing
// run can never leave state behind or collide with the dev data.
func testDB(t *testing.T) *mongo.Database {
	t.Helper()

	uri := os.Getenv("MONGO_TEST_URI")
	if uri == "" {
		uri = "mongodb://127.0.0.1:27017"
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	client, err := mongo.Connect(options.Client().ApplyURI(uri))
	if err != nil {
		t.Skipf("no mongo at %s: %v", uri, err)
	}
	if err := client.Ping(ctx, nil); err != nil {
		_ = client.Disconnect(context.Background())
		t.Skipf("no mongo at %s: %v", uri, err)
	}

	db := client.Database("jsm_test_" + bson.NewObjectID().Hex())
	t.Cleanup(func() {
		_ = db.Drop(context.Background())
		_ = client.Disconnect(context.Background())
	})
	return db
}

func seedApp(t *testing.T, col *mongo.Collection, userID bson.ObjectID, company, status string) domain.Application {
	t.Helper()
	app, err := CreateApplication(context.Background(), col, domain.Application{
		UserID:        userID,
		CompanyName:   company,
		PositionTitle: "Engineer",
		Status:        status,
		Tags:          []string{"go"},
		AppliedAt:     time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("seed %s: %v", company, err)
	}
	return app
}

/* ---------- multi-tenancy ------------------------------------------------
   DATABASE.md requires user scoping in the filter itself, not as a check after
   reading. These tests are the enforcement of that rule.
   ------------------------------------------------------------------------- */

func TestApplicationsAreScopedByUser(t *testing.T) {
	col := testDB(t).Collection("applications")
	ctx := context.Background()

	alice, bob := bson.NewObjectID(), bson.NewObjectID()
	aliceApp := seedApp(t, col, alice, "Acme", "applied")
	seedApp(t, col, bob, "Globex", "applied")

	t.Run("get another user's document is not found", func(t *testing.T) {
		if _, err := FindApplicationByID(ctx, col, aliceApp.ID, bob); err != ErrApplicationNotFound {
			t.Errorf("got %v, want ErrApplicationNotFound", err)
		}
	})

	t.Run("update another user's document is not found", func(t *testing.T) {
		_, err := UpdateApplication(ctx, col, aliceApp.ID, bob, bson.M{"notes": "pwned"})
		if err != ErrApplicationNotFound {
			t.Errorf("got %v, want ErrApplicationNotFound", err)
		}
	})

	t.Run("delete another user's document is not found", func(t *testing.T) {
		if err := DeleteApplication(ctx, col, aliceApp.ID, bob); err != ErrApplicationNotFound {
			t.Errorf("got %v, want ErrApplicationNotFound", err)
		}
	})

	t.Run("the document survives all of that", func(t *testing.T) {
		got, err := FindApplicationByID(ctx, col, aliceApp.ID, alice)
		if err != nil {
			t.Fatal(err)
		}
		if got.Notes != "" {
			t.Errorf("notes = %q — another user's write landed", got.Notes)
		}
	})

	t.Run("list only returns your own", func(t *testing.T) {
		apps, total, err := ListApplications(ctx, col, bob, domain.ListApplicationsQuery{Page: 1, PageSize: 20})
		if err != nil {
			t.Fatal(err)
		}
		if total != 1 || len(apps) != 1 || apps[0].CompanyName != "Globex" {
			t.Errorf("bob sees total=%d %v, want only his own", total, apps)
		}
	})
}

/* ---------- filters and search ------------------------------------------- */

func TestListFiltersAndSearch(t *testing.T) {
	col := testDB(t).Collection("applications")
	ctx := context.Background()
	uid := bson.NewObjectID()

	seedApp(t, col, uid, "Acme Corp", "applied")
	seedApp(t, col, uid, "Globex", "offer")
	seedApp(t, col, uid, "Initech", "applied")

	q := func(query domain.ListApplicationsQuery) (int64, []domain.Application) {
		t.Helper()
		query.Page, query.PageSize = 1, 20
		apps, total, err := ListApplications(ctx, col, uid, query)
		if err != nil {
			t.Fatal(err)
		}
		return total, apps
	}

	if total, _ := q(domain.ListApplicationsQuery{Status: "applied"}); total != 2 {
		t.Errorf("status filter: total = %d, want 2", total)
	}
	if total, _ := q(domain.ListApplicationsQuery{Tag: "go"}); total != 3 {
		t.Errorf("tag filter: total = %d, want 3", total)
	}
	if total, apps := q(domain.ListApplicationsQuery{Search: "glob"}); total != 1 || apps[0].CompanyName != "Globex" {
		t.Errorf("search: total = %d %v, want Globex", total, apps)
	}
	if total, _ := q(domain.ListApplicationsQuery{Search: "ACME"}); total != 1 {
		t.Errorf("search should be case-insensitive: total = %d, want 1", total)
	}
}

// The search term goes into a Mongo $regex. Unescaped, ".*" would match every
// document and a crafted pattern could pin a CPU on backtracking.
func TestSearchTermIsRegexEscaped(t *testing.T) {
	col := testDB(t).Collection("applications")
	ctx := context.Background()
	uid := bson.NewObjectID()

	seedApp(t, col, uid, "Acme Corp", "applied")
	seedApp(t, col, uid, "Globex", "applied")

	for _, pattern := range []string{".*", "^A", "A|G", "(Acme|Globex)"} {
		apps, total, err := ListApplications(ctx, col, uid, domain.ListApplicationsQuery{
			Search: pattern, Page: 1, PageSize: 20,
		})
		if err != nil {
			t.Fatal(err)
		}
		if total != 0 {
			t.Errorf("%q matched %d documents (%v) — it was interpreted as a regex, not a literal",
				pattern, total, apps)
		}
	}
}

func TestListPagingIsStable(t *testing.T) {
	col := testDB(t).Collection("applications")
	ctx := context.Background()
	uid := bson.NewObjectID()

	// Identical applied_at on purpose: without _id as a tiebreaker the sort is
	// unstable and paging repeats or skips rows.
	for _, name := range []string{"A", "B", "C", "D", "E"} {
		seedApp(t, col, uid, name, "applied")
	}

	seen := map[string]bool{}
	for page := 1; page <= 5; page++ {
		apps, total, err := ListApplications(ctx, col, uid, domain.ListApplicationsQuery{Page: page, PageSize: 1})
		if err != nil {
			t.Fatal(err)
		}
		if total != 5 {
			t.Fatalf("total = %d, want 5", total)
		}
		if len(apps) != 1 {
			t.Fatalf("page %d returned %d rows, want 1", page, len(apps))
		}
		if seen[apps[0].ID.Hex()] {
			t.Fatalf("page %d repeated a document; the sort is unstable", page)
		}
		seen[apps[0].ID.Hex()] = true
	}
	if len(seen) != 5 {
		t.Errorf("saw %d distinct documents across 5 pages, want 5", len(seen))
	}
}

func TestEmptyPageReturnsEmptySliceNotNil(t *testing.T) {
	col := testDB(t).Collection("applications")
	apps, _, err := ListApplications(context.Background(), col, bson.NewObjectID(),
		domain.ListApplicationsQuery{Page: 1, PageSize: 20})
	if err != nil {
		t.Fatal(err)
	}
	// A nil slice marshals to JSON null, which the frontend types as an array.
	if apps == nil {
		t.Error("nil slice returned; it will serialise as null")
	}
}

/* ---------- update / delete ---------------------------------------------- */

func TestUpdateAppliesOnlyGivenFields(t *testing.T) {
	col := testDB(t).Collection("applications")
	ctx := context.Background()
	uid := bson.NewObjectID()
	app := seedApp(t, col, uid, "Acme", "applied")

	updated, err := UpdateApplication(ctx, col, app.ID, uid, bson.M{"status": "onsite"})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Status != "onsite" {
		t.Errorf("status = %q, want onsite", updated.Status)
	}
	if updated.CompanyName != "Acme" {
		t.Errorf("company_name = %q — an untouched field was cleared", updated.CompanyName)
	}
	if len(updated.Tags) != 1 {
		t.Errorf("tags = %v — an untouched field was cleared", updated.Tags)
	}
	if !updated.UpdatedAt.After(app.UpdatedAt) {
		t.Error("updated_at was not advanced")
	}
}

func TestUpdateWithNoFieldsIsANoOp(t *testing.T) {
	col := testDB(t).Collection("applications")
	ctx := context.Background()
	uid := bson.NewObjectID()
	app := seedApp(t, col, uid, "Acme", "applied")

	// An empty $set is rejected by Mongo, so this must not reach the driver.
	got, err := UpdateApplication(ctx, col, app.ID, uid, bson.M{})
	if err != nil {
		t.Fatalf("empty update errored: %v", err)
	}
	if got.CompanyName != "Acme" {
		t.Errorf("got %q, want the unchanged document", got.CompanyName)
	}
}

func TestDeleteIsNotFoundTheSecondTime(t *testing.T) {
	col := testDB(t).Collection("applications")
	ctx := context.Background()
	uid := bson.NewObjectID()
	app := seedApp(t, col, uid, "Acme", "applied")

	if err := DeleteApplication(ctx, col, app.ID, uid); err != nil {
		t.Fatal(err)
	}
	if err := DeleteApplication(ctx, col, app.ID, uid); err != ErrApplicationNotFound {
		t.Errorf("got %v, want ErrApplicationNotFound", err)
	}
}
