// Package api — capacity_authz_test.go
// Regression test for the horizontal-privilege fix on the capacity write routes
// (PUT /api/availability, POST /api/time-entries, POST /api/leave): a plain member
// may write only their OWN data; targeting another member's userId requires
// owner/admin. Without the fix, any member could set availability, log time, or
// file leave attributed to an arbitrary peer in the same org.
//
// DB-backed; skips cleanly when DATABASE_URL is unset.
package api

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/exo/gitstate/internal/config"
)

func TestCapacity_CrossMemberWriteRequiresManager(t *testing.T) {
	database := apiTestDB(t)
	defer database.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	const signingKey = "test-signing-key-for-capacity-authz"
	cfg := &config.Config{}
	cfg.Auth.JWTSigningKey = signingKey

	ns := time.Now().UnixNano()
	var orgID string
	if err := database.Pool().QueryRow(ctx,
		`INSERT INTO organizations (slug, name) VALUES ($1,$2) RETURNING id`,
		fmt.Sprintf("cap-authz-%d", ns), "Capacity AuthZ Org").Scan(&orgID); err != nil {
		t.Fatalf("create org: %v", err)
	}
	defer func() {
		_, _ = database.Pool().Exec(context.Background(), `DELETE FROM organizations WHERE id = $1`, orgID)
	}()

	attackerID, attackerTok := seedMember(t, ctx, database, signingKey, orgID, "member")
	victimID, _ := seedMember(t, ctx, database, signingKey, orgID, "member")
	_, ownerTok := seedMember(t, ctx, database, signingKey, orgID, "owner")

	mux := http.NewServeMux()
	RegisterCapacityRoutes(mux, database, cfg)

	do := func(method, path, token, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("X-Org-ID", orgID)
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		return rec
	}

	today := time.Now().UTC().Format("2006-01-02")

	// 1) A member targeting ANOTHER member's userId must be 403'd on every write.
	crossCases := []struct {
		name, method, path, body string
	}{
		{"availability", http.MethodPut, "/api/availability",
			fmt.Sprintf(`{"userId":%q,"weeklyHours":40}`, victimID)},
		{"timeEntry", http.MethodPost, "/api/time-entries",
			fmt.Sprintf(`{"userId":%q,"minutes":120,"occurredOn":%q}`, victimID, today)},
		{"leave", http.MethodPost, "/api/leave",
			fmt.Sprintf(`{"userId":%q,"startDate":%q,"endDate":%q}`, victimID, today, today)},
	}
	for _, c := range crossCases {
		rec := do(c.method, c.path, attackerTok, c.body)
		if rec.Code != http.StatusForbidden {
			t.Errorf("%s targeting peer as member: status = %d, want 403 (body=%s)",
				c.name, rec.Code, rec.Body.String())
		}
	}

	// 2) A member writing their OWN data (or omitting userId) must still succeed.
	selfCases := []struct {
		name, method, path string
		wantStatus         int
		body               string
	}{
		{"availabilitySelf", http.MethodPut, "/api/availability", http.StatusOK,
			fmt.Sprintf(`{"userId":%q,"weeklyHours":40}`, attackerID)},
		{"timeEntrySelf", http.MethodPost, "/api/time-entries", http.StatusCreated,
			fmt.Sprintf(`{"userId":%q,"minutes":60,"occurredOn":%q}`, attackerID, today)},
		{"leaveOmitUserID", http.MethodPost, "/api/leave", http.StatusCreated,
			fmt.Sprintf(`{"startDate":%q,"endDate":%q}`, today, today)},
	}
	for _, c := range selfCases {
		rec := do(c.method, c.path, attackerTok, c.body)
		if rec.Code != c.wantStatus {
			t.Errorf("%s as member: status = %d, want %d (body=%s)",
				c.name, rec.Code, c.wantStatus, rec.Body.String())
		}
	}

	// 3) A manager (owner) MAY write on behalf of another member.
	rec := do(http.MethodPut, "/api/availability", ownerTok,
		fmt.Sprintf(`{"userId":%q,"weeklyHours":32}`, victimID))
	if rec.Code != http.StatusOK {
		t.Errorf("availability for peer as owner: status = %d, want 200 (body=%s)",
			rec.Code, rec.Body.String())
	}
}
