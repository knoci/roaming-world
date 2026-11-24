package auth

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-kratos/kratos/v2/metadata"
	"github.com/go-kratos/kratos/v2/transport"
	khttp "github.com/go-kratos/kratos/v2/transport/http"
	jwt "github.com/golang-jwt/jwt/v5"
	"github.com/redis/go-redis/v9"
	gmetadata "google.golang.org/grpc/metadata"
)

func setTestSigningKey(t *testing.T) []byte {
	t.Helper()
	ResetKeyCacheForTest()
	t.Cleanup(ResetKeyCacheForTest)

	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatalf("generate random key: %v", err)
	}
	t.Setenv("JWT_SIGNING_KEY", base64.StdEncoding.EncodeToString(key))
	return key
}

func TestValidateJWTSigningKeyAndCaching(t *testing.T) {
	ResetKeyCacheForTest()
	t.Cleanup(ResetKeyCacheForTest)

	t.Setenv("JWT_SIGNING_KEY", "")
	if err := ValidateJWTSigningKey(); !errors.Is(err, ErrInvalidSigningKey) {
		t.Fatalf("expected ErrInvalidSigningKey for empty env, got %v", err)
	}

	t.Setenv("JWT_SIGNING_KEY", "not-valid-base64!!!")
	if err := ValidateJWTSigningKey(); !errors.Is(err, ErrInvalidSigningKey) {
		t.Fatalf("expected ErrInvalidSigningKey for bad base64, got %v", err)
	}

	shortKey := base64.StdEncoding.EncodeToString(make([]byte, 31))
	t.Setenv("JWT_SIGNING_KEY", shortKey)
	if err := ValidateJWTSigningKey(); !errors.Is(err, ErrInvalidSigningKey) {
		t.Fatalf("expected ErrInvalidSigningKey for 31-byte key, got %v", err)
	}

	validKey := base64.StdEncoding.EncodeToString(make([]byte, 32))
	t.Setenv("JWT_SIGNING_KEY", validKey)
	if err := ValidateJWTSigningKey(); err != nil {
		t.Fatalf("expected valid 32-byte key to pass, got %v", err)
	}

	// Changing env after validation must still use cached key until ResetKeyCacheForTest.
	t.Setenv("JWT_SIGNING_KEY", "")
	if err := ValidateJWTSigningKey(); err != nil {
		t.Fatalf("expected cached key to remain valid, got %v", err)
	}

	ResetKeyCacheForTest()
	if err := ValidateJWTSigningKey(); !errors.Is(err, ErrInvalidSigningKey) {
		t.Fatalf("expected ErrInvalidSigningKey after reset, got %v", err)
	}
}

func TestGenerateAndParseAccessToken(t *testing.T) {
	setTestSigningKey(t)

	tok, jti, err := GenerateAccessToken("u-123", "Alice", "alice@example.com", "https://img/a.png", "admin", 3, 15*time.Minute)
	if err != nil {
		t.Fatalf("GenerateAccessToken failed: %v", err)
	}
	if jti == "" {
		t.Fatalf("expected non-empty jti")
	}
	// Standard HS256 JWT has 3 dot-separated segments (no AES-GCM wrapper).
	if parts := strings.Split(tok, "."); len(parts) != 3 {
		t.Fatalf("expected standard 3-segment JWT, got %d segments", len(parts))
	}

	claims, err := ParseAccessToken(tok)
	if err != nil {
		t.Fatalf("ParseAccessToken failed: %v", err)
	}
	if claims.UID != "u-123" || claims.Name != "Alice" || claims.Email != "alice@example.com" ||
		claims.Avatar != "https://img/a.png" || claims.Role != "admin" || claims.TokenVersion != 3 {
		t.Fatalf("unexpected claims: %+v", claims)
	}
	if claims.ID != jti {
		t.Fatalf("expected jti %q, got %q", jti, claims.ID)
	}
	if claims.Issuer != DefaultIssuer {
		t.Fatalf("expected issuer %q, got %q", DefaultIssuer, claims.Issuer)
	}
	if len(claims.Audience) != 1 || claims.Audience[0] != DefaultAudience {
		t.Fatalf("expected audience [%q], got %v", DefaultAudience, claims.Audience)
	}
	if claims.ExpiresAt == nil || time.Until(claims.ExpiresAt.Time) <= 14*time.Minute {
		t.Fatalf("unexpected expiration: %v", claims.ExpiresAt)
	}
}

