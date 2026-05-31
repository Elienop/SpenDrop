package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/elienop/spendrop/internal/database"
	"github.com/elienop/spendrop/internal/sse"
)

// invalidatePayload is the JSON shape carried in the SSE `data:` line of an
// `event: invalidate` frame: {"resources":[...]}.
type invalidatePayload struct {
	Resources []string `json:"resources"`
}

// subscribeAndCapture wires a fresh in-memory hub onto the handler, registers
// one buffered SSE client owned by the given user id, and returns a drain
// function plus a cancel.
func subscribeAndCapture(t *testing.T, h *Handler, userID int64) (drain func() []string, cancel func()) {
	t.Helper()
	hub := sse.NewHub()
	ctx, cancelFn := context.WithCancel(context.Background())
	go hub.Run(ctx)
	h.SetEventBroker(hub)

	client := sse.NewClient(userID)
	if err := hub.Register(client); err != nil {
		t.Fatalf("register sse client: %v", err)
	}
	ch := client.Events()

	drain = func() []string {
		deadline := time.Now().Add(500 * time.Millisecond)
		var resources []string
		for time.Now().Before(deadline) {
			select {
			case frame := <-ch:
				resources = append(resources, parseInvalidateResources(t, frame)...)
			default:
				if len(resources) > 0 {
					return resources
				}
				time.Sleep(5 * time.Millisecond)
			}
		}
		return resources
	}
	cancel = func() {
		hub.Unregister(client)
		cancelFn()
	}
	return drain, cancel
}

// parseInvalidateResources extracts the resources array from a raw SSE frame
// of the form "event: invalidate\ndata: {...}\n\n". Non-data lines are ignored.
func parseInvalidateResources(t *testing.T, frame []byte) []string {
	t.Helper()
	for _, line := range bytes.Split(frame, []byte("\n")) {
		s := strings.TrimSpace(string(line))
		if !strings.HasPrefix(s, "data:") {
			continue
		}
		jsonPart := strings.TrimSpace(strings.TrimPrefix(s, "data:"))
		var p invalidatePayload
		if err := json.Unmarshal([]byte(jsonPart), &p); err != nil {
			t.Fatalf("parse invalidate data %q: %v", jsonPart, err)
		}
		return p.Resources
	}
	return nil
}

func hasAll(got []string, want ...string) bool {
	set := make(map[string]struct{}, len(got))
	for _, g := range got {
		set[g] = struct{}{}
	}
	for _, w := range want {
		if _, ok := set[w]; !ok {
			return false
		}
	}
	return true
}

func TestHandleRestoreTransaction_BroadcastsTrashResources(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	admin := seedTestUser(t, q, "admin", RoleAdmin)

	tombstoned := seedTombstonedTestTransaction(t, q, admin.ID, 1, "2026-04-01", 12.50, "gone")

	drain, cancel := subscribeAndCapture(t, h, admin.ID)
	defer cancel()

	req := httptest.NewRequest(http.MethodPost, "/api/transactions/"+itoa(tombstoned.ID)+"/restore", nil)
	req = withUserAndURLParam(req, admin, "id", itoa(tombstoned.ID))
	rec := httptest.NewRecorder()
	h.handleRestoreTransaction(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("restore: status=%d body=%s", rec.Code, rec.Body.String())
	}
	got := drain()
	if !hasAll(got, "trash", "transactions", "dashboard", "reports", "budgets") {
		t.Errorf("restore broadcast resources=%v, want superset of [trash transactions dashboard reports budgets]", got)
	}
}

func TestHandleCreateCategory_BroadcastsCategoryResources(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	admin := seedTestUser(t, q, "admin", RoleAdmin)

	drain, cancel := subscribeAndCapture(t, h, admin.ID)
	defer cancel()

	body := `{"name":"New Cat","type":"expense","sort_order":0}`
	req := httptest.NewRequest(http.MethodPost, "/api/categories", strings.NewReader(body))
	req = withUser(req, admin)
	rec := httptest.NewRecorder()
	h.handleCreateCategory(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("create category: status=%d body=%s", rec.Code, rec.Body.String())
	}
	got := drain()
	if !hasAll(got, "categories", "dashboard", "reports") {
		t.Errorf("create-category broadcast resources=%v, want superset of [categories dashboard reports]", got)
	}
}

