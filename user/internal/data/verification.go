package data

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/knoci/roaming-world/user/internal/biz"
	"github.com/redis/go-redis/v9"
)

const (
	verificationTTLSeconds      = 600
	verificationCooldownSeconds = 60
	verificationMaxFailures     = 5
)

// issueVerificationScript atomically enforces email cooldown and stores the verification code.
// KEYS[1]: cooldownKey
// KEYS[2]: codeKey
// ARGV[1]: cooldownTTLSeconds
// ARGV[2]: value ("code:0")
// ARGV[3]: codeTTLSeconds
// Returns 0 if throttled, 1 if stored.
var issueVerificationScript = redis.NewScript(`
if redis.call('EXISTS', KEYS[1]) == 1 then
	return 0
end
redis.call('SET', KEYS[1], '1', 'EX', tonumber(ARGV[1]))
redis.call('SET', KEYS[2], ARGV[2], 'EX', tonumber(ARGV[3]))
return 1
`)

// checkVerificationScript atomically validates a verification code, enforces max failures,
// increments failure counts preserving TTL via KEEPTTL on mismatch, and optionally consumes the key on match.
// KEYS[1]: codeKey
// ARGV[1]: submitted code
// ARGV[2]: consume flag ("1" or "0")
// ARGV[3]: maxFailures
// Returns:
//   1  -> matched (and deleted when consume == "1")
//   0  -> mismatch (failures incremented with KEEPTTL, or deleted if max reached)
//  -1  -> missing, malformed, or already exceeded max failures
var checkVerificationScript = redis.NewScript(`
local raw = redis.call('GET', KEYS[1])
if not raw then
	return -1
end
local sep = string.find(raw, ':', 1, true)
if not sep then
	return -1
end
local stored = string.sub(raw, 1, sep - 1)
local failures = tonumber(string.sub(raw, sep + 1)) or 0
local maxFailures = tonumber(ARGV[3]) or 5
if failures >= maxFailures then
	redis.call('DEL', KEYS[1])
	return -1
end
if stored == ARGV[1] then
	if ARGV[2] == '1' then
		redis.call('DEL', KEYS[1])
	end
	return 1
end
failures = failures + 1
if failures >= maxFailures then
	redis.call('DEL', KEYS[1])
else
	redis.call('SET', KEYS[1], stored .. ':' .. tostring(failures), 'KEEPTTL')
end
return 0
`)

func verificationKey(target biz.VerificationTarget) string {
	identity := target.Purpose + "\x00" + target.UID + "\x00" + normalizeEmail(target.Email)
	return fmt.Sprintf("email_verification:code:%x", sha256.Sum256([]byte(identity)))
}

func verificationCooldownKey(email string) string {
	return fmt.Sprintf("email_verification:cooldown:%x", sha256.Sum256([]byte(normalizeEmail(email))))
}

func formatVerificationValue(code string, failures int) string {
	return fmt.Sprintf("%s:%d", code, failures)
}

func parseVerificationValue(raw string) (code string, failures int, ok bool) {
	idx := strings.IndexByte(raw, ':')
	if idx <= 0 || idx >= len(raw)-1 {
		return "", 0, false
	}
	n, err := strconv.Atoi(raw[idx+1:])
	if err != nil || n < 0 {
		return "", 0, false
	}
	return raw[:idx], n, true
}

func (r *userRepo) IssueVerificationCode(ctx context.Context, target biz.VerificationTarget, code string) (func(context.Context) error, error) {
	if r == nil || r.data == nil || r.data.redis == nil {
		return nil, errors.New("verification: redis client is nil")
	}
	rdb := r.data.redis
	cooldownKey := verificationCooldownKey(target.Email)
	codeKey := verificationKey(target)

	res, err := issueVerificationScript.Run(
		ctx,
		rdb,
		[]string{cooldownKey, codeKey},
		verificationCooldownSeconds,
		formatVerificationValue(code, 0),
		verificationTTLSeconds,
	).Int64()
	if err != nil {
		return nil, err
	}
	if res == 0 {
		return nil, biz.ErrVerificationThrottled
	}

	discard := func(discardCtx context.Context) error {
		return rdb.Del(discardCtx, cooldownKey, codeKey).Err()
	}
	return discard, nil
}

func (r *userRepo) CheckVerificationCode(ctx context.Context, target biz.VerificationTarget, code string, consume bool) error {
	if r == nil || r.data == nil || r.data.redis == nil {
		return errors.New("verification: redis client is nil")
	}
	key := verificationKey(target)
	consumeArg := "0"
	if consume {
		consumeArg = "1"
	}

	res, err := checkVerificationScript.Run(
		ctx,
		r.data.redis,
		[]string{key},
		code,
		consumeArg,
		verificationMaxFailures,
	).Int64()
	if err != nil {
		return err
	}
	switch res {
	case 1:
		return nil
	case 0, -1:
		return biz.ErrVerificationExpired
	default:
		return fmt.Errorf("verification: unexpected script result %d", res)
	}
}
