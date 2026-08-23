package integration_test

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/VanceMichael/go-base-aridlink-g04/internal/evidence"
	"github.com/VanceMichael/go-base-aridlink-g04/internal/platform"
)

func TestG04Task17SealMembershipRace(t *testing.T) {
	s := newSuite(t)
	seed := s.seedProgram()
	ctx := s.as(s.field)
	bundle, err := s.evidence.Create(ctx, seed.Site.ID, "", "")
	if err != nil {
		t.Fatalf("create evidence bundle: %v", err)
	}
	base := evidence.Item{Kind: "field_note", ObjectKey: "seal/base.json", Checksum: "0011223344556677"}
	late := evidence.Item{Kind: "remote_asset", ObjectKey: "seal/late.tif", Checksum: "8899aabbccddeeff"}
	if err := s.evidence.AddItems(ctx, bundle.ID, []evidence.Item{base}); err != nil {
		t.Fatalf("add base evidence: %v", err)
	}
	if _, err := s.store.DB().Exec(s.ctx, `CREATE OR REPLACE FUNCTION pause_late_evidence() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
	IF NEW.object_key='seal/late.tif' THEN
		PERFORM pg_sleep(0.3);
	END IF;
	RETURN NEW;
END $$`); err != nil {
		t.Fatalf("create evidence pause function: %v", err)
	}
	if _, err := s.store.DB().Exec(s.ctx, `CREATE TRIGGER pause_late_evidence BEFORE INSERT ON evidence_items FOR EACH ROW EXECUTE FUNCTION pause_late_evidence()`); err != nil {
		t.Fatalf("create evidence pause trigger: %v", err)
	}
	start := make(chan struct{})
	var wg sync.WaitGroup
	var addErr, sealErr error
	var digest string
	wg.Add(1)
	go func() {
		defer wg.Done()
		<-start
		addErr = s.evidence.AddItems(ctx, bundle.ID, []evidence.Item{late})
	}()
	wg.Add(1)
	go func() {
		defer wg.Done()
		<-start
		time.Sleep(50 * time.Millisecond)
		digest, sealErr = s.evidence.Seal(ctx, bundle.ID, bundle.Version)
	}()
	close(start)
	wg.Wait()
	if _, err := s.store.DB().Exec(s.ctx, `DROP TRIGGER pause_late_evidence ON evidence_items`); err != nil {
		t.Fatalf("remove evidence pause trigger: %v", err)
	}
	if addErr != nil {
		t.Fatalf("late evidence add: %v", addErr)
	}
	if sealErr != nil {
		if !errors.Is(sealErr, platform.ErrConflict) && !strings.Contains(sealErr.Error(), "40001") {
			t.Fatalf("seal failed unexpectedly: %v", sealErr)
		}
		found, err := s.evidence.Get(ctx, bundle.ID)
		if err != nil {
			t.Fatalf("read bundle after serialization conflict: %v", err)
		}
		digest, sealErr = s.evidence.Seal(ctx, bundle.ID, found.Version)
		if sealErr != nil {
			t.Fatalf("seal after serialization retry: %v", sealErr)
		}
	}
	parts := []string{base.Kind + ":" + base.ObjectKey + ":" + base.Checksum, late.Kind + ":" + late.ObjectKey + ":" + late.Checksum}
	sort.Strings(parts)
	sum := sha256.Sum256([]byte(strings.Join(parts, "\n")))
	expected := hex.EncodeToString(sum[:])
	if digest != expected {
		t.Fatalf("seal used stale membership: digest=%s expected=%s", digest, expected)
	}
}
