package biz

import (
	"context"
	"io"
	"mime/multipart"
	"strings"
	"testing"
	"time"

	kerrors "github.com/go-kratos/kratos/v2/errors"
	"github.com/go-kratos/kratos/v2/log"
	v1 "github.com/knoci/roaming-world/user/api/user/v1"
	"golang.org/x/crypto/bcrypt"
)

type fakeSessionStore struct {
	revokedVersions map[string]int64
	revokedJTIs     map[string]bool
	refreshTokens   map[string]string
}

func newFakeSessionStore() *fakeSessionStore {
	return &fakeSessionStore{
		revokedVersions: make(map[string]int64),
		revokedJTIs:     make(map[string]bool),
		refreshTokens:   make(map[string]string),
	}
}

func (s *fakeSessionStore) SaveRefreshToken(_ context.Context, uid, rawToken string, _ time.Duration) error {
	s.refreshTokens[rawToken] = uid
	return nil
}

func (s *fakeSessionStore) ConsumeRefreshToken(_ context.Context, rawToken string) (string, error) {
	uid, ok := s.refreshTokens[rawToken]
	if !ok {
		return "", ErrUnauthorized
	}
	delete(s.refreshTokens, rawToken)
	return uid, nil
}

func (s *fakeSessionStore) RevokeAllUserSessions(_ context.Context, uid string, newVersion int64) error {
	s.revokedVersions[uid] = newVersion
	for tok, owner := range s.refreshTokens {
		if owner == uid {
			delete(s.refreshTokens, tok)
		}
	}
	return nil
}

func (s *fakeSessionStore) RevokeAccessToken(_ context.Context, jti string, _ time.Duration) error {
	s.revokedJTIs[jti] = true
	return nil
}

func (s *fakeSessionStore) IsRevoked(_ context.Context, uid, jti string, tokenVersion int64) (bool, error) {
	if s.revokedJTIs[jti] {
		return true, nil
	}
	if minVer, ok := s.revokedVersions[uid]; ok && minVer > tokenVersion {
		return true, nil
	}
	return false, nil
}

type fakeUserRepo struct {
	usersByUID   map[string]*User
	usersByEmail map[string]*User
	usersByName  map[string]*User

	checkCodeCalls   int
	consumeCodeCalls int
	lastCheckTarget  VerificationTarget
	checkCodeErr     error
	createdUser      *User
	updatedUser      *User
}

func newFakeUserRepo() *fakeUserRepo {
	return &fakeUserRepo{
		usersByUID:   make(map[string]*User),
		usersByEmail: make(map[string]*User),
		usersByName:  make(map[string]*User),
	}
}

func (r *fakeUserRepo) seed(u *User) {
	cp := *u
	cp.Email = normalizeEmail(cp.Email)
	r.usersByUID[cp.UID] = &cp
	r.usersByEmail[cp.Email] = &cp
	r.usersByName[cp.Name] = &cp
}

func (r *fakeUserRepo) Create(_ context.Context, user *User) (*User, error) {
	cp := *user
	if cp.UID == "" {
		cp.UID = "uid-created"
	}
	cp.Email = normalizeEmail(cp.Email)
	r.createdUser = &cp
	r.seed(&cp)
	return &cp, nil
}

func (r *fakeUserRepo) Update(_ context.Context, user *User) (*User, error) {
	cp := *user
	cp.Email = normalizeEmail(cp.Email)
	r.updatedUser = &cp
	r.seed(&cp)
	return &cp, nil
}

func (r *fakeUserRepo) FindByUID(_ context.Context, uid string) (*User, error) {
	if u, ok := r.usersByUID[uid]; ok {
		cp := *u
		return &cp, nil
	}
	return nil, ErrUserNotFound
}

func (r *fakeUserRepo) FindByEmail(_ context.Context, email string) (*User, error) {
	if u, ok := r.usersByEmail[normalizeEmail(email)]; ok {
		cp := *u
		return &cp, nil
	}
	return nil, ErrUserNotFound
}

func (r *fakeUserRepo) FindByName(_ context.Context, name string) (*User, error) {
	if u, ok := r.usersByName[name]; ok {
		cp := *u
		return &cp, nil
	}
	return nil, ErrUserNotFound
}

func (r *fakeUserRepo) FindByKeyword(_ context.Context, keyword string) (*User, error) {
	if u, ok := r.usersByUID[keyword]; ok {
		cp := *u
		return &cp, nil
	}
	if u, ok := r.usersByName[keyword]; ok {
		cp := *u
		return &cp, nil
	}
	if u, ok := r.usersByEmail[normalizeEmail(keyword)]; ok {
		cp := *u
		return &cp, nil
	}
	return nil, ErrUserNotFound
}

