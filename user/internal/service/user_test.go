package service

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"io"
	"mime/multipart"
	nethttp "net/http"
	"net/http/httptest"
	"net/textproto"
	"sync"
	"testing"
	"time"

	"github.com/go-kratos/kratos/v2/log"
	"github.com/go-kratos/kratos/v2/transport"
	khttp "github.com/go-kratos/kratos/v2/transport/http"
	"github.com/golang-jwt/jwt/v5"
	"github.com/knoci/roaming-world/pkg/auth"
	pb "github.com/knoci/roaming-world/user/api/user/v1"
	"github.com/knoci/roaming-world/user/internal/biz"
	pkgjwt "github.com/knoci/roaming-world/user/internal/pkg"
	"golang.org/x/crypto/bcrypt"
	grpcmd "google.golang.org/grpc/metadata"
)

type stubUserRepo struct {
	biz.UserRepo
	mu               sync.Mutex
	user             *biz.User
	usersByUID       map[string]*biz.User
	usersByEmail     map[string]*biz.User
	usersByName      map[string]*biz.User
	uploadInvoked    bool
	findByEmailCalls int
	findByUIDCalls   int
}

func newStubUserRepo() *stubUserRepo {
	return &stubUserRepo{
		usersByUID:   make(map[string]*biz.User),
		usersByEmail: make(map[string]*biz.User),
		usersByName:  make(map[string]*biz.User),
	}
}

func (r *stubUserRepo) seed(u *biz.User) {
	r.mu.Lock()
	defer r.mu.Unlock()
	cp := *u
	r.user = &cp
	r.usersByUID[cp.UID] = &cp
	r.usersByEmail[cp.Email] = &cp
	r.usersByName[cp.Name] = &cp
}

func (r *stubUserRepo) Create(_ context.Context, user *biz.User) (*biz.User, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	cp := *user
	if cp.UID == "" {
		cp.UID = "uid-created"
	}
	if cp.TokenVersion <= 0 {
		cp.TokenVersion = 1
	}
	r.usersByUID[cp.UID] = &cp
	r.usersByEmail[cp.Email] = &cp
	r.usersByName[cp.Name] = &cp
	return &cp, nil
}

func (r *stubUserRepo) FindByUID(_ context.Context, uid string) (*biz.User, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.findByUIDCalls++
	if u, ok := r.usersByUID[uid]; ok {
		cp := *u
		return &cp, nil
	}
	return nil, biz.ErrUserNotFound
}

func (r *stubUserRepo) FindByEmail(_ context.Context, email string) (*biz.User, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.findByEmailCalls++
	if u, ok := r.usersByEmail[email]; ok {
		cp := *u
		return &cp, nil
	}
	return nil, biz.ErrUserNotFound
}

func (r *stubUserRepo) FindByName(_ context.Context, name string) (*biz.User, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if u, ok := r.usersByName[name]; ok {
		cp := *u
		return &cp, nil
	}
	return nil, biz.ErrUserNotFound
}

func (r *stubUserRepo) FindByKeyword(_ context.Context, _ string) (*biz.User, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.user == nil {
		return nil, biz.ErrUserNotFound
	}
	cp := *r.user
	return &cp, nil
}

func (r *stubUserRepo) UpdatePasswordAndBumpVersion(_ context.Context, uid, passwordHash string) (int64, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	u, ok := r.usersByUID[uid]
	if !ok {
		return 0, biz.ErrUserNotFound
	}
	if u.TokenVersion <= 0 {
		u.TokenVersion = 1
	}
	u.Password = passwordHash
	u.TokenVersion++
	cp := *u
	r.usersByUID[cp.UID] = &cp
	r.usersByEmail[cp.Email] = &cp
	r.usersByName[cp.Name] = &cp
	return cp.TokenVersion, nil
}

func (r *stubUserRepo) Delete(_ context.Context, uid string) (int64, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	u, ok := r.usersByUID[uid]
	if !ok {
		return 0, nil
	}
	ver := u.TokenVersion
	if ver <= 0 {
		ver = 1
	}
	delete(r.usersByUID, uid)
	delete(r.usersByEmail, u.Email)
	delete(r.usersByName, u.Name)
	return ver + 1, nil
}

