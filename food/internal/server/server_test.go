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
	pb "github.com/knoci/roaming-world/food/api/food/v1"
	"github.com/knoci/roaming-world/food/internal/biz"
	"github.com/knoci/roaming-world/food/internal/conf"
	"github.com/knoci/roaming-world/food/internal/service"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
)

type noopFoodRepo struct{}

func (noopFoodRepo) CreateFood(_ context.Context, f *biz.Food) (*biz.Food, error) { return f, nil }
func (noopFoodRepo) GetFoodList(_ context.Context, _, _ int) ([]*biz.Food, int64, error) {
	return nil, 0, nil
}
func (noopFoodRepo) GetRandomFood(_ context.Context) (*biz.Food, error) { return nil, nil }

func TestFoodHTTPAndGRPCValidateMiddleware(t *testing.T) {
	logger := log.NewStdLogger(io.Discard)
	svc := service.NewFoodService(biz.NewFoodUsecase(noopFoodRepo{}, logger), logger)

	httpSrv := NewHTTPServer(&conf.Server{Http: &conf.Server_HTTP{}}, svc, logger)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/food", bytes.NewBufferString(`{"name":""}`))
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

	client := pb.NewFoodClient(conn)
	if _, err := client.CreateFood(context.Background(), &pb.CreateFoodRequest{}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("expected gRPC InvalidArgument from validate middleware, got %v", err)
	}
	validReq := &pb.CreateFoodRequest{
		Name:     "Hotpot",
		View:     []string{"https://example.com/a.jpg"},
		Describe: "Spicy",
		Recipe:   "Boil",
		Article:  "Classic",
		Location: "Chengdu",
	}
	if _, err := client.CreateFood(context.Background(), validReq); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("expected gRPC Unauthenticated (401), got %v", err)
	}
}