func (r *fakeUserRepo) UpdatePasswordAndBumpVersion(_ context.Context, uid, passwordHash string) (int64, error) {
	u, ok := r.usersByUID[uid]
	if !ok {
		return 0, ErrUserNotFound
	}
	if u.TokenVersion <= 0 {
		u.TokenVersion = 1
	}
	u.Password = passwordHash
	u.TokenVersion++
	r.seed(u)
	return u.TokenVersion, nil
}

func (r *fakeUserRepo) IncrementTokenVersion(_ context.Context, uid string) (int64, error) {
	u, ok := r.usersByUID[uid]
	if !ok {
		return 0, ErrUserNotFound
	}
	if u.TokenVersion <= 0 {
		u.TokenVersion = 1
	}
	u.TokenVersion++
	r.seed(u)
	return u.TokenVersion, nil
}

func (r *fakeUserRepo) Delete(_ context.Context, uid string) (int64, error) {
	u, ok := r.usersByUID[uid]
	if !ok {
		return 0, nil
	}
	ver := u.TokenVersion
	if ver <= 0 {
		ver = 1
	}
	delete(r.usersByUID, uid)
	delete(r.usersByEmail, normalizeEmail(u.Email))
	delete(r.usersByName, u.Name)
	return ver + 1, nil
}

func (r *fakeUserRepo) IssueVerificationCode(_ context.Context, _ VerificationTarget, _ string) (func(context.Context) error, error) {
	return func(context.Context) error { return nil }, nil
}

func (r *fakeUserRepo) CheckVerificationCode(_ context.Context, target VerificationTarget, _ string, consume bool) error {
	r.checkCodeCalls++
	r.lastCheckTarget = target
	if consume {
		r.consumeCodeCalls++
	}
	return r.checkCodeErr
}

func (r *fakeUserRepo) UploadAvatar(_ context.Context, uid string, _ *multipart.FileHeader) (*User, error) {
	u, ok := r.usersByUID[uid]
	if !ok {
		return nil, ErrUserNotFound
	}
	cp := *u
	return &cp, nil
}

func TestLoginMissingUserAndWrongPasswordReturnUnified401(t *testing.T) {
	repo := newFakeUserRepo()
	hash, err := bcrypt.GenerateFromPassword([]byte("correct-password-20ch"), bcrypt.DefaultCost)
	if err != nil {
		t.Fatal(err)
	}
	repo.seed(&User{
		UID:      "u-1",
		Name:     "alice",
		Email:    "alice@example.com",
		Password: string(hash),
		Status:   "active",
	})
	repo.seed(&User{
		UID:      "u-banned",
		Name:     "banned",
		Email:    "banned@example.com",
		Password: string(hash),
		Status:   "banned",
	})

	uc := NewUserUsecase(repo, nil, log.NewStdLogger(io.Discard))

	missingErr := func() error {
		_, err := uc.Login(context.Background(), &v1.LoginRequest{
			Email:    "nobody@example.com",
			Password: "any-password",
		})
		return err
	}()
	if missingErr == nil {
		t.Fatal("expected error for missing user")
	}
	if kerrors.Code(missingErr) != 401 {
		t.Fatalf("expected HTTP 401 for missing user, got %d (%v)", kerrors.Code(missingErr), missingErr)
	}

	wrongPwErr := func() error {
		_, err := uc.Login(context.Background(), &v1.LoginRequest{
			Email:    "alice@example.com",
			Password: "wrong-password",
		})
		return err
	}()
	if wrongPwErr == nil {
		t.Fatal("expected error for wrong password")
	}
	if kerrors.Code(wrongPwErr) != 401 {
		t.Fatalf("expected HTTP 401 for wrong password, got %d (%v)", kerrors.Code(wrongPwErr), wrongPwErr)
	}

	if kerrors.Reason(missingErr) != kerrors.Reason(wrongPwErr) || missingErr.Error() != wrongPwErr.Error() {
		t.Fatalf("missing user and wrong password errors must be indistinguishable: %v vs %v", missingErr, wrongPwErr)
	}

	// Banned account with wrong password must still return 401 (no status leak)
	_, bannedWrongPwErr := uc.Login(context.Background(), &v1.LoginRequest{
		Email:    "banned@example.com",
		Password: "wrong-password",
	})
	if kerrors.Code(bannedWrongPwErr) != 401 {
		t.Fatalf("expected 401 for banned user with wrong password, got %v", bannedWrongPwErr)
	}

	// Banned account with correct password returns 403 ErrUserBanned
	_, bannedRightPwErr := uc.Login(context.Background(), &v1.LoginRequest{
		Email:    "banned@example.com",
		Password: "correct-password-20ch",
	})
	if !kerrors.Is(bannedRightPwErr, ErrUserBanned) {
		t.Fatalf("expected ErrUserBanned for banned user with right password, got %v", bannedRightPwErr)
	}

	// Valid login succeeds (also with mixed-case email)
	reply, err := uc.Login(context.Background(), &v1.LoginRequest{
		Email:    "  ALICE@Example.COM ",
		Password: "correct-password-20ch",
	})
	if err != nil {
		t.Fatalf("expected valid login to succeed, got %v", err)
	}
	if reply.Uid != "u-1" || reply.Name != "alice" {
		t.Fatalf("unexpected login reply: %+v", reply)
	}
}