func TestGenerateAccessTokenDefaultsAndValidation(t *testing.T) {
	setTestSigningKey(t)

	if _, _, err := GenerateAccessToken("", "Alice", "", "", "user", 1, time.Minute); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("expected ErrInvalidToken for empty uid, got %v", err)
	}

	tok, _, err := GenerateAccessToken("u-default", "Bob", "", "", "user", 0, 0)
	if err != nil {
		t.Fatalf("GenerateAccessToken with defaults failed: %v", err)
	}
	claims, err := ParseAccessToken(tok)
	if err != nil {
		t.Fatalf("ParseAccessToken failed: %v", err)
	}
	if claims.TokenVersion != 1 {
		t.Fatalf("expected default TokenVersion 1, got %d", claims.TokenVersion)
	}
	remaining := time.Until(claims.ExpiresAt.Time)
	if remaining < DefaultAccessTTL-time.Minute || remaining > DefaultAccessTTL+time.Second {
		t.Fatalf("expected default TTL around %v, got %v", DefaultAccessTTL, remaining)
	}
}

func TestParseAccessTokenRejections(t *testing.T) {
	key := setTestSigningKey(t)

	signCustom := func(method jwt.SigningMethod, c *Claims, signingKey any) string {
		t.Helper()
		s, err := jwt.NewWithClaims(method, c).SignedString(signingKey)
		if err != nil {
			t.Fatalf("sign custom token: %v", err)
		}
		return s
	}

	now := time.Now()
	cases := []struct {
		name  string
		token string
	}{
		{
			name:  "empty",
			token: "",
		},
		{
			name:  "malformed",
			token: "not.a.jwt",
		},
		{
			name: "wrong algorithm HS512",
			token: signCustom(jwt.SigningMethodHS512, &Claims{
				UID: "u-1",
				RegisteredClaims: jwt.RegisteredClaims{
					Issuer:    DefaultIssuer,
					Audience:  jwt.ClaimStrings{DefaultAudience},
					ExpiresAt: jwt.NewNumericDate(now.Add(time.Hour)),
				},
			}, key),
		},
		{
			name: "wrong key",
			token: signCustom(jwt.SigningMethodHS256, &Claims{
				UID: "u-1",
				RegisteredClaims: jwt.RegisteredClaims{
					Issuer:    DefaultIssuer,
					Audience:  jwt.ClaimStrings{DefaultAudience},
					ExpiresAt: jwt.NewNumericDate(now.Add(time.Hour)),
				},
			}, make([]byte, 32)),
		},
		{
			name: "expired",
			token: signCustom(jwt.SigningMethodHS256, &Claims{
				UID: "u-1",
				RegisteredClaims: jwt.RegisteredClaims{
					Issuer:    DefaultIssuer,
					Audience:  jwt.ClaimStrings{DefaultAudience},
					ExpiresAt: jwt.NewNumericDate(now.Add(-time.Minute)),
				},
			}, key),
		},
		{
			name: "missing exp",
			token: signCustom(jwt.SigningMethodHS256, &Claims{
				UID: "u-1",
				RegisteredClaims: jwt.RegisteredClaims{
					Issuer:   DefaultIssuer,
					Audience: jwt.ClaimStrings{DefaultAudience},
				},
			}, key),
		},
		{
			name: "wrong issuer",
			token: signCustom(jwt.SigningMethodHS256, &Claims{
				UID: "u-1",
				RegisteredClaims: jwt.RegisteredClaims{
					Issuer:    "other-issuer",
					Audience:  jwt.ClaimStrings{DefaultAudience},
					ExpiresAt: jwt.NewNumericDate(now.Add(time.Hour)),
				},
			}, key),
		},
		{
			name: "wrong audience",
			token: signCustom(jwt.SigningMethodHS256, &Claims{
				UID: "u-1",
				RegisteredClaims: jwt.RegisteredClaims{
					Issuer:    DefaultIssuer,
					Audience:  jwt.ClaimStrings{"other-api"},
					ExpiresAt: jwt.NewNumericDate(now.Add(time.Hour)),
				},
			}, key),
		},
		{
			name: "empty uid",
			token: signCustom(jwt.SigningMethodHS256, &Claims{
				UID: "",
				RegisteredClaims: jwt.RegisteredClaims{
					Issuer:    DefaultIssuer,
					Audience:  jwt.ClaimStrings{DefaultAudience},
					ExpiresAt: jwt.NewNumericDate(now.Add(time.Hour)),
				},
			}, key),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := ParseAccessToken(tc.token); !errors.Is(err, ErrInvalidToken) {
				t.Fatalf("expected ErrInvalidToken, got %v", err)
			}
		})
	}
}