// TestPublishInvalidate_RepresentativeResourceSets guards every DISTINCT
// resource-set shape produced by the non-transaction publish sites, beyond the
// trash-restore and create-category cases covered above. Each row reuses the
// shared subscribeAndCapture/drain/hasAll plumbing and exercises a real handler
// end-to-end so a wrong resource set, a pre-commit call, or a wrong success
// guard at one of these sites is caught automatically:
//   - currency create -> single-resource "currencies"
//   - savings set     -> "savings","reports"
//   - budget set      -> "budgets","reports"
//   - purge single    -> "trash"
func TestPublishInvalidate_RepresentativeResourceSets(t *testing.T) {
	tests := []struct {
		name string
		// invoke seeds whatever the handler needs, fires it, and returns the
		// recorder + the status code the handler must produce on success.
		invoke   func(t *testing.T, h *Handler, q *database.Queries, admin database.User) *httptest.ResponseRecorder
		wantCode int
		want     []string
	}{
		{
			name: "currency create broadcasts currencies",
			invoke: func(t *testing.T, h *Handler, q *database.Queries, admin database.User) *httptest.ResponseRecorder {
				body := `{"code":"EUR","name":"Euro","symbol":"€","rate_to_base":1.1}`
				req := httptest.NewRequest(http.MethodPost, "/api/currencies", strings.NewReader(body))
				req = withUser(req, admin)
				rec := httptest.NewRecorder()
				h.handleCreateCurrency(rec, req)
				return rec
			},
			wantCode: http.StatusCreated,
			want:     []string{"currencies"},
		},
		{
			name: "savings set broadcasts savings+reports",
			invoke: func(t *testing.T, h *Handler, q *database.Queries, admin database.User) *httptest.ResponseRecorder {
				body := `{"target_amount":15000}`
				req := httptest.NewRequest(http.MethodPut, "/api/savings-goals/2026", strings.NewReader(body))
				req = withUserAndURLParam(req, admin, "year", "2026")
				rec := httptest.NewRecorder()
				h.handleSetSavingsGoal(rec, req)
				return rec
			},
			wantCode: http.StatusOK,
			want:     []string{"savings", "reports"},
		},
		{
			name: "budget set broadcasts budgets+reports",
			invoke: func(t *testing.T, h *Handler, q *database.Queries, admin database.User) *httptest.ResponseRecorder {
				body := `{"amount":1200}`
				req := httptest.NewRequest(http.MethodPost, "/api/budgets/2026/4", strings.NewReader(body))
				req = withUserAndURLParams(req, admin, map[string]string{"year": "2026", "month": "4"})
				rec := httptest.NewRecorder()
				h.handleSetBudget(rec, req)
				return rec
			},
			wantCode: http.StatusOK,
			want:     []string{"budgets", "reports"},
		},
		{
			name: "purge single broadcasts trash",
			invoke: func(t *testing.T, h *Handler, q *database.Queries, admin database.User) *httptest.ResponseRecorder {
				tombstoned := seedTombstonedTestTransaction(t, q, admin.ID, 1, "2026-04-01", 12.50, "gone")
				req := httptest.NewRequest(http.MethodDelete, "/api/transactions/"+itoa(tombstoned.ID)+"/purge", nil)
				req = withUserAndURLParam(req, admin, "id", itoa(tombstoned.ID))
				rec := httptest.NewRecorder()
				h.handlePurgeTransaction(rec, req)
				return rec
			},
			wantCode: http.StatusOK,
			want:     []string{"trash"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			q, db := setupTestDB(t)
			h := NewHandler(q, db)
			admin := seedTestUser(t, q, "admin", RoleAdmin)

			drain, cancel := subscribeAndCapture(t, h, admin.ID)
			defer cancel()

			rec := tc.invoke(t, h, q, admin)
			if rec.Code != tc.wantCode {
				t.Fatalf("%s: status=%d body=%s", tc.name, rec.Code, rec.Body.String())
			}
			got := drain()
			if !hasAll(got, tc.want...) {
				t.Errorf("%s broadcast resources=%v, want superset of %v", tc.name, got, tc.want)
			}
		})
	}
}
