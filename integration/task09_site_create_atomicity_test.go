package integration_test

import (
	"testing"

	"github.com/VanceMichael/go-base-aridlink-g04/internal/site"
)

func TestG04Task09SiteCreateAtomicity(t *testing.T) {
	s := newSuite(t)
	ctx := s.as(s.manager)
	seed := s.seedProgram()
	if _, err := s.store.DB().Exec(s.ctx, `CREATE OR REPLACE FUNCTION reject_site_audit() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
	IF NEW.action='site.proposed' THEN
		RAISE EXCEPTION 'simulated site audit failure';
	END IF;
	RETURN NEW;
END $$`); err != nil {
		t.Fatalf("create site failure function: %v", err)
	}
	if _, err := s.store.DB().Exec(s.ctx, `CREATE TRIGGER reject_site_audit BEFORE INSERT ON audit_entries FOR EACH ROW EXECUTE FUNCTION reject_site_audit()`); err != nil {
		t.Fatalf("create site failure trigger: %v", err)
	}

	created, err := s.site.Create(ctx, structInput(seed.Program.ID, s.partnerOrganization))
	if err == nil {
		t.Fatal("expected site audit failure")
	}
	if created.ID == "" {
		t.Fatal("site create did not return the attempted identity")
	}
	if count := s.count("sites"); count != 1 {
		t.Fatalf("failed site creation left %d site rows, want only the seeded site", count)
	}
	var audits int
	if err := s.store.DB().QueryRow(s.ctx, `SELECT count(*) FROM audit_entries WHERE action='site.proposed' AND resource_id=$1`, created.ID).Scan(&audits); err != nil {
		t.Fatalf("inspect site audit: %v", err)
	}
	if audits != 0 {
		t.Fatalf("failed site creation left %d audit rows", audits)
	}

	if _, err := s.store.DB().Exec(s.ctx, `DROP TRIGGER reject_site_audit ON audit_entries`); err != nil {
		t.Fatalf("remove site failure trigger: %v", err)
	}
	created, err = s.site.Create(ctx, structInput(seed.Program.ID, s.partnerOrganization))
	if err != nil {
		t.Fatalf("site create after audit recovery: %v", err)
	}
	if created.Status != "proposed" || s.count("sites") != 2 {
		t.Fatalf("unexpected recovered site: %+v", created)
	}
}

// Keep the scenario focused on the site aggregate while using the public input type.
func structInput(programID, organizationID string) site.CreateInput {
	return site.CreateInput{ProgramID: programID, OrganizationID: organizationID, Name: "Supplementary dune site", CountryCode: "JOR", AreaHectares: 75, Ecosystem: "dryland"}
}