func TestConcurrentTokenOperations(t *testing.T) {
	setTestSigningKey(t)

	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			uid := fmt.Sprintf("user-%d", id)
			tok, _, err := GenerateAccessToken(uid, "name", "e@x.com", "", "user", 1, time.Minute)
			if err != nil {
				t.Errorf("GenerateAccessToken: %v", err)
				return
			}
			claims, err := ParseAccessToken(tok)
			if err != nil {
				t.Errorf("ParseAccessToken: %v", err)
				return
			}
			if claims.UID != uid {
				t.Errorf("got uid %q, want %q", claims.UID, uid)
			}
		}(i)
	}
	wg.Wait()
}

func TestRefreshTokenGenerationAndHashing(t *testing.T) {
	t1, err := GenerateRefreshToken()
	if err != nil {
		t.Fatalf("GenerateRefreshToken failed: %v", err)
	}
	t2, err := GenerateRefreshToken()
	if err != nil {
		t.Fatalf("GenerateRefreshToken second call failed: %v", err)
	}
	if t1 == t2 {
		t.Fatalf("expected unique refresh tokens")
	}
	rawBytes, err := base64.RawURLEncoding.DecodeString(t1)
	if err != nil || len(rawBytes) != 32 {
		t.Fatalf("expected 32-byte RawURLEncoding token, err=%v len=%d", err, len(rawBytes))
	}

	h1 := HashRefreshToken(t1)
	if len(h1) != 64 || h1 == t1 {
		t.Fatalf("unexpected SHA-256 hex digest: %q", h1)
	}
	if HashRefreshToken(t1) != h1 {
		t.Fatalf("HashRefreshToken must be deterministic")
	}
	if HashRefreshToken(t2) == h1 {
		t.Fatalf("distinct tokens must produce distinct hashes")
	}
}

type fakeHeader map[string][]string

func (h fakeHeader) Get(key string) string {
	if vals := h[key]; len(vals) > 0 {
		return vals[0]
	}
	return ""
}
func (h fakeHeader) Set(key, value string) { h[key] = []string{value} }
func (h fakeHeader) Add(key, value string) { h[key] = append(h[key], value) }
func (h fakeHeader) Keys() []string {
	keys := make([]string, 0, len(h))
	for k := range h {
		keys = append(keys, k)
	}
	return keys
}
func (h fakeHeader) Values(key string) []string { return h[key] }

type fakeTransporter struct {
	hdr fakeHeader
}

func (f fakeTransporter) Kind() transport.Kind            { return transport.KindGRPC }
func (f fakeTransporter) Endpoint() string                { return "" }
func (f fakeTransporter) Operation() string               { return "" }
func (f fakeTransporter) RequestHeader() transport.Header { return f.hdr }
func (f fakeTransporter) ReplyHeader() transport.Header   { return fakeHeader{} }

