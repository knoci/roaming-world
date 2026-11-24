package service

import (
	"bytes"
	"encoding/json"
	stderrors "errors"
	"io"
	"net/http"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/go-kratos/kratos/v2/metadata"
	khttp "github.com/go-kratos/kratos/v2/transport/http"
	"github.com/google/uuid"
	"github.com/knoci/roaming-world/pkg/auth"
)

const (
	maxStreamBodyBytes = 64 << 10 // 64 KiB
	sseReadBufferBytes = 4096
)

var threadIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

type jsonErrorBody struct {
	Code    int    `json:"code"`
	Reason  string `json:"reason"`
	Message string `json:"message"`
}

func writeJSONError(w http.ResponseWriter, status int, reason, message string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(jsonErrorBody{
		Code:    status,
		Reason:  reason,
		Message: message,
	})
}

func authenticateHTTPRequest(r *http.Request) (*auth.Claims, error) {
	authCtx := r.Context()
	if _, ok := khttp.RequestFromServerContext(authCtx); !ok {
		if vals := r.Header.Values("Authorization"); len(vals) > 0 {
			authCtx = metadata.NewServerContext(authCtx, metadata.New(map[string][]string{
				"authorization": vals,
			}))
		}
	}
	return auth.ParseClaimsFromContext(authCtx, nil)
}

func requestIDFromHTTP(r *http.Request) string {
	if v := strings.TrimSpace(r.Header.Get("X-Request-ID")); v != "" {
		return v
	}
	return uuid.NewString()
}

func parseAndValidateStreamBody(w http.ResponseWriter, r *http.Request) ([]byte, int, string, string) {
	if r.Body == nil {
		return nil, http.StatusBadRequest, "INVALID_ARGUMENT", "request body is required"
	}
	limited := http.MaxBytesReader(w, r.Body, maxStreamBodyBytes)
	raw, err := io.ReadAll(limited)
	if err != nil {
		var maxErr *http.MaxBytesError
		if stderrors.As(err, &maxErr) {
			return nil, http.StatusRequestEntityTooLarge, "PAYLOAD_TOO_LARGE", "request body exceeds 64 KiB limit"
		}
		return nil, http.StatusBadRequest, "INVALID_ARGUMENT", "failed to read request body"
	}

	var payload map[string]any
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if err := dec.Decode(&payload); err != nil || payload == nil {
		return nil, http.StatusBadRequest, "INVALID_ARGUMENT", "invalid json body"
	}

	rawQuery, ok := payload["query"].(string)
	if !ok {
		return nil, http.StatusBadRequest, "INVALID_ARGUMENT", "query must be a string"
	}
	query := strings.TrimSpace(rawQuery)
	if query == "" {
		return nil, http.StatusBadRequest, "INVALID_ARGUMENT", "query must not be empty"
	}
	if utf8.RuneCountInString(rawQuery) > maxAIQueryRunes {
		return nil, http.StatusBadRequest, "INVALID_ARGUMENT", "query exceeds maximum length"
	}

	topK := int64(defaultTopK)
	if rawTopK, exists := payload["top_k"]; exists && rawTopK != nil {
		num, ok := rawTopK.(json.Number)
		if !ok {
			return nil, http.StatusBadRequest, "INVALID_ARGUMENT", "top_k must be an integer between 1 and 10"
		}
		parsed, err := num.Int64()
		if err != nil {
			return nil, http.StatusBadRequest, "INVALID_ARGUMENT", "top_k must be an integer between 1 and 10"
		}
		if parsed == 0 {
			topK = defaultTopK
		} else if parsed < minTopK || parsed > maxTopK {
			return nil, http.StatusBadRequest, "INVALID_ARGUMENT", "top_k must be between 1 and 10"
		} else {
			topK = parsed
		}
	}

	payload["query"] = query
	payload["top_k"] = topK

	encoded, err := json.Marshal(payload)
	if err != nil {
		return nil, http.StatusInternalServerError, "AI_PROXY_FAILED", "failed to prepare ai request"
	}
	return encoded, 0, "", ""
}

