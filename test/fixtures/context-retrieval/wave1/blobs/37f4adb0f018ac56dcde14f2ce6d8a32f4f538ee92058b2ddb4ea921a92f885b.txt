//go:build integration

package usecase_test

import (
	"context"
	"encoding/json"
	"os"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"

	driver "github.com/tarantool/go-tarantool/v2"
)

type verificationCleanupMetadata struct {
	Prefix                  string   `json:"prefix"`
	SeededAt                int64    `json:"seeded_at"`
	ExpiredPerKind          int      `json:"expired_per_kind"`
	SeededExpiredEmailCount int      `json:"seeded_expired_email_count"`
	LiveEmail               string   `json:"live_email"`
	LiveReceiptEmail        string   `json:"live_receipt_email"`
	LiveOperation           string   `json:"live_operation"`
	LiveResetEmail          string   `json:"live_reset_email"`
	LiveEmailUUID           string   `json:"live_email_uuid"`
	LiveCode                string   `json:"live_code"`
	LegacyUser              string   `json:"legacy_user"`
	LegacySandbox           string   `json:"legacy_sandbox"`
	OldTimestamp            int64    `json:"old_timestamp"`
	LiveDeadline            int64    `json:"live_deadline"`
	ExpiredEmailUUIDs       []string `json:"expired_email_uuids"`
	ExpiredLookupCodes      []string `json:"expired_lookup_codes"`
}

// Only the coordinator seeds disposable data. This test uses fixture SELECTs
// and waits for the real production sixty-second Lua cleanup fiber. It cannot
// force cleanup, evaluate Lua, mutate storage, or change clocks/configuration.
func TestActualVerificationCleanup(t *testing.T) {
	if os.Getenv("AUTH_VERIFICATION_CLEANUP_FIXTURE_ENABLED") != "true" {
		t.Skip("requires root-owned disposable cleanup fixture")
	}
	prefix := os.Getenv("AUTH_VERIFICATION_CLEANUP_PREFIX")
	if len(prefix) < 12 || len(prefix) > 96 || !regexp.MustCompile(`^t16[a-zA-Z0-9-]+-$`).MatchString(prefix) {
		t.Fatal("synthetic cleanup namespace required")
	}
	_, fixture := t16DirectVerification(t)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	marker := cleanupRows(t, ctx, fixture, "auth_verification_cleanup_fixture", "primary", prefix)
	if len(marker) != 1 || len(marker[0]) != 2 {
		t.Fatal("committed disposable cleanup seed marker required")
	}
	raw, ok := marker[0][1].(string)
	if !ok {
		t.Fatal("invalid cleanup marker format")
	}
	var metadata verificationCleanupMetadata
	if json.Unmarshal([]byte(raw), &metadata) != nil || metadata.Prefix != prefix || metadata.ExpiredPerKind < 1205 || metadata.SeededExpiredEmailCount != metadata.ExpiredPerKind+1 || len(metadata.ExpiredEmailUUIDs) != metadata.SeededExpiredEmailCount || len(metadata.ExpiredLookupCodes) != metadata.ExpiredPerKind || metadata.SeededAt <= 0 || metadata.OldTimestamp <= 0 || metadata.LiveDeadline <= time.Now().Unix() {
		t.Fatal("invalid committed cleanup seed inventory")
	}
	if !strings.HasPrefix(prefix, fixture.prefix) {
		t.Fatal("cleanup namespace outside owned test run")
	}
	liveKeys := []struct{ space, key string }{
		{"user_signup_space", metadata.LiveEmail},
		{"user_signup_consumption_receipts", metadata.LiveOperation},
		{"user_password_reset", metadata.LiveResetEmail},
		{"user_email_change", metadata.LiveEmailUUID},
		{"user_email_change_code_lookup", metadata.LiveCode},
		{"user", metadata.LegacyUser}, {"sandbox", metadata.LegacySandbox},
	}
	initial := map[string][]interface{}{}
	for _, entry := range liveKeys {
		rows := cleanupRows(t, ctx, fixture, entry.space, "primary", entry.key)
		if len(rows) != 1 {
			t.Fatal("retained fixture sentinel missing before cleanup observation")
		}
		initial[entry.space] = rows[0]
	}
	if timestamp, ok := directTestUnix(initial["user"][2]); !ok || timestamp != metadata.OldTimestamp {
		t.Fatal("legacy presence fixture does not have original old timestamp")
	}
	if len(initial["sandbox"]) != 6 || initial["sandbox"][4] != nil {
		t.Fatal("legacy sandbox missing-heartbeat retention fixture changed")
	}
	// Matching against exact owned prefixes/UUIDs avoids inspecting or counting
	// unrelated records. No captured tuple or credential is ever logged.
	deadline := time.Now().Add(75 * time.Second)
	var expired [5]int
	for {
		expired = cleanupExpiredCounts(t, ctx, fixture, &metadata)
		if expired == [5]int{} {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("real cleanup deadline exceeded: expired signup=%d receipt=%d email=%d reset=%d reverse_lookup=%d", expired[0], expired[1], expired[2], expired[3], expired[4])
		}
		timer := time.NewTimer(500 * time.Millisecond)
		select {
		case <-timer.C:
		case <-ctx.Done():
			timer.Stop()
			t.Fatal("cleanup observation timed out")
		}
	}
	for _, entry := range liveKeys {
		rows := cleanupRows(t, ctx, fixture, entry.space, "primary", entry.key)
		if len(rows) != 1 || !reflect.DeepEqual(rows[0], initial[entry.space]) {
			t.Fatal("cleanup changed live verification or retained legacy sentinel")
		}
	}
	mapping := initial["user_email_change_code_lookup"]
	if len(mapping) != 3 || mapping[1] != metadata.LiveEmailUUID {
		t.Fatal("live reverse mapping owner changed")
	}
	t.Log("real sixty-second verification fiber removed >1000 expired proofs/receipts and owned reverse indexes; live verification and legacy presence/sandbox sentinels retained")
}

