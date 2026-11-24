package service

import (
	"context"
	"encoding/base64"
	"errors"
	"io"
	"testing"
	"time"

	kerrors "github.com/go-kratos/kratos/v2/errors"
	"github.com/go-kratos/kratos/v2/log"
	kmetadata "github.com/go-kratos/kratos/v2/metadata"
	jwt "github.com/golang-jwt/jwt/v5"
	pb "github.com/knoci/roaming-world/audiobook/api/audiobook/v1"
	"github.com/knoci/roaming-world/audiobook/internal/biz"
	"github.com/knoci/roaming-world/pkg/auth"
	gmetadata "google.golang.org/grpc/metadata"
)

type stubAudiobookRepo struct {
	createBookErr   error
	createDetailErr error
	getDetailsErr   error
}

func (r *stubAudiobookRepo) CreateAudiobook(_ context.Context, a *biz.Audiobook) (*biz.Audiobook, error) {
	if r.createBookErr != nil {
		return nil, r.createBookErr
	}
	return &biz.Audiobook{
		BID:         "bid-1",
		View:        a.View,
		Author:      a.Author,
		Name:        a.Name,
		Rating:      a.Rating,
		Description: a.Description,
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}, nil
}

func (r *stubAudiobookRepo) CreateAudiobookDetail(_ context.Context, d *biz.AudiobookDetail) (*biz.AudiobookDetail, error) {
	if r.createDetailErr != nil {
		return nil, r.createDetailErr
	}
	return &biz.AudiobookDetail{
		DID:       "did-1",
		BID:       d.BID,
		Chapter:   d.Chapter,
		Audio:     d.Audio,
		Name:      d.Name,
		Duration:  d.Duration,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}, nil
}

func (r *stubAudiobookRepo) GetAudiobooks(_ context.Context) ([]*biz.Audiobook, error) {
	return nil, nil
}

func (r *stubAudiobookRepo) GetAudiobookDetails(_ context.Context, _ string) ([]*biz.AudiobookDetail, error) {
	if r.getDetailsErr != nil {
		return nil, r.getDetailsErr
	}
	return nil, nil
}

func makeTestAudiobookToken(t *testing.T, uid, role string) string {
	t.Helper()
	rawKey := []byte("0123456789abcdef0123456789abcdef")
	t.Setenv("JWT_SIGNING_KEY", base64.StdEncoding.EncodeToString(rawKey))
	auth.ResetKeyCacheForTest()
	t.Cleanup(auth.ResetKeyCacheForTest)

	if uid == "" {
		now := time.Now()
		claims := &auth.Claims{
			UID:  "",
			Name: "tester",
			Role: role,
			RegisteredClaims: jwt.RegisteredClaims{
				Issuer:    auth.DefaultIssuer,
				Audience:  jwt.ClaimStrings{auth.DefaultAudience},
				IssuedAt:  jwt.NewNumericDate(now),
				ExpiresAt: jwt.NewNumericDate(now.Add(time.Hour)),
			},
		}
		signed, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(rawKey)
		if err != nil {
			t.Fatalf("sign empty-uid jwt: %v", err)
		}
		return signed
	}

	tok, _, err := auth.GenerateAccessToken(uid, "tester", "", "", role, 1, time.Hour)
	if err != nil {
		t.Fatalf("generate access token: %v", err)
	}
	return tok
}

func TestAudiobookErrorStatusCodes(t *testing.T) {
	if biz.ErrUnauthorized.Code != 401 {
		t.Fatalf("expected ErrUnauthorized code 401, got %d", biz.ErrUnauthorized.Code)
	}
	if biz.ErrForbidden.Code != 403 {
		t.Fatalf("expected ErrForbidden code 403, got %d", biz.ErrForbidden.Code)
	}
}

