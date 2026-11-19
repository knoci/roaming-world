package data

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/knoci/roaming-world/user/internal/biz"
	clientv3 "go.etcd.io/etcd/client/v3"
)

const (
	verificationTTLSeconds      = 600
	verificationCooldownSeconds = 60
	verificationMaxFailures     = 5
)

type verificationCode struct {
	Code     string
	Failures int
}

func verificationKey(target biz.VerificationTarget) string {
	identity := target.Purpose + "\x00" + target.UID + "\x00" + target.Email
	return fmt.Sprintf("email_verification:code:%x", sha256.Sum256([]byte(identity)))
}

func (r *userRepo) IssueVerificationCode(ctx context.Context, target biz.VerificationTarget, code string) (func(context.Context) error, error) {
	etcd := r.data.etcd
	cooldownKey := fmt.Sprintf("email_verification:cooldown:%x", sha256.Sum256([]byte(strings.ToLower(target.Email))))
	cooldown, err := etcd.Get(ctx, cooldownKey)
	if err != nil {
		return nil, err
	}
	if len(cooldown.Kvs) > 0 {
		return nil, biz.ErrVerificationThrottled
	}
	value, err := json.Marshal(verificationCode{Code: code})
	if err != nil {
		return nil, err
	}
	codeLease, err := etcd.Grant(ctx, verificationTTLSeconds)
	if err != nil {
		return nil, err
	}
	discard := func(ctx context.Context) error {
		_, err := etcd.Revoke(ctx, codeLease.ID)
		return err
	}
	cooldownLease, err := etcd.Grant(ctx, verificationCooldownSeconds)
	if err != nil {
		_ = discard(ctx)
		return nil, err
	}
	response, err := etcd.Txn(ctx).
		If(clientv3.Compare(clientv3.Version(cooldownKey), "=", 0)).
		Then(
			clientv3.OpPut(cooldownKey, "", clientv3.WithLease(cooldownLease.ID)),
			clientv3.OpPut(verificationKey(target), string(value), clientv3.WithLease(codeLease.ID)),
		).Commit()
	if err != nil || !response.Succeeded {
		_ = discard(ctx)
		_, _ = etcd.Revoke(ctx, cooldownLease.ID)
		if err != nil {
			return nil, err
		}
		return nil, biz.ErrVerificationThrottled
	}
	return discard, nil
}

func (r *userRepo) CheckVerificationCode(ctx context.Context, target biz.VerificationTarget, code string, consume bool) error {
	etcd := r.data.etcd
	key := verificationKey(target)
	for {
		response, err := etcd.Get(ctx, key)
		if err != nil {
			return err
		}
		if len(response.Kvs) == 0 {
			return biz.ErrVerificationExpired
		}
		row := response.Kvs[0]
		var state verificationCode
		if err := json.Unmarshal(row.Value, &state); err != nil {
			return err
		}
		if state.Failures >= verificationMaxFailures {
			return biz.ErrVerificationExpired
		}

		matches := subtle.ConstantTimeCompare([]byte(state.Code), []byte(code)) == 1
		if matches && !consume {
			return nil
		}
		operation := clientv3.OpDelete(key)
		if !matches {
			state.Failures++
			value, err := json.Marshal(state)
			if err != nil {
				return err
			}
			operation = clientv3.OpPut(key, string(value), clientv3.WithLease(clientv3.LeaseID(row.Lease)))
		}
		result, err := etcd.Txn(ctx).
			If(clientv3.Compare(clientv3.ModRevision(key), "=", row.ModRevision)).
			Then(operation).Commit()
		if err != nil {
			return err
		}
		if result.Succeeded {
			if matches {
				return nil
			}
			return biz.ErrVerificationExpired
		}
	}
}
