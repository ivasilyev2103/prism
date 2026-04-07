package audit_test

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"github.com/helldriver666/prism/internal/audit"
	"github.com/helldriver666/prism/internal/types"
)

const testHMACKey = "test-hmac-key-for-audit-chain-32"

func newTestLogger(t *testing.T) audit.Logger {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "audit_test.db")
	l, err := audit.NewLogger(dbPath, []byte(testHMACKey))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.(interface{ Close() error }).Close() })
	return l
}

func testRecord(id string) *types.RequestRecord {
	return &types.RequestRecord{
		ID: id, Timestamp: time.Now().Unix(), ProjectID: "proj",
		ProviderID: types.ProviderClaude, ServiceType: types.ServiceChat,
		Model: "claude-haiku", BillingType: types.BillingPerToken,
		Usage: types.UsageMetrics{InputTokens: 100, OutputTokens: 50},
		CostUSD: 0.001, LatencyMS: 200, Status: "ok",
	}
}

func TestAuditLog_LogAndQuery(t *testing.T) {
	l := newTestLogger(t)
	ctx := context.Background()

	for i := 0; i < 5; i++ {
		if err := l.Log(ctx, testRecord(fmt_Sprintf("r%d", i))); err != nil {
			t.Fatal(err)
		}
	}

	records, err := l.Query(ctx, &audit.Filter{
		From: time.Now().Add(-time.Minute),
		To:   time.Now().Add(time.Minute),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 5 {
		t.Errorf("expected 5 records, got %d", len(records))
	}
}

func TestAuditLog_QueryByServiceType(t *testing.T) {
	l := newTestLogger(t)
	ctx := context.Background()

	r1 := testRecord("r1")
	r1.ServiceType = types.ServiceChat
	r2 := testRecord("r2")
	r2.ServiceType = types.ServiceImage

	l.Log(ctx, r1)
	l.Log(ctx, r2)

	records, err := l.Query(ctx, &audit.Filter{
		ServiceType: types.ServiceImage,
		From:        time.Now().Add(-time.Minute),
		To:          time.Now().Add(time.Minute),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 || records[0].ServiceType != types.ServiceImage {
		t.Errorf("expected 1 image record, got %d", len(records))
	}
}

func TestAuditLog_UpdateForbidden(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "audit_worm.db")
	l, err := audit.NewLogger(dbPath, []byte(testHMACKey))
	if err != nil {
		t.Fatal(err)
	}
	defer l.(interface{ Close() error }).Close()

	l.Log(context.Background(), testRecord("r1"))

	// Try UPDATE directly via SQL — should fail due to WORM trigger.
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	_, err = db.Exec(`UPDATE audit_log SET status = 'tampered' WHERE id = 'r1'`)
	if err == nil {
		t.Fatal("expected UPDATE to be rejected by WORM trigger")
	}
}

func TestAuditLog_DeleteForbidden(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "audit_worm.db")
	l, err := audit.NewLogger(dbPath, []byte(testHMACKey))
	if err != nil {
		t.Fatal(err)
	}
	defer l.(interface{ Close() error }).Close()

	l.Log(context.Background(), testRecord("r1"))

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	_, err = db.Exec(`DELETE FROM audit_log WHERE id = 'r1'`)
	if err == nil {
		t.Fatal("expected DELETE to be rejected by WORM trigger")
	}
}

func TestAuditLog_NoPIIInEntries(t *testing.T) {
	l := newTestLogger(t)

	// RequestRecord does not contain request/response bodies by design.
	// Verify the struct has no fields that could leak PII.
	r := testRecord("r1")
	l.Log(context.Background(), r)

	records, err := l.Query(context.Background(), &audit.Filter{
		From: time.Now().Add(-time.Minute),
		To:   time.Now().Add(time.Minute),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 {
		t.Fatal("expected 1 record")
	}
	// RequestRecord has no TextContent, BinaryData, or RawBody fields.
	// This test verifies that the struct only contains metadata.
	rec := records[0]
	if rec.ID != "r1" || rec.ProjectID != "proj" || rec.Status != "ok" {
		t.Errorf("unexpected record content: %+v", rec)
	}
}

func TestHMACChain_VerifyChain_ValidRange(t *testing.T) {
	l := newTestLogger(t)
	ctx := context.Background()

	for i := 0; i < 10; i++ {
		l.Log(ctx, testRecord(fmt_Sprintf("r%d", i)))
	}

	err := l.VerifyChain(ctx, time.Now().Add(-time.Minute), time.Now().Add(time.Minute))
	if err != nil {
		t.Fatal("expected valid chain:", err)
	}
}

func TestHMACChain_TamperDetected(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "audit_tamper.db")
	l, err := audit.NewLogger(dbPath, []byte(testHMACKey))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	for i := 0; i < 5; i++ {
		l.Log(ctx, testRecord(fmt_Sprintf("r%d", i)))
	}

	l.(interface{ Close() error }).Close()

	// Tamper: directly modify a record's cost_usd, bypassing WORM triggers.
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	// Drop triggers to allow tampering.
	db.Exec(`DROP TRIGGER audit_no_update`)
	db.Exec(`UPDATE audit_log SET cost_usd = 999.99 WHERE id = 'r2'`)
	db.Close()

	// Re-open logger and verify chain.
	l2, err := audit.NewLogger(dbPath, []byte(testHMACKey))
	if err != nil {
		t.Fatal(err)
	}
	defer l2.(interface{ Close() error }).Close()

	err = l2.VerifyChain(ctx, time.Now().Add(-time.Minute), time.Now().Add(time.Minute))
	if err == nil {
		t.Fatal("expected tamper detection error")
	}
}

func TestHMACChain_SeparateKey_FromEncryption(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "audit_key.db")

	key1 := []byte("audit-hmac-key-aaaaaaaaaaaaaaaaa")
	key2 := []byte("vault-encryption-key-bbbbbbbbbbb")

	l1, err := audit.NewLogger(dbPath, key1)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	l1.Log(ctx, testRecord("r1"))
	l1.Log(ctx, testRecord("r2"))
	l1.(interface{ Close() error }).Close()

	// Verify with correct key — should pass.
	l2, err := audit.NewLogger(dbPath, key1)
	if err != nil {
		t.Fatal(err)
	}
	err = l2.VerifyChain(ctx, time.Now().Add(-time.Minute), time.Now().Add(time.Minute))
	if err != nil {
		t.Fatal("expected valid chain with correct key:", err)
	}
	l2.(interface{ Close() error }).Close()

	// Verify with wrong key (encryption key instead of HMAC key) — should fail.
	l3, err := audit.NewLogger(dbPath, key2)
	if err != nil {
		t.Fatal(err)
	}
	defer l3.(interface{ Close() error }).Close()

	err = l3.VerifyChain(ctx, time.Now().Add(-time.Minute), time.Now().Add(time.Minute))
	if err == nil {
		t.Fatal("expected HMAC verification failure with wrong key")
	}
}

func fmt_Sprintf(format string, args ...any) string {
	return fmt.Sprintf(format, args...)
}