func TestFindUserRedactsEmailPII(t *testing.T) {
	repo := newFakeUserRepo()
	repo.seed(&User{
		UID:    "u-1",
		Name:   "alice",
		Email:  "secret@example.com",
		Avatar: "https://cdn.example.com/a.png",
	})
	uc := NewUserUsecase(repo, nil, log.NewStdLogger(io.Discard))

	reply, err := uc.FindUser(context.Background(), "alice")
	if err != nil {
		t.Fatalf("FindUser failed: %v", err)
	}
	if reply.Email != "" {
		t.Fatalf("FindUser leaked email PII: %q", reply.Email)
	}
	if reply.Uid != "u-1" || reply.Name != "alice" || reply.Avatar != "https://cdn.example.com/a.png" {
		t.Fatalf("unexpected FindUser reply: %+v", reply)
	}
}

func TestEmailCaseNormalizationAndDuplicateCheckOrder(t *testing.T) {
	repo := newFakeUserRepo()
	repo.seed(&User{
		UID:   "u-existing",
		Name:  "taken-name",
		Email: "taken@example.com",
	})
	uc := NewUserUsecase(repo, nil, log.NewStdLogger(io.Discard))

	// Duplicate username in Register must NOT consume verification code
	_, err := uc.Register(context.Background(), &v1.RegisterRequest{
		Name:     "taken-name",
		Email:    "NewUser@Example.COM",
		Password: "password123456",
		Code:     "123456",
	})
	if !kerrors.Is(err, ErrUsernameAlreadyExists) {
		t.Fatalf("expected ErrUsernameAlreadyExists, got %v", err)
	}
	if repo.consumeCodeCalls != 0 {
		t.Fatalf("Register consumed verification code on duplicate username: %d calls", repo.consumeCodeCalls)
	}

	// Duplicate email (case-insensitive) in Register must NOT consume verification code
	_, err = uc.Register(context.Background(), &v1.RegisterRequest{
		Name:     "fresh-name",
		Email:    "  TAKEN@EXAMPLE.COM  ",
		Password: "password123456",
		Code:     "123456",
	})
	if !kerrors.Is(err, ErrEmailAlreadyExists) {
		t.Fatalf("expected ErrEmailAlreadyExists, got %v", err)
	}
	if repo.consumeCodeCalls != 0 {
		t.Fatalf("Register consumed verification code on duplicate email: %d calls", repo.consumeCodeCalls)
	}

	// Valid Register normalizes email
	_, err = uc.Register(context.Background(), &v1.RegisterRequest{
		Name:     "fresh-name",
		Email:    "  Fresh.User@Example.COM ",
		Password: "password123456",
		Code:     "123456",
	})
	if err != nil {
		t.Fatalf("valid Register failed: %v", err)
	}
	if repo.createdUser == nil || repo.createdUser.Email != "fresh.user@example.com" {
		t.Fatalf("expected normalized created email, got %+v", repo.createdUser)
	}
	if repo.lastCheckTarget.Email != "fresh.user@example.com" {
		t.Fatalf("expected normalized verification target email, got %q", repo.lastCheckTarget.Email)
	}

	// Duplicate username in UpdateUserInfo must NOT consume verification code
	repo.consumeCodeCalls = 0
	_, err = uc.UpdateUserInfo(context.Background(), "uid-created", &v1.UpdateUserInfoRequest{
		NewName:  "taken-name",
		NewEmail: "Another@Example.COM",
		Code:     "654321",
	})
	if !kerrors.Is(err, ErrUsernameAlreadyExists) {
		t.Fatalf("expected ErrUsernameAlreadyExists on UpdateUserInfo, got %v", err)
	}
	if repo.consumeCodeCalls != 0 {
		t.Fatalf("UpdateUserInfo consumed verification code on duplicate name")
	}

	// CheckUserExist normalizes email
	if err := uc.CheckUserExist(context.Background(), "fresh-name", "  FRESH.USER@EXAMPLE.COM "); err != nil {
		t.Fatalf("CheckUserExist should normalize email, got %v", err)
	}

	// Password > 72 bytes rejected in Register and ResetPassword
	longPw := strings.Repeat("a", 73)
	if _, err := uc.Register(context.Background(), &v1.RegisterRequest{
		Name:     "pw-test",
		Email:    "pw@example.com",
		Password: longPw,
		Code:     "123456",
	}); !kerrors.Is(err, ErrInvalidArgument) {
		t.Fatalf("expected ErrInvalidArgument for >72 byte Register password, got %v", err)
	}
	if err := uc.ResetPassword(context.Background(), &v1.ResetPasswordRequest{
		Name:        "fresh-name",
		Email:       "FRESH.USER@EXAMPLE.COM",
		Code:        "123456",
		NewPassword: longPw,
	}); !kerrors.Is(err, ErrInvalidArgument) {
		t.Fatalf("expected ErrInvalidArgument for >72 byte ResetPassword password, got %v", err)
	}
}