// HandleStream proxies POST /v1/ai/stream directly as Server-Sent Events without Kratos JSON wrapping.
func (s *GatewayService) HandleStream(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSONError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method not allowed")
		return
	}

	claims, err := authenticateHTTPRequest(r)
	if err != nil {
		writeJSONError(w, http.StatusUnauthorized, "UNAUTHORIZED", "unauthorized")
		return
	}

	if retryAfter, reason, msg, ok := s.checkRateLimit(r.Context(), claims.UID); !ok {
		w.Header().Set("Retry-After", retryAfterSeconds(retryAfter))
		writeJSONError(w, http.StatusTooManyRequests, reason, msg)
		return
	}

	encoded, errStatus, errReason, errMsg := parseAndValidateStreamBody(w, r)
	if errStatus != 0 {
		writeJSONError(w, errStatus, errReason, errMsg)
		return
	}

	token := s.resolveToken()
	if token == "" {
		writeJSONError(w, http.StatusServiceUnavailable, "AI_UNAVAILABLE", "ai engine unavailable")
		return
	}

	if err := s.breaker.Allow(); err != nil {
		writeJSONError(w, http.StatusServiceUnavailable, "AI_CIRCUIT_OPEN", "ai engine temporarily unavailable")
		return
	}

	base := s.resolveBaseURL()
	httpReq, err := http.NewRequestWithContext(r.Context(), http.MethodPost, base+"/v1/ai/stream", bytes.NewReader(encoded))
	if err != nil {
		s.breaker.CancelProbe()
		s.log.WithContext(r.Context()).Errorf("gateway: failed to build ai stream request: %v", err)
		writeJSONError(w, http.StatusInternalServerError, "AI_PROXY_FAILED", "failed to prepare ai request")
		return
	}

	requestID := requestIDFromHTTP(r)
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "text/event-stream")
	httpReq.Header.Set("X-AI-Gateway-Token", token)
	httpReq.Header.Set("X-User-ID", claims.UID)
	httpReq.Header.Set("X-User-Role", claims.Role)
	httpReq.Header.Set("X-Request-ID", requestID)
	if traceparent := strings.TrimSpace(r.Header.Get("traceparent")); traceparent != "" {
		httpReq.Header.Set("traceparent", traceparent)
	}

	resp, err := s.httpClient.Do(httpReq)
	if err != nil {
		if r.Context().Err() != nil {
			s.breaker.CancelProbe()
			return
		}
		s.breaker.RecordFailure()
		s.log.WithContext(r.Context()).Errorf("gateway: ai stream request failed (request_id=%s): %v", requestID, err)
		writeJSONError(w, http.StatusBadGateway, "AI_UNAVAILABLE", "ai engine unavailable")
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		rawErr, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorLogBytes+1))
		if resp.StatusCode >= 500 {
			s.breaker.RecordFailure()
		} else {
			s.breaker.RecordSuccess()
		}
		s.log.WithContext(r.Context()).Errorf("gateway: ai stream upstream error (status=%d, request_id=%s, body=%q)", resp.StatusCode, requestID, truncateLogBody(rawErr))
		switch resp.StatusCode {
		case http.StatusBadRequest, http.StatusUnprocessableEntity:
			writeJSONError(w, http.StatusBadRequest, "AI_INVALID_REQUEST", "invalid ai request")
		case http.StatusTooManyRequests:
			writeJSONError(w, http.StatusTooManyRequests, "AI_RATE_LIMITED", "ai service rate limited")
		case http.StatusServiceUnavailable:
			writeJSONError(w, http.StatusServiceUnavailable, "AI_UNAVAILABLE", "ai engine unavailable")
		default:
			writeJSONError(w, http.StatusBadGateway, "AI_UNAVAILABLE", "ai engine unavailable")
		}
		return
	}

	s.breaker.RecordSuccess()
	s.recordTokensUsed(r.Context(), claims.UID, resp.Header.Get("X-AI-Tokens-Used"))

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	flusher, canFlush := w.(http.Flusher)
	if canFlush {
		flusher.Flush()
	}

	buf := make([]byte, sseReadBufferBytes)
	for {
		n, readErr := resp.Body.Read(buf)
		if n > 0 {
			if _, writeErr := w.Write(buf[:n]); writeErr != nil {
				return
			}
			if canFlush {
				flusher.Flush()
			}
		}
		if readErr != nil {
			break
		}
	}

	if trailerTokens := resp.Trailer.Get("X-AI-Tokens-Used"); trailerTokens != "" {
		s.recordTokensUsed(r.Context(), claims.UID, trailerTokens)
	}
}

