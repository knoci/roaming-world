package auth

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

const (
	refreshKeyPrefix     = "auth:refresh:"
	userRefreshKeyPrefix = "auth:user_refresh:"
	userVersionKeyPrefix = "auth:user_ver:"
	jtiBlacklistPrefix   = "auth:blacklist:jti:"
)

// consumeRefreshScript atomically verifies a refresh token exists, deletes the single-use key,
// removes its hash from the user's active refresh token set, and returns the owning UID.
var consumeRefreshScript = redis.NewScript(`
local uid = redis.call('GET', KEYS[1])
if not uid then
	return nil
end
redis.call('DEL', KEYS[1])
redis.call('SREM', ARGV[1] .. uid, ARGV[2])
return uid
`)

// RevocationChecker checks whether an otherwise valid access token has been revoked
// via JTI blacklisting or a user-level token version bump.
type RevocationChecker interface {
	IsRevoked(ctx context.Context, uid, jti string, tokenVersion int64) (bool, error)
}

// RedisSessionStore persists hashed refresh tokens, user token versions, and JTI blacklists in Redis.
type RedisSessionStore struct {
	client redis.UniversalClient
}

var _ RevocationChecker = (*RedisSessionStore)(nil)

// NewRedisSessionStore constructs a RedisSessionStore backed by the provided Redis client.
func NewRedisSessionStore(client redis.UniversalClient) *RedisSessionStore {
	return &RedisSessionStore{client: client}
}

// SaveRefreshToken hashes rawToken with SHA-256 and stores the hash mapped to uid with TTL,
// tracking the hash in auth:user_refresh:{uid} so all sessions can be revoked together.
func (s *RedisSessionStore) SaveRefreshToken(ctx context.Context, uid, rawToken string, ttl time.Duration) error {
	if s == nil || s.client == nil {
		return errors.New("auth: redis client is nil")
	}
	if strings.TrimSpace(uid) == "" {
		return errors.New("auth: uid is required")
	}
	if strings.TrimSpace(rawToken) == "" {
		return errors.New("auth: refresh token is required")
	}
	if ttl <= 0 {
		ttl = DefaultRefreshTTL
	}

	hash := HashRefreshToken(rawToken)
	refreshKey := refreshKeyPrefix + hash
	userSetKey := userRefreshKeyPrefix + uid

	pipe := s.client.Pipeline()
	pipe.Set(ctx, refreshKey, uid, ttl)
	pipe.SAdd(ctx, userSetKey, hash)
	pipe.Expire(ctx, userSetKey, ttl)
	if _, err := pipe.Exec(ctx); err != nil {
		return fmt.Errorf("auth: save refresh token: %w", err)
	}
	return nil
}

// ConsumeRefreshToken atomically verifies and consumes a single-use refresh token,
// returning its owning uid. Subsequent attempts with the same token return ErrRefreshTokenInvalid.
func (s *RedisSessionStore) ConsumeRefreshToken(ctx context.Context, rawToken string) (string, error) {
	if s == nil || s.client == nil {
		return "", errors.New("auth: redis client is nil")
	}
	if strings.TrimSpace(rawToken) == "" {
		return "", ErrRefreshTokenInvalid
	}

	hash := HashRefreshToken(rawToken)
	refreshKey := refreshKeyPrefix + hash

	uid, err := consumeRefreshScript.Run(ctx, s.client, []string{refreshKey}, userRefreshKeyPrefix, hash).Text()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return "", ErrRefreshTokenInvalid
		}
		return "", fmt.Errorf("auth: consume refresh token: %w", err)
	}
	if strings.TrimSpace(uid) == "" {
		return "", ErrRefreshTokenInvalid
	}
	return uid, nil
}

// RevokeAllUserSessions bumps auth:user_ver:{uid} to newVersion (invalidating all older access tokens)
// and deletes all refresh tokens tracked in auth:user_refresh:{uid}.
func (s *RedisSessionStore) RevokeAllUserSessions(ctx context.Context, uid string, newVersion int64) error {
	if s == nil || s.client == nil {
		return errors.New("auth: redis client is nil")
	}
	if strings.TrimSpace(uid) == "" {
		return errors.New("auth: uid is required")
	}
	if newVersion <= 0 {
		return errors.New("auth: newVersion must be positive")
	}

	userSetKey := userRefreshKeyPrefix + uid
	hashes, err := s.client.SMembers(ctx, userSetKey).Result()
	if err != nil && !errors.Is(err, redis.Nil) {
		return fmt.Errorf("auth: list user refresh tokens: %w", err)
	}

	pipe := s.client.Pipeline()
	pipe.Set(ctx, userVersionKeyPrefix+uid, newVersion, 0)
	if len(hashes) > 0 {
		keys := make([]string, len(hashes))
		for i, h := range hashes {
			keys[i] = refreshKeyPrefix + h
		}
		pipe.Del(ctx, keys...)
	}
	pipe.Del(ctx, userSetKey)
	if _, err := pipe.Exec(ctx); err != nil {
		return fmt.Errorf("auth: revoke all user sessions: %w", err)
	}
	return nil
}

// RevokeAccessToken adds jti to auth:blacklist:jti:{jti} for its remaining lifetime.
// If ttl <= 0, the token is already expired and no Redis entry is needed.
func (s *RedisSessionStore) RevokeAccessToken(ctx context.Context, jti string, ttl time.Duration) error {
	if s == nil || s.client == nil {
		return errors.New("auth: redis client is nil")
	}
	if strings.TrimSpace(jti) == "" {
		return errors.New("auth: jti is required")
	}
	if ttl <= 0 {
		return nil
	}
	if err := s.client.Set(ctx, jtiBlacklistPrefix+jti, "1", ttl).Err(); err != nil {
		return fmt.Errorf("auth: revoke access token: %w", err)
	}
	return nil
}

// IsRevoked returns true if jti is present in auth:blacklist:jti:{jti}
// or if auth:user_ver:{uid} is strictly greater than tokenVersion.
func (s *RedisSessionStore) IsRevoked(ctx context.Context, uid, jti string, tokenVersion int64) (bool, error) {
	if s == nil || s.client == nil {
		return false, errors.New("auth: redis client is nil")
	}

	pipe := s.client.Pipeline()
	var existsCmd *redis.IntCmd
	var verCmd *redis.StringCmd

	if strings.TrimSpace(jti) != "" {
		existsCmd = pipe.Exists(ctx, jtiBlacklistPrefix+jti)
	}
	if strings.TrimSpace(uid) != "" {
		verCmd = pipe.Get(ctx, userVersionKeyPrefix+uid)
	}
	if existsCmd == nil && verCmd == nil {
		return false, nil
	}

	_, err := pipe.Exec(ctx)
	if err != nil && !errors.Is(err, redis.Nil) {
		return false, fmt.Errorf("auth: check revocation: %w", err)
	}

	if existsCmd != nil {
		n, e := existsCmd.Result()
		if e != nil && !errors.Is(e, redis.Nil) {
			return false, fmt.Errorf("auth: check jti blacklist: %w", e)
		}
		if n > 0 {
			return true, nil
		}
	}

	if verCmd != nil {
		v, e := verCmd.Int64()
		if e != nil {
			if errors.Is(e, redis.Nil) {
				return false, nil
			}
			return false, fmt.Errorf("auth: parse user token version: %w", e)
		}
		if v > tokenVersion {
			return true, nil
		}
	}

	return false, nil
}