func TestExtractBearerTokenAllSources(t *testing.T) {
	// Missing in background context.
	if tok, present := ExtractBearerToken(context.Background()); tok != "" || present {
		t.Fatalf("expected ('', false), got (%q, %v)", tok, present)
	}

	// 1. Kratos HTTP server context via real khttp.Server handler.
	t.Run("kratos http request context", func(t *testing.T) {
		srv := khttp.NewServer()
		var gotTok string
		var gotPresent bool
		srv.Route("/").GET("/probe", func(ctx khttp.Context) error {
			gotTok, gotPresent = ExtractBearerToken(ctx)
			return ctx.String(http.StatusOK, "ok")
		})

		req := httptest.NewRequest(http.MethodGet, "/probe", nil)
		req.Header.Set("Authorization", "Bearer http-tok-123")
		rec := httptest.NewRecorder()
		srv.ServeHTTP(rec, req)
		if gotTok != "http-tok-123" || !gotPresent {
			t.Fatalf("got (%q, %v), want ('http-tok-123', true)", gotTok, gotPresent)
		}

		// Present but invalid scheme.
		reqBad := httptest.NewRequest(http.MethodGet, "/probe", nil)
		reqBad.Header.Set("Authorization", "Basic dXNlcjpwYXNz")
		recBad := httptest.NewRecorder()
		srv.ServeHTTP(recBad, reqBad)
		if gotTok != "" || !gotPresent {
			t.Fatalf("got (%q, %v), want ('', true)", gotTok, gotPresent)
		}
	})

	// 2. Kratos transport.FromServerContext.
	t.Run("kratos transport context", func(t *testing.T) {
		ctx := transport.NewServerContext(context.Background(), fakeTransporter{
			hdr: fakeHeader{"Authorization": {"bearer transport-tok"}},
		})
		if tok, present := ExtractBearerToken(ctx); tok != "transport-tok" || !present {
			t.Fatalf("got (%q, %v), want ('transport-tok', true)", tok, present)
		}
	})

	// 3. Kratos metadata.FromServerContext.
	t.Run("kratos metadata context", func(t *testing.T) {
		md := metadata.New(map[string][]string{"authorization": {"Bearer kratos-md-tok"}})
		ctx := metadata.NewServerContext(context.Background(), md)
		if tok, present := ExtractBearerToken(ctx); tok != "kratos-md-tok" || !present {
			t.Fatalf("got (%q, %v), want ('kratos-md-tok', true)", tok, present)
		}
	})

	// 4. gRPC incoming metadata (including fallback when Kratos metadata lacks authorization).
	t.Run("grpc incoming metadata", func(t *testing.T) {
		ctx := metadata.NewServerContext(context.Background(), metadata.New(map[string][]string{"x-other": {"1"}}))
		ctx = gmetadata.NewIncomingContext(ctx, gmetadata.Pairs("authorization", "Bearer grpc-tok-999"))
		if tok, present := ExtractBearerToken(ctx); tok != "grpc-tok-999" || !present {
			t.Fatalf("got (%q, %v), want ('grpc-tok-999', true)", tok, present)
		}
	})

	// Malformed Bearer variants.
	for _, raw := range []string{"Bearer ", "Bearer   ", "Bearer a b", "Token xyz", ""} {
		ctx := gmetadata.NewIncomingContext(context.Background(), gmetadata.Pairs("authorization", raw))
		tok, present := ExtractBearerToken(ctx)
		if tok != "" || !present {
			t.Fatalf("header %q: got (%q, %v), want ('', true)", raw, tok, present)
		}
	}
}

type stubRevocationChecker struct {
	revoked bool
	err     error
	called  bool
}

func (s *stubRevocationChecker) IsRevoked(_ context.Context, _, _ string, _ int64) (bool, error) {
	s.called = true
	return s.revoked, s.err
}

