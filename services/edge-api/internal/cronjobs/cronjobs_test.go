package cronjobs

import (
	"testing"
	"time"

	"github.com/robfig/cron/v3"
)

func TestRegisterAddsSixJobs(t *testing.T) {
	c := cron.New()
	d := &Deps{
		Now:              time.Now,
		ExpireMembership: func(time.Time) int { return 0 },
		ResetPresence:    func(time.Time, int) int { return 0 },
		RetryOutbox:      func() int { return 0 },
		VerifyAudit:      func() (int64, bool) { return 0, true },
		Alert:            func(string, string, string) {},
	}
	if err := Register(c, d); err != nil {
		t.Fatal(err)
	}
	if len(c.Entries()) != 6 {
		t.Fatalf("harus 6 job terjadwal, got %d", len(c.Entries()))
	}
}

func TestAntipassbackResetAlertsWhenReset(t *testing.T) {
	var alerts []string
	d := &Deps{
		Now:           time.Now,
		ResetHours:    18,
		ResetPresence: func(_ time.Time, hours int) int { if hours != 18 { t.Fatalf("hours=%d", hours) }; return 2 },
		Alert:         func(typ, _, _ string) { alerts = append(alerts, typ) },
	}
	d.RunAntipassbackReset()
	if len(alerts) != 1 || alerts[0] != "ANTIPASSBACK_AUTO_RESET" {
		t.Fatalf("harus alert ANTIPASSBACK_AUTO_RESET, got %v", alerts)
	}
}

func TestAuditVerifyAlertsWhenBroken(t *testing.T) {
	var crit int
	d := &Deps{
		Now:         time.Now,
		VerifyAudit: func() (int64, bool) { return 7, false },
		Alert:       func(_, sev, _ string) { if sev == "critical" { crit++ } },
	}
	d.RunAuditChainVerify()
	if crit != 1 {
		t.Fatal("rantai rusak harus memicu alert critical")
	}
}

func TestMembershipExpiryNoAlertNoop(t *testing.T) {
	called := false
	d := &Deps{Now: time.Now, ExpireMembership: func(time.Time) int { called = true; return 0 }, Alert: func(string, string, string) {}}
	d.RunMembershipExpiry()
	if !called {
		t.Fatal("ExpireMembership harus dipanggil")
	}
}