func TestAudiobookAuthGRPCMetadata(t *testing.T) {
	logger := log.NewStdLogger(io.Discard)
	repo := &stubAudiobookRepo{}
	svc := NewAudiobookService(biz.NewAudiobookUsecase(repo, logger), logger)

	const ua = "roaming-test-ua"
	bookReq := &pb.CreateAudiobookRequest{Name: "Test Book", Author: "Tester"}
	detailReq := &pb.CreateAudiobookDetailRequest{Bid: "bid-1", Chapter: 1, Audio: "a.mp3", Name: "Ch1", Duration: 60}

	t.Run("anonymous returns 401", func(t *testing.T) {
		_, err := svc.CreateAudiobook(context.Background(), bookReq)
		if se := kerrors.FromError(err); se == nil || se.Code != 401 {
			t.Fatalf("expected 401, got %v", err)
		}
		_, err = svc.CreateAudiobookDetail(context.Background(), detailReq)
		if se := kerrors.FromError(err); se == nil || se.Code != 401 {
			t.Fatalf("expected 401, got %v", err)
		}
	})

	t.Run("empty uid rejected with 401", func(t *testing.T) {
		tok := makeTestAudiobookToken(t, "", "admin")
		ctx := gmetadata.NewIncomingContext(context.Background(), gmetadata.Pairs(
			"authorization", "Bearer "+tok,
			"user-agent", ua,
		))
		_, err := svc.CreateAudiobook(ctx, bookReq)
		if se := kerrors.FromError(err); se == nil || se.Code != 401 {
			t.Fatalf("expected 401 for empty uid, got %v", err)
		}
	})

	t.Run("non-admin user returns 403", func(t *testing.T) {
		tok := makeTestAudiobookToken(t, "user-1", "user")
		ctx := gmetadata.NewIncomingContext(context.Background(), gmetadata.Pairs(
			"authorization", "Bearer "+tok,
			"user-agent", ua,
		))
		_, err := svc.CreateAudiobook(ctx, bookReq)
		if se := kerrors.FromError(err); se == nil || se.Code != 403 {
			t.Fatalf("expected 403, got %v", err)
		}
		_, err = svc.CreateAudiobookDetail(ctx, detailReq)
		if se := kerrors.FromError(err); se == nil || se.Code != 403 {
			t.Fatalf("expected 403, got %v", err)
		}
	})

	t.Run("admin via grpc incoming metadata succeeds", func(t *testing.T) {
		tok := makeTestAudiobookToken(t, "admin-1", "admin")
		ctx := gmetadata.NewIncomingContext(context.Background(), gmetadata.Pairs(
			"authorization", "Bearer "+tok,
			"user-agent", ua,
		))
		reply, err := svc.CreateAudiobook(ctx, bookReq)
		if err != nil || reply.Audiobook == nil || reply.Audiobook.Bid != "bid-1" {
			t.Fatalf("expected admin CreateAudiobook success, got reply=%v err=%v", reply, err)
		}
	})

	t.Run("admin via kratos server metadata succeeds", func(t *testing.T) {
		tok := makeTestAudiobookToken(t, "admin-1", "admin")
		ctx := kmetadata.NewServerContext(context.Background(), kmetadata.New(map[string][]string{
			"authorization": {"Bearer " + tok},
			"user-agent":    {ua},
		}))
		reply, err := svc.CreateAudiobookDetail(ctx, detailReq)
		if err != nil || reply.Detail == nil || reply.Detail.Did != "did-1" {
			t.Fatalf("expected admin CreateAudiobookDetail success, got reply=%v err=%v", reply, err)
		}
	})

	t.Run("missing audiobook preserves 404 not found", func(t *testing.T) {
		repo.createDetailErr = biz.ErrAudiobookNotFound
		defer func() { repo.createDetailErr = nil }()
		tok := makeTestAudiobookToken(t, "admin-1", "admin")
		ctx := gmetadata.NewIncomingContext(context.Background(), gmetadata.Pairs(
			"authorization", "Bearer "+tok,
			"user-agent", ua,
		))
		_, err := svc.CreateAudiobookDetail(ctx, detailReq)
		if se := kerrors.FromError(err); se == nil || se.Code != 404 {
			t.Fatalf("expected 404 ErrAudiobookNotFound, got %v", err)
		}
	})

	t.Run("GetAudiobookDetails distinguishes 404 vs 500", func(t *testing.T) {
		repo.getDetailsErr = biz.ErrAudiobookNotFound
		defer func() { repo.getDetailsErr = nil }()
		_, err := svc.GetAudiobookDetails(context.Background(), &pb.GetAudiobookDetailsRequest{Bid: "missing"})
		if se := kerrors.FromError(err); se == nil || se.Code != 404 {
			t.Fatalf("expected 404 for missing audiobook, got %v", err)
		}

		repo.getDetailsErr = errors.New("connection refused")
		_, err = svc.GetAudiobookDetails(context.Background(), &pb.GetAudiobookDetailsRequest{Bid: "db-fail"})
		if se := kerrors.FromError(err); se == nil || se.Code != 500 {
			t.Fatalf("expected 500 for database error, got %v", err)
		}
	})
}
