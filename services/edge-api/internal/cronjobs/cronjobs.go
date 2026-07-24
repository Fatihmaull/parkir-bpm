// Package cronjobs — scheduler & 6 job terjadwal (PRD §8.3). Setiap job wajib idempoten;
// pada implementasi pgx tiap job mengambil advisory lock PostgreSQL untuk mencegah eksekusi
// ganda saat edge-api restart. Di mode memstore, lock tidak diperlukan (proses tunggal).
package cronjobs

import (
	"log/slog"
	"time"

	"github.com/robfig/cron/v3"
)

// Deps — fungsi kerja tiap job (di-inject agar dapat diuji tanpa scheduler).
type Deps struct {
	Now              func() time.Time
	ResetHours       int
	ExpireMembership func(now time.Time) int
	ResetPresence    func(now time.Time, hours int) int
	RetryOutbox      func() int
	VerifyAudit      func() (brokenSeq int64, ok bool)
	PurgeImages      func(olderThan time.Time) int
	ReconcilePayment func() (variance int, err error)
	Alert            func(typ, severity, msg string)
	Log              *slog.Logger
}

func (d *Deps) log() *slog.Logger {
	if d.Log != nil {
		return d.Log
	}
	return slog.Default()
}

// ── Job bodies (dipanggil scheduler; diuji langsung) ──

func (d *Deps) RunMembershipExpiry() {
	n := d.ExpireMembership(d.Now())
	if n > 0 {
		d.log().Info("cron: membership_expiry", "expired", n)
	}
}

func (d *Deps) RunAntipassbackReset() {
	n := d.ResetPresence(d.Now(), d.ResetHours)
	if n > 0 {
		d.log().Warn("cron: antipassback_reset", "reset", n)
		d.Alert("ANTIPASSBACK_AUTO_RESET", "warning", "reset presence otomatis")
	}
}

func (d *Deps) RunOutboxRetry() {
	if n := d.RetryOutbox(); n > 0 {
		d.log().Info("cron: outbox_retry", "requeued", n)
	}
}

func (d *Deps) RunAuditChainVerify() {
	if seq, ok := d.VerifyAudit(); !ok {
		d.log().Error("cron: audit_chain_verify RUSAK", "broken_seq", seq)
		d.Alert("AUDIT_CHAIN_BROKEN", "critical", "rantai audit rusak — verifikasi terjadwal gagal")
	}
}

func (d *Deps) RunImagePurge() {
	if d.PurgeImages == nil {
		return
	}
	cutoff := d.Now().Add(-7 * 24 * time.Hour) // retensi lokal 7 hari (§4.4)
	if n := d.PurgeImages(cutoff); n > 0 {
		d.log().Info("cron: image_purge", "deleted", n)
	}
}

func (d *Deps) RunPaymentReconcile() {
	if d.ReconcilePayment == nil {
		return
	}
	v, err := d.ReconcilePayment()
	if err != nil {
		d.log().Warn("cron: payment_reconcile gagal", "err", err)
		return
	}
	if v != 0 {
		d.Alert("PAYMENT_VARIANCE", "warning", "selisih rekonsiliasi pembayaran terdeteksi")
	}
}

// Register memasang 6 job ke scheduler dengan jadwal §8.3.
func Register(c *cron.Cron, d *Deps) error {
	if d.ResetHours == 0 {
		d.ResetHours = 18
	}
	specs := []struct {
		spec string
		fn   func()
	}{
		{"*/15 * * * *", d.RunMembershipExpiry},  // tiap 15 menit
		{"0 4 * * *", d.RunAntipassbackReset},     // 04:00
		{"0 * * * *", d.RunOutboxRetry},           // tiap jam
		{"0 2 * * *", d.RunImagePurge},            // 02:00
		{"0 3 * * *", d.RunAuditChainVerify},      // 03:00
		{"0 1 * * *", d.RunPaymentReconcile},      // 01:00
	}
	for _, s := range specs {
		if _, err := c.AddFunc(s.spec, s.fn); err != nil {
			return err
		}
	}
	return nil
}