func (r *stubUserRepo) CheckVerificationCode(_ context.Context, _ biz.VerificationTarget, _ string, _ bool) error {
	return nil
}

func (r *stubUserRepo) UploadAvatar(_ context.Context, uid string, _ *multipart.FileHeader) (*biz.User, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.uploadInvoked = true
	return &biz.User{UID: uid, Name: "alice", Avatar: "https://cdn.example.com/avatars/" + uid + "/ok.png"}, nil
}

type memorySessionStore struct {
	mu          sync.Mutex
	refresh     map[string]string
	userVersion map[string]int64
	blacklist   map[string]bool
}

func newMemorySessionStore() *memorySessionStore {
	return &memorySessionStore{
		refresh:     make(map[string]string),
		userVersion: make(map[string]int64),
		blacklist:   make(map[string]bool),
	}
}

func (s *memorySessionStore) SaveRefreshToken(_ context.Context, uid, rawToken string, _ time.Duration) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.refresh[auth.HashRefreshToken(rawToken)] = uid
	return nil
}

func (s *memorySessionStore) ConsumeRefreshToken(_ context.Context, rawToken string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	h := auth.HashRefreshToken(rawToken)
	uid, ok := s.refresh[h]
	if !ok {
		return "", auth.ErrRefreshTokenInvalid
	}
	delete(s.refresh, h)
	return uid, nil
}

func (s *memorySessionStore) RevokeAllUserSessions(_ context.Context, uid string, newVersion int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.userVersion[uid] = newVersion
	for h, owner := range s.refresh {
		if owner == uid {
			delete(s.refresh, h)
		}
	}
	return nil
}

func (s *memorySessionStore) RevokeAccessToken(_ context.Context, jti string, _ time.Duration) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.blacklist[jti] = true
	return nil
}

func (s *memorySessionStore) IsRevoked(_ context.Context, uid, jti string, tokenVersion int64) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if jti != "" && s.blacklist[jti] {
		return true, nil
	}
	if minVer, ok := s.userVersion[uid]; ok && minVer > tokenVersion {
		return true, nil
	}
	return false, nil
}

type testHeader map[string]string

func (h testHeader) Get(key string) string      { return h[key] }
func (h testHeader) Set(key, value string)      { h[key] = value }
func (h testHeader) Add(key, value string)      { h[key] = value }
func (h testHeader) Keys() []string             { return nil }
func (h testHeader) Values(key string) []string { return []string{h[key]} }

type testTransport struct {
	kind   transport.Kind
	header testHeader
}

func (t testTransport) Kind() transport.Kind            { return t.kind }
func (t testTransport) Endpoint() string                { return "" }
func (t testTransport) Operation() string               { return "" }
func (t testTransport) RequestHeader() transport.Header { return t.header }
func (t testTransport) ReplyHeader() transport.Header   { return testHeader{} }

func setupSigningKey(t *testing.T, fill byte) []byte {
	t.Helper()
	key := bytes.Repeat([]byte{fill}, 32)
	t.Setenv("JWT_SIGNING_KEY", base64.StdEncoding.EncodeToString(key))
	pkgjwt.ResetKeyCacheForTest()
	t.Cleanup(pkgjwt.ResetKeyCacheForTest)
	return key
}