func cleanupRows(t *testing.T, ctx context.Context, fixture *t16TarantoolFixture, space, index string, key interface{}) [][]interface{} {
	t.Helper()
	data, err := fixture.conn.Do(driver.NewSelectRequest(space).Index(index).Limit(^uint32(0)).Iterator(driver.IterEq).Key([]interface{}{key}).Context(ctx)).Get()
	if err != nil {
		t.Fatal("readonly cleanup fixture probe unavailable")
	}
	rows := make([][]interface{}, 0, len(data))
	for _, item := range data {
		row, ok := item.([]interface{})
		if !ok {
			t.Fatal("invalid cleanup fixture tuple format")
		}
		rows = append(rows, row)
	}
	return rows
}
func cleanupAllRows(t *testing.T, ctx context.Context, fixture *t16TarantoolFixture, space string) [][]interface{} {
	t.Helper()
	data, err := fixture.conn.Do(driver.NewSelectRequest(space).Index("primary").Limit(^uint32(0)).Iterator(driver.IterAll).Key([]interface{}{}).Context(ctx)).Get()
	if err != nil {
		t.Fatal("readonly cleanup inventory probe unavailable")
	}
	rows := make([][]interface{}, 0, len(data))
	for _, item := range data {
		row, ok := item.([]interface{})
		if !ok {
			t.Fatal("invalid cleanup inventory tuple format")
		}
		rows = append(rows, row)
	}
	return rows
}
func cleanupExpiredCounts(t *testing.T, ctx context.Context, fixture *t16TarantoolFixture, m *verificationCleanupMetadata) [5]int {
	t.Helper()
	var counts [5]int
	namespaces := []struct {
		space, prefix string
		field, index  int
	}{
		{"user_signup_space", m.Prefix + "expired-signup-", 0, 0},
		{"user_signup_consumption_receipts", m.Prefix + "expired-receipt-", 1, 1},
		{"user_email_change", m.Prefix + "expired-", 2, 2},
		{"user_password_reset", m.Prefix + "expired-reset-", 0, 3},
	}
	for _, namespace := range namespaces {
		for _, row := range cleanupAllRows(t, ctx, fixture, namespace.space) {
			if len(row) <= namespace.field {
				t.Fatal("short verification cleanup tuple")
			}
			key, ok := row[namespace.field].(string)
			if ok && strings.HasPrefix(key, namespace.prefix) {
				counts[namespace.index]++
			}
		}
	}
	// One snapshot avoids a round trip per code while counting only exact owned
	// keys. The shadow expired UUID shares a live code outside this inventory.
	ownedCodes := make(map[string]struct{}, len(m.ExpiredLookupCodes))
	for _, code := range m.ExpiredLookupCodes {
		ownedCodes[code] = struct{}{}
	}
	for _, row := range cleanupAllRows(t, ctx, fixture, "user_email_change_code_lookup") {
		if len(row) != 3 {
			t.Fatal("invalid reverse cleanup tuple format")
		}
		code, ok := row[0].(string)
		if _, owned := ownedCodes[code]; ok && owned {
			counts[4]++
		}
	}
	return counts
}