func TestParseClaimsFromContext(t *testing.T) {
	setTestSigningKey(t)

	// Missing token -> ErrMissingToken.
	if _, err := ParseClaimsFromContext(context.Background(), nil); !errors.Is(err, ErrMissingToken) {
		t.Fatalf("expected ErrMissingToken, got %v", err)
	}

	// Non-Bearer header -> ErrInvalidToken.
	ctxBadHdr := gmetadata.NewIncomingContext(context.Background(), gmetadata.Pairs("authorization", "Basic abc"))
	if _, err := ParseClaimsFromContext(ctxBadHdr, nil); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("expected ErrInvalidToken for Basic header, got %v", err)
	}

	// Invalid JWT -> ErrInvalidToken.
	ctxBadJWT := gmetadata.NewIncomingContext(context.Background(), gmetadata.Pairs("authorization", "Bearer not.a.jwt"))
	if _, err := ParseClaimsFromContext(ctxBadJWT, nil); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("expected ErrInvalidToken for malformed jwt, got %v", err)
	}

	// Valid JWT without checker.
	tok, _, err := GenerateAccessToken("u-42", "Eve", "eve@example.com", "", "user", 2, time.Minute)
	if err != nil {
		t.Fatalf("GenerateAccessToken: %v", err)
	}
	ctxValid := gmetadata.NewIncomingContext(context.Background(), gmetadata.Pairs("authorization", "Bearer "+tok))
	claims, err := ParseClaimsFromContext(ctxValid, nil)
	if err != nil || claims.UID != "u-42" || claims.TokenVersion != 2 {
		t.Fatalf("expected valid claims, got %+v, err=%v", claims, err)
	}

	// Valid JWT with revocation checker returning revoked=true.
	checker := &stubRevocationChecker{revoked: true}
	if _, err := ParseClaimsFromContext(ctxValid, checker); !errors.Is(err, ErrTokenRevoked) {
		t.Fatalf("expected ErrTokenRevoked, got %v", err)
	}
	if !checker.called {
		t.Fatalf("expected checker to be called")
	}

	// Valid JWT with revocation checker returning backend error.
	boom := errors.New("redis down")
	errChecker := &stubRevocationChecker{err: boom}
	if _, err := ParseClaimsFromContext(ctxValid, errChecker); !errors.Is(err, boom) {
		t.Fatalf("expected wrapped backend error, got %v", err)
	}
}

// --- Lightweight in-process RESP2 server for testing RedisSessionStore with real go-redis/v9 ---

type fakeRedisEntry struct {
	val    string
	expire time.Time
}

type fakeRedisServer struct {
	ln      net.Listener
	mu      sync.Mutex
	strings map[string]fakeRedisEntry
	sets    map[string]map[string]struct{}
	setExp  map[string]time.Time
	scripts map[string]string
	closed  atomic.Bool
}

func startFakeRedis(t *testing.T) (*fakeRedisServer, *redis.Client) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen fake redis: %v", err)
	}
	s := &fakeRedisServer{
		ln:      ln,
		strings: make(map[string]fakeRedisEntry),
		sets:    make(map[string]map[string]struct{}),
		setExp:  make(map[string]time.Time),
		scripts: make(map[string]string),
	}
	go s.acceptLoop()

	client := redis.NewClient(&redis.Options{
		Addr:     ln.Addr().String(),
		Protocol: 2,
	})
	t.Cleanup(func() {
		_ = client.Close()
		s.closed.Store(true)
		_ = ln.Close()
	})
	return s, client
}

func (s *fakeRedisServer) acceptLoop() {
	for {
		conn, err := s.ln.Accept()
		if err != nil {
			return
		}
		go s.serveConn(conn)
	}
}

func (s *fakeRedisServer) serveConn(conn net.Conn) {
	defer conn.Close()
	r := bufio.NewReader(conn)
	w := bufio.NewWriter(conn)

	for {
		args, err := readRESPArray(r)
		if err != nil {
			return
		}
		if len(args) == 0 {
			continue
		}
		resp := s.exec(args)
		if _, err := w.WriteString(resp); err != nil {
			return
		}
		if err := w.Flush(); err != nil {
			return
		}
	}
}

func readRESPArray(r *bufio.Reader) ([]string, error) {
	line, err := r.ReadString('\n')
	if err != nil {
		return nil, err
	}
	line = strings.TrimRight(line, "\r\n")
	if !strings.HasPrefix(line, "*") {
		return nil, fmt.Errorf("unexpected RESP prefix: %q", line)
	}
	n, err := strconv.Atoi(line[1:])
	if err != nil {
		return nil, err
	}
	args := make([]string, n)
	for i := 0; i < n; i++ {
		hdr, err := r.ReadString('\n')
		if err != nil {
			return nil, err
		}
		hdr = strings.TrimRight(hdr, "\r\n")
		if !strings.HasPrefix(hdr, "$") {
			return nil, fmt.Errorf("unexpected bulk hdr: %q", hdr)
		}
		sz, err := strconv.Atoi(hdr[1:])
		if err != nil {
			return nil, err
		}
		buf := make([]byte, sz+2)
		if _, err := io.ReadFull(r, buf); err != nil {
			return nil, err
		}
		args[i] = string(buf[:sz])
	}
	return args, nil
}

