package server

import (
	"fmt"
	"net/http"
	"sync/atomic"
	"time"

	khttp "github.com/go-kratos/kratos/v2/transport/http"
)

var (
	httpRequestsTotal  atomic.Uint64
	httpDurationMicros atomic.Uint64
)

// metricsFilter 包住所有 HTTP 路由，包括 /healthz 和 /metrics。
func metricsFilter(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		httpRequestsTotal.Add(1)
		httpDurationMicros.Add(uint64(time.Since(start).Microseconds()))
	})
}

func metricsHandler(ctx khttp.Context) error {
	ctx.Response().Header().Set("Content-Type", "text/plain; version=0.0.4")
	_, err := fmt.Fprintf(ctx.Response(), "# HELP roaming_http_requests_total Total HTTP requests.\n"+
		"# TYPE roaming_http_requests_total counter\n"+
		"roaming_http_requests_total %d\n"+
		"# HELP roaming_http_duration_microseconds_total Cumulative HTTP duration in microseconds.\n"+
		"# TYPE roaming_http_duration_microseconds_total counter\n"+
		"roaming_http_duration_microseconds_total %d\n",
		httpRequestsTotal.Load(), httpDurationMicros.Load())
	return err
}
