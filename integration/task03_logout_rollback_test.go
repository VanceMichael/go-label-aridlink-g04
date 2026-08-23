package integration_test

import (
	"testing"

	"github.com/VanceMichael/go-base-aridlink-g04/internal/auth"
	"github.com/VanceMichael/go-base-aridlink-g04/internal/platform"
)

func TestG04Task03LogoutRollback(t *testing.T) {
	s := newSuite(t)
	result, err := s.auth.Login(s.ctx, auth.Credentials{Email: "manager@aridlink.test", Password: "correct-horse-battery"})
	if err != nil {
		t.Fatalf("login manager: %v", err)
	}
	if _, err := s.store.DB().Exec(s.ctx, `CREATE OR REPLACE FUNCTION reject_logout_audit() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
	IF NEW.action='session.revoked' THEN
		RAISE EXCEPTION 'simulated audit sink failure';
	END IF;
	RETURN NEW;
END $$`); err != nil {
		t.Fatalf("create audit failure function: %v", err)
	}
	if _, err := s.store.DB().Exec(s.ctx, `CREATE TRIGGER reject_logout_audit BEFORE INSERT ON audit_entries FOR EACH ROW EXECUTE FUNCTION reject_logout_audit()`); err != nil {
		t.Fatalf("create audit failure trigger: %v", err)
	}

	if err := s.auth.Logout(s.ctx, result.Token); err == nil {
		t.Fatal("expected logout to report the audit failure")
	}
	if _, err := s.auth.Authenticate(s.ctx, result.Token); err != nil {
		t.Fatalf("session was revoked despite failed audit: %v", err)
	}
	var revoked int
	if err := s.store.DB().QueryRow(s.ctx, `SELECT count(*) FROM sessions s JOIN users u ON u.id=s.user_id WHERE u.email=$1 AND s.revoked_at IS NOT NULL`, "manager@aridlink.test").Scan(&revoked); err != nil {
		t.Fatalf("inspect session state: %v", err)
	}
	if revoked != 0 {
		t.Fatalf("failed logout left revoked sessions: %d", revoked)
	}
	var auditCount int
	if err := s.store.DB().QueryRow(s.ctx, `SELECT count(*) FROM audit_entries WHERE action='session.revoked' AND resource_id=$1`, result.User.ID).Scan(&auditCount); err != nil {
		t.Fatalf("inspect audit state: %v", err)
	}
	if auditCount != 0 {
		t.Fatalf("failed logout left audit entries: %d", auditCount)
	}

	if _, err := s.store.DB().Exec(s.ctx, `DROP TRIGGER reject_logout_audit ON audit_entries`); err != nil {
		t.Fatalf("remove audit failure trigger: %v", err)
	}
	if err := s.auth.Logout(s.ctx, result.Token); err != nil {
		t.Fatalf("logout after audit recovery: %v", err)
	}
	if _, err := s.auth.Authenticate(s.ctx, result.Token); err == nil {
		t.Fatal("revoked session was still accepted")
	} else if err != platform.ErrUnauthorized {
		t.Fatalf("unexpected authentication error after logout: %v", err)
	}
	if err := s.store.DB().QueryRow(s.ctx, `SELECT count(*) FROM audit_entries WHERE action='session.revoked' AND resource_id=$1`, result.User.ID).Scan(&auditCount); err != nil {
		t.Fatalf("inspect successful audit: %v", err)
	}
	if auditCount != 1 {
		t.Fatalf("successful logout wrote %d audit entries", auditCount)
	}
}
