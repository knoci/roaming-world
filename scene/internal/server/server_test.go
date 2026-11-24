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
	pb "github.com/knoci/roaming-world/scene/api/scene/v1"
	"github.com/knoci/roaming-world/scene/internal/biz"
	"github.com/knoci/roaming-world/scene/internal/conf"
	"github.com/knoci/roaming-world/scene/internal/service"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
)

type noopSceneRepo struct{}

func (noopSceneRepo) CreateScene(_ context.Context, s *biz.Scene) (*biz.Scene, error) { return s, nil }
func (noopSceneRepo) DeleteScene(_ context.Context, _ string) error                   { return nil }
func (noopSceneRepo) UpdateScene(_ context.Context, s *biz.Scene) (*biz.Scene, error) { return s, nil }
func (noopSceneRepo) SearchScene(_ context.Context, _ string, _ int, _ int) ([]*biz.Scene, int, error) {
	return nil, 0, nil
}
func (noopSceneRepo) ListScenes(_ context.Context, _ int, _ int) ([]*biz.Scene, int, error) {
	return nil, 0, nil
}
func (noopSceneRepo) GetSceneByID(_ context.Context, sid string) (*biz.Scene, error) {
	return &biz.Scene{SID: sid}, nil
}

func TestSceneHTTPAndGRPCValidateMiddleware(t *testing.T) {
	logger := log.NewStdLogger(io.Discard)
	svc := service.NewSceneService(biz.NewSceneUsecase(noopSceneRepo{}, logger), logger)

	httpSrv := NewHTTPServer(&conf.Server{Http: &conf.Server_HTTP{}}, svc, logger)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/scene", bytes.NewBufferString(`{"name":"a"}`))
	req.Header.Set("Content-Type", "application/json")
	httpSrv.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 from HTTP validate middleware, got %d: %s", rec.Code, rec.Body.String())
	}

	grpcSrv := NewGRPCServer(&conf.Server{Grpc: &conf.Server_GRPC{}}, svc, logger)
	lis := bufconn.Listen(1024 * 1024)
	go func() { _ = grpcSrv.Serve(lis) }()
	t.Cleanup(func() { grpcSrv.GracefulStop() })

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

	client := pb.NewSceneClient(conn)
	if _, err := client.CreateScene(context.Background(), &pb.CreateSceneRequest{Name: "a"}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("expected gRPC InvalidArgument from validate middleware, got %v", err)
	}
	if _, err := client.CreateScene(context.Background(), &pb.CreateSceneRequest{Name: "Valid Scene"}); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("expected gRPC Unauthenticated (401), got %v", err)
	}
}