func TestResetPasswordAndDeleteUserRevokeSessionsByVersion(t *testing.T) {
	repo := newFakeUserRepo()
	sessions := newFakeSessionStore()
	hash, err := bcrypt.GenerateFromPassword([]byte("initial-password"), bcrypt.DefaultCost)
	if err != nil {
		t.Fatal(err)
	}
	repo.seed(&User{
		UID:          "u-rev",
		Name:         "revuser",
		Email:        "rev@example.com",
		Password:     string(hash),
		Role:         "admin",
		Status:       "active",
		TokenVersion: 1,
	})
	_ = sessions.SaveRefreshToken(context.Background(), "u-rev", "refresh-1", time.Hour)

	uc := NewUserUsecase(repo, sessions, log.NewStdLogger(io.Discard))

	// Authenticate returns Role and TokenVersion in one call
	authed, err := uc.Authenticate(context.Background(), &v1.LoginRequest{
		Email:    "rev@example.com",
		Password: "initial-password",
	})
	if err != nil {
		t.Fatalf("Authenticate failed: %v", err)
	}
	if authed.Role != "admin" || authed.TokenVersion != 1 || authed.Password != "" {
		t.Fatalf("unexpected authed user: %+v", authed)
	}

	// ResetPassword bumps token_version to 2 and revokes all user sessions
	if err := uc.ResetPassword(context.Background(), &v1.ResetPasswordRequest{
		Name:        "revuser",
		Email:       "rev@example.com",
		Code:        "123456",
		NewPassword: "new-password-123",
	}); err != nil {
		t.Fatalf("ResetPassword failed: %v", err)
	}
	if got := sessions.revokedVersions["u-rev"]; got != 2 {
		t.Fatalf("expected revoked version 2 after ResetPassword, got %d", got)
	}
	if len(sessions.refreshTokens) != 0 {
		t.Fatalf("expected all refresh tokens cleared after ResetPassword, got %v", sessions.refreshTokens)
	}

	// Version 1 token is revoked, version 2 token is valid
	if revoked, _ := sessions.IsRevoked(context.Background(), "u-rev", "jti-1", 1); !revoked {
		t.Fatal("expected version 1 token to be revoked after ResetPassword")
	}
	if revoked, _ := sessions.IsRevoked(context.Background(), "u-rev", "jti-2", 2); revoked {
		t.Fatal("expected version 2 token to remain valid after ResetPassword")
	}

	// DeleteUser bumps version to 3 and revokes all sessions
	_ = sessions.SaveRefreshToken(context.Background(), "u-rev", "refresh-2", time.Hour)
	newVer, err := uc.DeleteUser(context.Background(), "u-rev")
	if err != nil {
		t.Fatalf("DeleteUser failed: %v", err)
	}
	if newVer != 3 || sessions.revokedVersions["u-rev"] != 3 {
		t.Fatalf("expected version 3 after DeleteUser, got newVer=%d stored=%d", newVer, sessions.revokedVersions["u-rev"])
	}
	if revoked, _ := sessions.IsRevoked(context.Background(), "u-rev", "jti-2", 2); !revoked {
		t.Fatal("expected version 2 token to be revoked after DeleteUser")
	}
}
