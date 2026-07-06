package biz

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/glebarez/sqlite"
	"github.com/go-kratos/kratos/v2/log"
	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"
	gormLogger "gorm.io/gorm/logger"

	"github.com/opscenter/ai-gateway/internal/data/model"
)

func newTestBilling(t *testing.T) (*BillingManager, *gorm.DB) {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: gormLogger.Default.LogMode(gormLogger.Silent)})
	if err != nil {
		t.Fatalf("sqlite: %v", err)
	}
	if err := db.AutoMigrate(&model.AIBillingAccount{}, &model.AIBillingLedger{}, &model.AIUsageDaily{},
		&model.AIPriceTable{}, &model.AIPriceTableItem{}, &model.AIModelItem{}, &model.AICreditsRate{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return NewBillingManager(db, rdb, nil, nil, log.NewStdLogger(testWriter{t})), db
}

func seedAccount(t *testing.T, db *gorm.DB, bm *BillingManager, tenantID uint, balanceCredits float64) *model.AIBillingAccount {
	t.Helper()
	acct := &model.AIBillingAccount{
		TenantID: tenantID, IsEnabled: true, Mode: model.BillingModePrepaid,
		Currency: "CNY", BalanceMicro: int64(balanceCredits * model.MicroCreditScale),
		Status: model.BillingStatusActive, GraceHours: 0,
	}
	if err := db.Create(acct).Error; err != nil {
		t.Fatalf("seed account: %v", err)
	}
	// GORM omits zero-value fields on insert (column default 24 would win)
	if err := db.Model(acct).Update("grace_hours", 0).Error; err != nil {
		t.Fatalf("seed grace_hours: %v", err)
	}
	acct.GraceHours = 0
	bm.seedBalance(context.Background(), acct)
	return acct
}

func TestLedgerIdempotency(t *testing.T) {
	bm, db := newTestBilling(t)
	acct := seedAccount(t, db, bm, 1, 100)

	task := ledgerTask{accountID: acct.ID, entryType: model.LedgerEntryDeduct, amountMicro: -5_000_000, idemKey: "req:abc"}
	bm.applyLedger(task)
	bm.applyLedger(task) // replay must be a no-op

	var count int64
	db.Model(&model.AIBillingLedger{}).Where("account_id = ?", acct.ID).Count(&count)
	if count != 1 {
		t.Fatalf("ledger rows = %d, want 1 (idempotent)", count)
	}
	var after model.AIBillingAccount
	db.First(&after, acct.ID)
	if after.BalanceMicro != 95_000_000 {
		t.Fatalf("balance = %d, want 95000000", after.BalanceMicro)
	}
}

func TestLedgerBalanceChainConsistent(t *testing.T) {
	bm, db := newTestBilling(t)
	acct := seedAccount(t, db, bm, 2, 10)

	bm.applyLedger(ledgerTask{accountID: acct.ID, entryType: model.LedgerEntryRecharge, amountMicro: 20_000_000, idemKey: "r1"})
	bm.applyLedger(ledgerTask{accountID: acct.ID, entryType: model.LedgerEntryDeduct, amountMicro: -3_000_000, idemKey: "d1"})
	bm.applyLedger(ledgerTask{accountID: acct.ID, entryType: model.LedgerEntryDeduct, amountMicro: -7_000_000, idemKey: "d2"})

	var rows []model.AIBillingLedger
	db.Where("account_id = ?", acct.ID).Order("id asc").Find(&rows)
	running := int64(10_000_000) // starting balance
	for _, r := range rows {
		running += r.AmountMicro
		if r.BalanceAfterMicro != running {
			t.Fatalf("balance_after chain broken at %s: got %d want %d", r.IdempotencyKey, r.BalanceAfterMicro, running)
		}
	}
	var after model.AIBillingAccount
	db.First(&after, acct.ID)
	if after.BalanceMicro != running {
		t.Fatalf("account balance %d != ledger sum %d", after.BalanceMicro, running)
	}
}

func TestSuspensionAndRecovery(t *testing.T) {
	bm, db := newTestBilling(t)
	acct := seedAccount(t, db, bm, 3, 1) // 1 credit, no grace

	// burn past zero → suspended
	bm.applyLedger(ledgerTask{accountID: acct.ID, entryType: model.LedgerEntryDeduct, amountMicro: -2_000_000, idemKey: "burn"})
	var after model.AIBillingAccount
	db.First(&after, acct.ID)
	if after.Status != model.BillingStatusSuspended {
		t.Fatalf("status = %s, want suspended", after.Status)
	}

	// Admit must reject
	bm.acctCache.Delete(uint(3))
	_, err := bm.Admit(context.Background(), 3, 1, "m", []byte(`{}`))
	if err == nil {
		t.Fatal("suspended account must be rejected")
	}

	// recharge above zero → active again
	if _, err := bm.Recharge(context.Background(), 3, 10, "topup-1", "manual", ""); err != nil {
		t.Fatalf("recharge: %v", err)
	}
	db.First(&after, acct.ID)
	if after.Status != model.BillingStatusActive {
		t.Fatalf("status after recharge = %s, want active", after.Status)
	}
}

func TestGraceTransition(t *testing.T) {
	bm, db := newTestBilling(t)
	acct := &model.AIBillingAccount{
		TenantID: 4, IsEnabled: true, Mode: model.BillingModePrepaid, Currency: "CNY",
		BalanceMicro: 1_000_000, Status: model.BillingStatusActive, GraceHours: 24,
	}
	db.Create(acct)
	bm.seedBalance(context.Background(), acct)

	bm.applyLedger(ledgerTask{accountID: acct.ID, entryType: model.LedgerEntryDeduct, amountMicro: -2_000_000, idemKey: "g1"})
	var after model.AIBillingAccount
	db.First(&after, acct.ID)
	if after.Status != model.BillingStatusGrace {
		t.Fatalf("status = %s, want grace", after.Status)
	}
	if after.GraceUntil == nil || time.Until(*after.GraceUntil) < 23*time.Hour {
		t.Fatal("grace_until not set ~24h ahead")
	}
	// grace accounts are still admitted (within window)
	bm.acctCache.Delete(uint(4))
	if _, err := bm.Admit(context.Background(), 4, 1, "m", []byte(`{}`)); err != nil {
		t.Fatalf("grace account should be admitted during window: %v", err)
	}
}

func TestFreezeInsufficientBalance(t *testing.T) {
	bm, db := newTestBilling(t)
	acct := seedAccount(t, db, bm, 5, 1)

	// price the model so the estimate is expensive: 1000 credits per million output
	db.Create(&model.AIModelItem{ProviderID: 9, Name: "pricey", InputPricePerMillion: 1000, OutputPricePerMillion: 1000})
	db.Create(&model.AICreditsRate{Currency: "CNY", RatePerCredit: 0.01, IsEnabled: true})

	_, err := bm.Admit(context.Background(), 5, 9, "pricey", []byte(`{"max_tokens": 100000}`))
	if err == nil {
		t.Fatal("expected insufficient balance rejection")
	}
	_ = acct
}

func TestSettleRefundsOverFreeze(t *testing.T) {
	bm, db := newTestBilling(t)
	acct := seedAccount(t, db, bm, 6, 100)
	ctx := context.Background()

	h := &FreezeHandle{Account: acct, EstMicro: 10_000_000}
	// simulate the freeze having decremented Redis
	bm.rdb.DecrBy(ctx, "ai:gw:bill:bal:"+uintStr(acct.ID), 10_000_000)

	bm.Settle(ctx, h, "req-1", 4_000_000, "up-1", "test")
	// Redis headroom should be 100M - 10M + (10M-4M) = 96M
	val, _ := bm.rdb.Get(ctx, "ai:gw:bill:bal:"+uintStr(acct.ID)).Int64()
	if val != 96_000_000 {
		t.Fatalf("redis headroom = %d, want 96000000", val)
	}
	// drain the async ledger task
	select {
	case task := <-bm.ledgerQ:
		bm.applyLedger(task)
	case <-time.After(time.Second):
		t.Fatal("no ledger task enqueued")
	}
	var after model.AIBillingAccount
	db.First(&after, acct.ID)
	if after.BalanceMicro != 96_000_000 {
		t.Fatalf("db balance = %d, want 96000000", after.BalanceMicro)
	}
}

func uintStr(v uint) string {
	return strconv.FormatUint(uint64(v), 10)
}