func respBulk(v string) string {
	return fmt.Sprintf("$%d\r\n%s\r\n", len(v), v)
}

func (s *fakeRedisServer) isExpiredLocked(key string) bool {
	if e, ok := s.strings[key]; ok {
		if !e.expire.IsZero() && time.Now().After(e.expire) {
			delete(s.strings, key)
			return true
		}
	}
	if exp, ok := s.setExp[key]; ok && !exp.IsZero() && time.Now().After(exp) {
		delete(s.sets, key)
		delete(s.setExp, key)
		return true
	}
	return false
}

func (s *fakeRedisServer) exec(args []string) string {
	cmd := strings.ToUpper(args[0])
	s.mu.Lock()
	defer s.mu.Unlock()

	switch cmd {
	case "CLIENT", "PING":
		if cmd == "PING" {
			return "+PONG\r\n"
		}
		return "+OK\r\n"
	case "SET":
		key, val := args[1], args[2]
		var exp time.Time
		for i := 3; i < len(args)-1; i++ {
			opt := strings.ToUpper(args[i])
			switch opt {
			case "EX":
				sec, _ := strconv.Atoi(args[i+1])
				exp = time.Now().Add(time.Duration(sec) * time.Second)
				i++
			case "PX":
				ms, _ := strconv.Atoi(args[i+1])
				exp = time.Now().Add(time.Duration(ms) * time.Millisecond)
				i++
			}
		}
		s.strings[key] = fakeRedisEntry{val: val, expire: exp}
		return "+OK\r\n"
	case "GET":
		key := args[1]
		s.isExpiredLocked(key)
		e, ok := s.strings[key]
		if !ok {
			return "$-1\r\n"
		}
		return respBulk(e.val)
	case "EXISTS":
		count := 0
		for _, k := range args[1:] {
			s.isExpiredLocked(k)
			if _, ok := s.strings[k]; ok {
				count++
			} else if _, ok := s.sets[k]; ok {
				count++
			}
		}
		return fmt.Sprintf(":%d\r\n", count)
	case "DEL":
		count := 0
		for _, k := range args[1:] {
			if _, ok := s.strings[k]; ok {
				delete(s.strings, k)
				count++
			}
			if _, ok := s.sets[k]; ok {
				delete(s.sets, k)
				delete(s.setExp, k)
				count++
			}
		}
		return fmt.Sprintf(":%d\r\n", count)
	case "SADD":
		key := args[1]
		s.isExpiredLocked(key)
		if s.sets[key] == nil {
			s.sets[key] = make(map[string]struct{})
		}
		added := 0
		for _, m := range args[2:] {
			if _, exists := s.sets[key][m]; !exists {
				s.sets[key][m] = struct{}{}
				added++
			}
		}
		return fmt.Sprintf(":%d\r\n", added)
	case "SREM":
		key := args[1]
		s.isExpiredLocked(key)
		removed := 0
		if set := s.sets[key]; set != nil {
			for _, m := range args[2:] {
				if _, exists := set[m]; exists {
					delete(set, m)
					removed++
				}
			}
		}
		return fmt.Sprintf(":%d\r\n", removed)
	case "SMEMBERS":
		key := args[1]
		s.isExpiredLocked(key)
		set := s.sets[key]
		var b strings.Builder
		b.WriteString(fmt.Sprintf("*%d\r\n", len(set)))
		for m := range set {
			b.WriteString(respBulk(m))
		}
		return b.String()
	case "EXPIRE", "PEXPIRE":
		key := args[1]
		n, _ := strconv.Atoi(args[2])
		var d time.Duration
		if cmd == "EXPIRE" {
			d = time.Duration(n) * time.Second
		} else {
			d = time.Duration(n) * time.Millisecond
		}
		if e, ok := s.strings[key]; ok {
			e.expire = time.Now().Add(d)
			s.strings[key] = e
			return ":1\r\n"
		}
		if _, ok := s.sets[key]; ok {
			s.setExp[key] = time.Now().Add(d)
			return ":1\r\n"
		}
		return ":0\r\n"
	case "EVALSHA":
		sha := args[1]
		script, ok := s.scripts[sha]
		if !ok {
			return "-NOSCRIPT No matching script. Please use EVAL.\r\n"
		}
		return s.runConsumeScriptLocked(script, args[2:])
	case "EVAL":
		script := args[1]
		s.scripts[consumeRefreshScript.Hash()] = script
		return s.runConsumeScriptLocked(script, args[2:])
	default:
		return fmt.Sprintf("-ERR unsupported command %s\r\n", cmd)
	}
}