func extractThreadIDFromPath(path string) string {
	const prefix = "/v1/ai/threads/"
	const suffix = "/resume"
	if !strings.HasPrefix(path, prefix) || !strings.HasSuffix(path, suffix) {
		return ""
	}
	mid := strings.TrimSuffix(strings.TrimPrefix(path, prefix), suffix)
	if strings.Contains(mid, "/") {
		return ""
	}
	return mid
}

// HandleResume proxies POST /v1/ai/threads/{thread_id}/resume cleanly without double-encoding.
func (s *GatewayService) HandleResume(w http.ResponseWriter, r *http.Request) {
	threadID := r.PathValue("thread_id")
	if threadID == "" {
		threadID = extractThreadIDFromPath(r.URL.Path)
	}
	s.HandleResumeWithThreadID(w, r, threadID)
}

// HandleResumeWithThreadID proxies POST /v1/ai/threads/{thread_id}/resume with an explicit thread_id.
func (s *GatewayService) HandleResumeWithThreadID(w http.ResponseWriter, r *http.Request, threadID string) {
	if r.Method != http.MethodPost {
		writeJSONError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method not allowed")
		return
	}

	claims, err := authenticateHTTPRequest(r)
	if err != nil {
		writeJSONError(w, http.StatusUnauthorized, "UNAUTHORIZED", "unauthorized")
		return
	}

	if retryAfter, reason, msg, ok := s.checkRateLimit(r.Context(), claims.UID); !ok {
		w.Header().Set("Retry-After", retryAfterSeconds(retryAfter))
		writeJSONError(w, http.StatusTooManyRequests, reason, msg)
		return
	}

	if !threadIDPattern.MatchString(threadID) {
		writeJSONError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "invalid thread_id")
		return
	}

	var rawBody []byte
	if r.Body != nil {
		limited := http.MaxBytesReader(w, r.Body, maxStreamBodyBytes)
		var readErr error
		rawBody, readErr = io.ReadAll(limited)
		if readErr != nil {
			var maxErr *http.MaxBytesError
			if stderrors.As(readErr, &maxErr) {
				writeJSONError(w, http.StatusRequestEntityTooLarge, "PAYLOAD_TOO_LARGE", "request body exceeds 64 KiB limit")
				return
			}
			writeJSONError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "failed to read request body")
			return
		}
	}
	if len(bytes.TrimSpace(rawBody)) == 0 {
		rawBody = []byte("{}")
	} else if !json.Valid(rawBody) {
		writeJSONError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "invalid json body")
		return
	}

	token := s.resolveToken()
	if token == "" {
		writeJSONError(w, http.StatusServiceUnavailable, "AI_UNAVAILABLE", "ai engine unavailable")
		return
	}

	if err := s.breaker.Allow(); err != nil {
		writeJSONError(w, http.StatusServiceUnavailable, "AI_CIRCUIT_OPEN", "ai engine temporarily unavailable")
		return
	}

	base := s.resolveBaseURL()
	upstreamURL := base + "/v1/ai/threads/" + threadID + "/resume"
	httpReq, err := http.NewRequestWithContext(r.Context(), http.MethodPost, upstreamURL, bytes.NewReader(rawBody))
	if err != nil {
		s.breaker.CancelProbe()
		s.log.WithContext(r.Context()).Errorf("gateway: failed to build ai resume request: %v", err)
		writeJSONError(w, http.StatusInternalServerError, "AI_PROXY_FAILED", "failed to prepare ai request")
		return
	}

	requestID := requestIDFromHTTP(r)
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("X-AI-Gateway-Token", token)
	httpReq.Header.Set("X-User-ID", claims.UID)
	httpReq.Header.Set("X-User-Role", claims.Role)
	httpReq.Header.Set("X-Request-ID", requestID)
	if traceparent := strings.TrimSpace(r.Header.Get("traceparent")); traceparent != "" {
		httpReq.Header.Set("traceparent", traceparent)
	}
	if idemKey := strings.TrimSpace(r.Header.Get("Idempotency-Key")); idemKey != "" {
		httpReq.Header.Set("Idempotency-Key", idemKey)
	}
	if authz := strings.TrimSpace(r.Header.Get("Authorization")); authz != "" {
		httpReq.Header.Set("Authorization", authz)
	}

	resp, err := s.httpClient.Do(httpReq)
	if err != nil {
		if r.Context().Err() != nil {
			s.breaker.CancelProbe()
			return
		}
		s.breaker.RecordFailure()
		s.log.WithContext(r.Context()).Errorf("gateway: ai resume request failed (request_id=%s): %v", requestID, err)
		writeJSONError(w, http.StatusBadGateway, "AI_UNAVAILABLE", "ai engine unavailable")
		return
	}
	defer resp.Body.Close()

	rawResp, err := io.ReadAll(io.LimitReader(resp.Body, maxAIResponseBytes+1))
	if err != nil {
		if r.Context().Err() != nil {
			s.breaker.CancelProbe()
			return
		}
		s.breaker.RecordFailure()
		s.log.WithContext(r.Context()).Errorf("gateway: failed to read ai resume response (status=%d, request_id=%s): %v", resp.StatusCode, requestID, err)
		writeJSONError(w, http.StatusBadGateway, "AI_UNAVAILABLE", "ai engine unavailable")
		return
	}
	if len(rawResp) > maxAIResponseBytes {
		s.breaker.RecordSuccess()
		s.log.WithContext(r.Context()).Errorf("gateway: ai resume response too large (status=%d, request_id=%s, limit=%d)", resp.StatusCode, requestID, maxAIResponseBytes)
		writeJSONError(w, http.StatusBadGateway, "AI_RESPONSE_TOO_LARGE", "ai engine response too large")
		return
	}

	if resp.StatusCode >= 500 {
		s.breaker.RecordFailure()
		s.log.WithContext(r.Context()).Errorf("gateway: ai resume upstream 5xx (status=%d, request_id=%s, body=%q)", resp.StatusCode, requestID, truncateLogBody(rawResp))
		if resp.StatusCode == http.StatusServiceUnavailable {
			writeJSONError(w, http.StatusServiceUnavailable, "AI_UNAVAILABLE", "ai engine unavailable")
			return
		}
		writeJSONError(w, http.StatusBadGateway, "AI_UNAVAILABLE", "ai engine unavailable")
		return
	}

	s.breaker.RecordSuccess()
	s.recordTokensUsed(r.Context(), claims.UID, resp.Header.Get("X-AI-Tokens-Used"))

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		s.log.WithContext(r.Context()).Errorf("gateway: ai resume upstream error (status=%d, request_id=%s, body=%q)", resp.StatusCode, requestID, truncateLogBody(rawResp))
		switch resp.StatusCode {
		case http.StatusBadRequest, http.StatusUnprocessableEntity:
			writeJSONError(w, http.StatusBadRequest, "AI_INVALID_REQUEST", "invalid ai request")
		case http.StatusNotFound:
			writeJSONError(w, http.StatusNotFound, "THREAD_NOT_FOUND", "thread not found")
		case http.StatusConflict:
			writeJSONError(w, http.StatusConflict, "THREAD_CONFLICT", "thread state conflict")
		case http.StatusTooManyRequests:
			writeJSONError(w, http.StatusTooManyRequests, "AI_RATE_LIMITED", "ai service rate limited")
		default:
			writeJSONError(w, http.StatusBadGateway, "AI_UNAVAILABLE", "ai engine unavailable")
		}
		return
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(resp.StatusCode)
	_, _ = w.Write(rawResp)
}
