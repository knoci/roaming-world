package data

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/knoci/roaming-world/user/internal/biz"
	clientv3 "go.etcd.io/etcd/client/v3"
)

func TestEmailVerification(t *testing.T) {
	endpoint := os.Getenv("ROAMING_TEST_ETCD_ENDPOINT")
	if endpoint == "" {
		t.Skip("set ROAMING_TEST_ETCD_ENDPOINT to a test etcd instance")
	}
	etcd, err := clientv3.New(clientv3.Config{
		Endpoints:   []string{endpoint},
		DialTimeout: 5 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer etcd.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	repo := &userRepo{data: &Data{etcd: etcd}}

	t.Run("scoped and consumed once", func(t *testing.T) {
		target := biz.VerificationTarget{
			Email: uuid.NewString() + "@example.test", Purpose: biz.VerificationChangeEmail, UID: "account-one",
		}
		const code = "018723"
		discard, err := repo.IssueVerificationCode(ctx, target, code)
		if err != nil {
			t.Fatal(err)
		}
		defer discard(ctx)

		otherPurpose := biz.VerificationTarget{Email: target.Email, Purpose: biz.VerificationResetPassword}
		if _, err := repo.IssueVerificationCode(ctx, otherPurpose, "286974"); !errors.Is(err, biz.ErrVerificationThrottled) {
			t.Fatalf("shared email cooldown returned %v", err)
		}
		if err := repo.CheckVerificationCode(ctx, otherPurpose, code, true); !errors.Is(err, biz.ErrVerificationExpired) {
			t.Fatalf("verification for a different purpose returned %v", err)
		}
		otherAccount := target
		otherAccount.UID = "account-two"
		if err := repo.CheckVerificationCode(ctx, otherAccount, code, true); !errors.Is(err, biz.ErrVerificationExpired) {
			t.Fatalf("verification for a different account returned %v", err)
		}
		if err := repo.CheckVerificationCode(ctx, target, code, false); err != nil {
			t.Fatalf("precheck failed: %v", err)
		}

		const workers = 12
		results := make(chan error, workers)
		start := make(chan struct{})
		for i := 0; i < workers; i++ {
			go func() {
				<-start
				results <- repo.CheckVerificationCode(ctx, target, code, true)
			}()
		}
		close(start)
		successes := 0
		for i := 0; i < workers; i++ {
			err := <-results
			switch {
			case err == nil:
				successes++
			case errors.Is(err, biz.ErrVerificationExpired):
			default:
				t.Errorf("unexpected verification error: %v", err)
			}
		}
		if successes != 1 {
			t.Fatalf("expected a single successful consumption, got %d", successes)
		}
	})

	t.Run("precheck failures share the attempt budget", func(t *testing.T) {
		target := biz.VerificationTarget{Email: uuid.NewString() + "@example.test", Purpose: biz.VerificationRegister}
		const code = "018723"
		discard, err := repo.IssueVerificationCode(ctx, target, code)
		if err != nil {
			t.Fatal(err)
		}
		defer discard(ctx)

		for attempt := 0; attempt < verificationMaxFailures; attempt++ {
			if err := repo.CheckVerificationCode(ctx, target, "286974", false); !errors.Is(err, biz.ErrVerificationExpired) {
				t.Fatalf("precheck with an incorrect code returned %v", err)
			}
		}
		if err := repo.CheckVerificationCode(ctx, target, code, true); !errors.Is(err, biz.ErrVerificationExpired) {
			t.Fatalf("consuming an exhausted code returned %v", err)
		}
	})
}