func (s *fakeRedisServer) runConsumeScriptLocked(script string, rest []string) string {
	// Validates the exact consumeRefreshScript semantics atomically under s.mu.
	if !strings.Contains(script, "SREM") || len(rest) < 4 {
		return "-ERR unexpected script\r\n"
	}
	numKeys, _ := strconv.Atoi(rest[0])
	if numKeys != 1 {
		return "-ERR expected 1 key\r\n"
	}
	refreshKey := rest[1]
	userSetPrefix := rest[2]
	hash := rest[3]

	s.isExpiredLocked(refreshKey)
	e, ok := s.strings[refreshKey]
	if !ok {
		return "$-1\r\n"
	}
	uid := e.val
	delete(s.strings, refreshKey)
	if set := s.sets[userSetPrefix+uid]; set != nil {
		delete(set, hash)
	}
	return respBulk(uid)
}

func (s *fakeRedisServer) containsRawValue(raw string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for k, v := range s.strings {
		if strings.Contains(k, raw) || strings.Contains(v.val, raw) {
			return true
		}
	}
	for k, set := range s.sets {
		if strings.Contains(k, raw) {
			return true
		}
		for m := range set {
			if strings.Contains(m, raw) {
				return true
			}
		}
	}
	return false
}

func TestRedisSessionStoreLifecycleAndRotation(t *testing.T) {
	srv, client := startFakeRedis(t)
	store := NewRedisSessionStore(client)
	ctx := context.Background()

	rawToken, err := GenerateRefreshToken()
	if err != nil {
		t.Fatalf("GenerateRefreshToken: %v", err)
	}

	if err := store.SaveRefreshToken(ctx, "user-1", rawToken, time.Minute); err != nil {
		t.Fatalf("SaveRefreshToken: %v", err)
	}
	// Raw refresh token must NEVER be stored in Redis.
	if srv.containsRawValue(rawToken) {
		t.Fatalf("raw refresh token leaked into Redis storage")
	}

	uid, err := store.ConsumeRefreshToken(ctx, rawToken)
	if err != nil {
		t.Fatalf("ConsumeRefreshToken first use: %v", err)
	}
	if uid != "user-1" {
		t.Fatalf("got uid %q, want 'user-1'", uid)
	}

	// Second consumption must fail (single-use rotation).
	if _, err := store.ConsumeRefreshToken(ctx, rawToken); !errors.Is(err, ErrRefreshTokenInvalid) {
		t.Fatalf("expected ErrRefreshTokenInvalid on replay, got %v", err)
	}
}

func TestRedisSessionStoreConcurrentConsumeSingleWinner(t *testing.T) {
	_, client := startFakeRedis(t)
	store := NewRedisSessionStore(client)
	ctx := context.Background()

	rawToken, err := GenerateRefreshToken()
	if err != nil {
		t.Fatalf("GenerateRefreshToken: %v", err)
	}
	if err := store.SaveRefreshToken(ctx, "user-race", rawToken, time.Minute); err != nil {
		t.Fatalf("SaveRefreshToken: %v", err)
	}

	const workers = 10
	var winners atomic.Int32
	var invalidCount atomic.Int32
	var wg sync.WaitGroup
	wg.Add(workers)
	for i := 0; i < workers; i++ {
		go func() {
			defer wg.Done()
			uid, err := store.ConsumeRefreshToken(ctx, rawToken)
			if err == nil && uid == "user-race" {
				winners.Add(1)
			} else if errors.Is(err, ErrRefreshTokenInvalid) {
				invalidCount.Add(1)
			} else {
				t.Errorf("unexpected error: %v", err)
			}
		}()
	}
	wg.Wait()

	if winners.Load() != 1 || invalidCount.Load() != workers-1 {
		t.Fatalf("expected 1 winner and %d invalid, got %d winners and %d invalid",
			workers-1, winners.Load(), invalidCount.Load())
	}
}