func makeRawJWT(t *testing.T, signingKey []byte, uid string, version int64) string {
	t.Helper()
	claims := pkgjwt.Claims{
		UID:          uid,
		Name:         "tester",
		Role:         "user",
		TokenVersion: version,
		RegisteredClaims: jwt.RegisteredClaims{
			ID:        "jti-" + uid,
			Issuer:    auth.DefaultIssuer,
			Audience:  jwt.ClaimStrings{auth.DefaultAudience},
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			NotBefore: jwt.NewNumericDate(time.Now()),
		},
	}
	raw, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(signingKey)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestServiceFindUserRedactsEmail(t *testing.T) {
	repo := &stubUserRepo{
		user: &biz.User{
			UID:    "uid-42",
			Name:   "bob",
			Email:  "bob.private@example.com",
			Avatar: "https://cdn.example.com/bob.png",
		},
	}
	uc := biz.NewUserUsecase(repo, nil, log.NewStdLogger(io.Discard))
	svc := NewUserService(uc, nil, nil, log.NewStdLogger(io.Discard))

	reply, err := svc.FindUser(context.Background(), &pb.FindUserRequest{Keyword: "bob"})
	if err != nil {
		t.Fatalf("FindUser failed: %v", err)
	}
	if reply.Email != "" {
		t.Fatalf("expected FindUser to redact email, got %q", reply.Email)
	}
	if reply.Uid != "uid-42" || reply.Name != "bob" || reply.Avatar != "https://cdn.example.com/bob.png" {
		t.Fatalf("unexpected FindUser reply: %+v", reply)
	}
}

func TestGetJwtClaimGRPCMetadataFallbackAndEmptyUIDRejection(t *testing.T) {
	signingKey := setupSigningKey(t, 7)

	validToken := makeRawJWT(t, signingKey, "uid-grpc-1", 1)
	emptyUIDToken := makeRawJWT(t, signingKey, "", 1)

	// Incoming gRPC metadata without Kratos transport
	grpcCtx := grpcmd.NewIncomingContext(context.Background(), grpcmd.Pairs(
		"authorization", "Bearer "+validToken,
	))
	claims, err := GetJwtClaim(grpcCtx)
	if err != nil {
		t.Fatalf("expected gRPC metadata token to succeed, got %v", err)
	}
	if claims.UID != "uid-grpc-1" {
		t.Fatalf("unexpected UID: %q", claims.UID)
	}

	// Incoming gRPC metadata with Kratos gRPC transport (empty transport headers -> fallback)
	grpcTransportCtx := transport.NewServerContext(grpcCtx, testTransport{
		kind:   transport.KindGRPC,
		header: testHeader{},
	})
	claims, err = GetJwtClaim(grpcTransportCtx)
	if err != nil || claims.UID != "uid-grpc-1" {
		t.Fatalf("expected gRPC transport fallback to succeed, got %v, %v", claims, err)
	}

	// Empty UID token must be rejected
	emptyCtx := grpcmd.NewIncomingContext(context.Background(), grpcmd.Pairs(
		"authorization", "Bearer "+emptyUIDToken,
	))
	if _, err := GetJwtClaim(emptyCtx); !errors.Is(err, biz.ErrUnauthorized) {
		t.Fatalf("expected ErrUnauthorized for empty UID token, got %v", err)
	}

	// HTTP transport with empty UID must also be rejected
	httpEmptyCtx := transport.NewServerContext(context.Background(), testTransport{
		kind: transport.KindHTTP,
		header: testHeader{
			"Authorization": "Bearer " + emptyUIDToken,
		},
	})
	if _, err := GetJwtClaim(httpEmptyCtx); !errors.Is(err, biz.ErrUnauthorized) {
		t.Fatalf("expected ErrUnauthorized for HTTP empty UID token, got %v", err)
	}
}

func TestLoginSingleQueryRefreshTokenLogoutAndVersionRevocation(t *testing.T) {
	setupSigningKey(t, 11)

	repo := newStubUserRepo()
	sessions := newMemorySessionStore()
	hash, err := bcrypt.GenerateFromPassword([]byte("strong-pass-123"), bcrypt.DefaultCost)
	if err != nil {
		t.Fatal(err)
	}
	repo.seed(&biz.User{
		UID:          "u-auth-1",
		Name:         "alice",
		Email:        "alice@example.com",
		Password:     string(hash),
		Avatar:       "https://cdn.example.com/a.png",
		Role:         "admin",
		Status:       "active",
		TokenVersion: 2,
	})

	uc := biz.NewUserUsecase(repo, sessions, log.NewStdLogger(io.Discard))
	svc := NewUserService(uc, nil, sessions, log.NewStdLogger(io.Discard))

	// 1. Login uses single FindByEmail query (no extra FindByUID for role) and returns access + refresh tokens
	loginRep, err := svc.Login(context.Background(), &pb.LoginRequest{
		Email:    "alice@example.com",
		Password: "strong-pass-123",
	})
	if err != nil {
		t.Fatalf("Login failed: %v", err)
	}
	if repo.findByEmailCalls != 1 || repo.findByUIDCalls != 0 {
		t.Fatalf("expected single FindByEmail query on Login, got emailCalls=%d uidCalls=%d", repo.findByEmailCalls, repo.findByUIDCalls)
	}
	if loginRep.Token == "" || loginRep.RefreshToken == "" {
		t.Fatalf("expected both access and refresh tokens, got %+v", loginRep)
	}
	claims, err := pkgjwt.ParseToken(loginRep.Token)
	if err != nil {
		t.Fatalf("ParseToken failed: %v", err)
	}
	if claims.Role != "admin" || claims.TokenVersion != 2 {
		t.Fatalf("expected Role=admin TokenVersion=2 in JWT, got %+v", claims)
	}

	// 2. RefreshToken rotates tokens and rejects reuse of old refresh token
	refRep, err := svc.RefreshToken(context.Background(), &pb.RefreshTokenRequest{
		RefreshToken: loginRep.RefreshToken,
	})
	if err != nil {
		t.Fatalf("RefreshToken failed: %v", err)
	}
	if refRep.Token == "" || refRep.RefreshToken == "" || refRep.RefreshToken == loginRep.RefreshToken {
		t.Fatalf("expected rotated refresh token, got %+v", refRep)
	}
	if _, err := svc.RefreshToken(context.Background(), &pb.RefreshTokenRequest{
		RefreshToken: loginRep.RefreshToken,
	}); !errors.Is(err, biz.ErrUnauthorized) {
		t.Fatalf("expected reused refresh token to fail with ErrUnauthorized, got %v", err)
	}

	// 3. Logout blacklists current access token JTI and consumes refresh token
	authCtx := transport.NewServerContext(context.Background(), testTransport{
		kind:   transport.KindHTTP,
		header: testHeader{"Authorization": "Bearer " + refRep.Token},
	})
	if _, err := svc.Logout(authCtx, &pb.LogoutRequest{RefreshToken: refRep.RefreshToken}); err != nil {
		t.Fatalf("Logout failed: %v", err)
	}
	if _, err := svc.getJwtClaim(authCtx); !errors.Is(err, biz.ErrUnauthorized) {
		t.Fatalf("expected logged-out access token to be rejected, got %v", err)
	}
	if _, err := svc.RefreshToken(context.Background(), &pb.RefreshTokenRequest{
		RefreshToken: refRep.RefreshToken,
	}); !errors.Is(err, biz.ErrUnauthorized) {
		t.Fatalf("expected logged-out refresh token to be rejected, got %v", err)
	}

	// 4. ResetPassword bumps token_version and invalidates older access tokens (even if JTI not blacklisted)
	oldTokenCtx := transport.NewServerContext(context.Background(), testTransport{
		kind:   transport.KindHTTP,
		header: testHeader{"Authorization": "Bearer " + loginRep.Token},
	})
	if _, err := svc.getJwtClaim(oldTokenCtx); err != nil {
		t.Fatalf("expected unrevoked v2 token to be valid before ResetPassword, got %v", err)
	}
	if _, err := svc.ResetPassword(context.Background(), &pb.ResetPasswordRequest{
		Name:        "alice",
		Email:       "alice@example.com",
		Code:        "123456",
		NewPassword: "brand-new-password",
	}); err != nil {
		t.Fatalf("ResetPassword failed: %v", err)
	}
	if _, err := svc.getJwtClaim(oldTokenCtx); !errors.Is(err, biz.ErrUnauthorized) {
		t.Fatalf("expected v2 access token to be revoked after ResetPassword bumped version to 3, got %v", err)
	}
}

func buildMultipartAvatarRequest(t *testing.T, filename, headerContentType string, payload []byte) *nethttp.Request {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	h := make(textproto.MIMEHeader)
	h.Set("Content-Disposition", `form-data; name="avatar"; filename="`+filename+`"`)
	h.Set("Content-Type", headerContentType)
	part, err := writer.CreatePart(h)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write(payload); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(nethttp.MethodPost, "/v1/user/avatar", &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	return req
}

func TestUploadAvatarRejectsDisguisedHTMLAndAcceptsValidPNG(t *testing.T) {
	signingKey := setupSigningKey(t, 9)
	tok := makeRawJWT(t, signingKey, "uid-avatar-1", 1)

	repo := &stubUserRepo{}
	uc := biz.NewUserUsecase(repo, nil, log.NewStdLogger(io.Discard))
	svc := NewUserService(uc, nil, nil, log.NewStdLogger(io.Discard))

	var lastRep *pb.UploadAvatarReply
	var lastErr error
	srv := khttp.NewServer()
	srv.Route("/").POST("/v1/user/avatar", func(ctx khttp.Context) error {
		lastRep, lastErr = svc.UploadAvatar(ctx, &pb.UploadAvatarRequest{})
		return lastErr
	})

	invoke := func(req *nethttp.Request) (*pb.UploadAvatarReply, error) {
		lastRep, lastErr = nil, nil
		req.Header.Set("Authorization", "Bearer "+tok)
		rec := httptest.NewRecorder()
		srv.ServeHTTP(rec, req)
		return lastRep, lastErr
	}

	// Disguised HTML claiming Content-Type: image/png and .png filename
	htmlReq := buildMultipartAvatarRequest(t, "evil.png", "image/png", []byte("<!DOCTYPE html><html><body><script>alert(1)</script></body></html>"))
	if _, err := invoke(htmlReq); !errors.Is(err, biz.ErrInvalidArgument) {
		t.Fatalf("expected ErrInvalidArgument for disguised HTML avatar, got %v", err)
	}
	if repo.uploadInvoked {
		t.Fatal("disguised HTML avatar should never reach repository")
	}

	// Disguised plain text claiming Content-Type: image/png
	textReq := buildMultipartAvatarRequest(t, "fake.png", "image/png", []byte("plain text pretending to be png"))
	if _, err := invoke(textReq); !errors.Is(err, biz.ErrInvalidArgument) {
		t.Fatalf("expected ErrInvalidArgument for disguised text avatar, got %v", err)
	}
	if repo.uploadInvoked {
		t.Fatal("disguised text avatar should never reach repository")
	}

	// Valid PNG magic bytes should succeed even with untrusted filename/header
	pngBytes := []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n', 0x00, 0x00, 0x00, 0x0d, 'I', 'H', 'D', 'R'}
	pngReq := buildMultipartAvatarRequest(t, "../../weird.html", "application/octet-stream", pngBytes)
	rep, err := invoke(pngReq)
	if err != nil {
		t.Fatalf("expected valid PNG avatar to succeed, got %v", err)
	}
	if !repo.uploadInvoked || rep == nil || rep.Uid != "uid-avatar-1" {
		t.Fatalf("expected valid PNG avatar to reach repository, got invoked=%v rep=%+v", repo.uploadInvoked, rep)
	}
}

func TestValidateAvatarMagicBytesAllAllowedTypes(t *testing.T) {
	cases := []struct {
		name    string
		data    []byte
		wantExt string
		wantErr bool
	}{
		{name: "png", data: []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'}, wantExt: ".png"},
		{name: "jpeg", data: []byte{0xff, 0xd8, 0xff, 0xe0, 0x00, 0x10, 'J', 'F', 'I', 'F'}, wantExt: ".jpg"},
		{name: "gif", data: []byte("GIF89a\x01\x00\x01\x00"), wantExt: ".gif"},
		{name: "webp", data: []byte("RIFF\x24\x00\x00\x00WEBPVP8 "), wantExt: ".webp"},
		{name: "html", data: []byte("<html><body>xss</body></html>"), wantErr: true},
		{name: "svg", data: []byte("<svg xmlns=\"http://www.w3.org/2000/svg\"></svg>"), wantErr: true},
		{name: "empty", data: nil, wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, ext, err := validateAvatarMagicBytes(bytes.NewReader(tc.data))
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error for %s, got ext=%q", tc.name, ext)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error for %s: %v", tc.name, err)
			}
			if ext != tc.wantExt {
				t.Fatalf("expected ext %q, got %q", tc.wantExt, ext)
			}
		})
	}
}
