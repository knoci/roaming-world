package server

import (
	"bytes"
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-kratos/kratos/v2/log"
	pb "github.com/knoci/roaming-world/audiobook/api/audiobook/v1"
	"github.com/knoci/roaming-world/audiobook/internal/biz"
	"github.com/knoci/roaming-world/audiobook/internal/conf"
	"github.com/knoci/roaming-world/audiobook/internal/service"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
)

type noopAudiobookRepo struct{}

func (noopAudiobookRepo) CreateAudiobook(_ context.Context, a *biz.Audiobook) (*biz.Audiobook, error) {
	return a, nil
}
func (noopAudiobookRepo) CreateAudiobookDetail(_ context.Context, d *biz.AudiobookDetail) (*biz.AudiobookDetail, error) {
	return d, nil
}
func (noopAudiobookRepo) GetAudiobooks(_ context.Context) ([]*biz.Audiobook, error) {
	return nil, nil
}
func (noopAudiobookRepo) GetAudiobookDetails(_ context.Context, _ string) ([]*biz.AudiobookDetail, error) {
	return nil, nil
}

func TestAudiobookHTTPValidateMiddleware(t *testing.T) {
	logger := log.NewStdLogger(io.Discard)
	svc := service.NewAudiobookService(biz.NewAudiobookUsecase(noopAudiobookRepo{}, logger), logger)
	srv := NewHTTPServer(&conf.Server{Http: &conf.Server_HTTP{}}, svc, logger)

	// Missing required name and author fields should be rejected by validate.Validator() with 400.
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/audiobook", bytes.NewBufferString(`{"author":"","name":""}`))
	req.Header.Set("Content-Type", "application/json")
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 Bad Request from validate middleware, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestAudiobookGRPCValidateMiddleware(t *testing.T) {
	logger := log.NewStdLogger(io.Discard)
	svc := service.NewAudiobookService(biz.NewAudiobookUsecase(noopAudiobookRepo{}, logger), logger)
	srv := NewGRPCServer(&conf.Server{Grpc: &conf.Server_GRPC{}}, svc, logger)

	lis := bufconn.Listen(1024 * 1024)
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(func() { srv.GracefulStop() })

	conn, err := grpc.NewClient(
		"passthrough:///bufnet",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return lis.DialContext(ctx)
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatalf("dial bufconn: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	client := pb.NewAudiobookClient(conn)

	// Empty author & name should fail validate.Validator() with InvalidArgument before auth.
	_, err = client.CreateAudiobook(context.Background(), &pb.CreateAudiobookRequest{})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("expected gRPC InvalidArgument from validate middleware, got %v", err)
	}

	// Valid payload without auth should reach handler and return Unauthenticated (401).
	_, err = client.CreateAudiobook(context.Background(), &pb.CreateAudiobookRequest{
		Author: "Tester",
		Name:   "Valid Book",
	})
	if status.Code(err) != codes.Unauthenticated {
		t.Fatalf("expected gRPC Unauthenticated (401), got %v", err)
	}
}