func TestRedisSessionStoreRevocationAndContextIntegration(t *testing.T) {
	setTestSigningKey(t)
	_, client := startFakeRedis(t)
	store := NewRedisSessionStore(client)
	ctx := context.Background()

	// Save 2 refresh tokens for user-99.
	rt1, _ := GenerateRefreshToken()
	rt2, _ := GenerateRefreshToken()
	if err := store.SaveRefreshToken(ctx, "user-99", rt1, time.Minute); err != nil {
		t.Fatalf("save rt1: %v", err)
	}
	if err := store.SaveRefreshToken(ctx, "user-99", rt2, time.Minute); err != nil {
		t.Fatalf("save rt2: %v", err)
	}

	// Issue v1 access token; initially not revoked.
	tokV1, jtiV1, err := GenerateAccessToken("user-99", "Neo", "neo@matrix.io", "", "user", 1, time.Minute)
	if err != nil {
		t.Fatalf("GenerateAccessToken v1: %v", err)
	}
	ctxV1 := gmetadata.NewIncomingContext(ctx, gmetadata.Pairs("authorization", "Bearer "+tokV1))
	if _, err := ParseClaimsFromContext(ctxV1, store); err != nil {
		t.Fatalf("expected v1 token valid before revocation, got %v", err)
	}

	// Blacklist single JTI.
	if err := store.RevokeAccessToken(ctx, jtiV1, time.Minute); err != nil {
		t.Fatalf("RevokeAccessToken: %v", err)
	}
	if _, err := ParseClaimsFromContext(ctxV1, store); !errors.Is(err, ErrTokenRevoked) {
		t.Fatalf("expected ErrTokenRevoked after JTI blacklist, got %v", err)
	}

	// RevokeAccessToken with ttl <= 0 is a safe no-op.
	if err := store.RevokeAccessToken(ctx, "already-expired-jti", 0); err != nil {
		t.Fatalf("RevokeAccessToken ttl=0: %v", err)
	}

	// Issue another v1 token (different jti), then revoke all sessions bumping version to 2.
	tokV1b, _, err := GenerateAccessToken("user-99", "Neo", "neo@matrix.io", "", "user", 1, time.Minute)
	if err != nil {
		t.Fatalf("GenerateAccessToken v1b: %v", err)
	}
	if err := store.RevokeAllUserSessions(ctx, "user-99", 2); err != nil {
		t.Fatalf("RevokeAllUserSessions: %v", err)
	}

	// Both refresh tokens must now be invalid.
	if _, err := store.ConsumeRefreshToken(ctx, rt1); !errors.Is(err, ErrRefreshTokenInvalid) {
		t.Fatalf("expected rt1 revoked, got %v", err)
	}
	if _, err := store.ConsumeRefreshToken(ctx, rt2); !errors.Is(err, ErrRefreshTokenInvalid) {
		t.Fatalf("expected rt2 revoked, got %v", err)
	}

	// v1b access token is now revoked because user_ver (2) > tokenVersion (1).
	ctxV1b := gmetadata.NewIncomingContext(ctx, gmetadata.Pairs("authorization", "Bearer "+tokV1b))
	if _, err := ParseClaimsFromContext(ctxV1b, store); !errors.Is(err, ErrTokenRevoked) {
		t.Fatalf("expected v1b token revoked after version bump, got %v", err)
	}

	// New v2 access token is accepted.
	tokV2, _, err := GenerateAccessToken("user-99", "Neo", "neo@matrix.io", "", "user", 2, time.Minute)
	if err != nil {
		t.Fatalf("GenerateAccessToken v2: %v", err)
	}
	ctxV2 := gmetadata.NewIncomingContext(ctx, gmetadata.Pairs("authorization", "Bearer "+tokV2))
	claims, err := ParseClaimsFromContext(ctxV2, store)
	if err != nil || claims.TokenVersion != 2 {
		t.Fatalf("expected v2 token accepted, got claims=%+v err=%v", claims, err)
	}
}
