package auth

import (
	"context"
	"fmt"
	"strings"

	"github.com/go-kratos/kratos/v2/metadata"
	"github.com/go-kratos/kratos/v2/transport"
	khttp "github.com/go-kratos/kratos/v2/transport/http"
	gmetadata "google.golang.org/grpc/metadata"
)

// ExtractBearerToken extracts a Bearer token from Kratos HTTP request context,
// Kratos transport server context, Kratos server metadata, or incoming gRPC metadata.
//
// Return semantics:
//   - ("", false): no Authorization header/metadata was present in any source.
//   - ("", true):  an Authorization header/metadata was present, but not valid "Bearer <non-empty>".
//   - (tok, true): a valid Bearer token was extracted.
func ExtractBearerToken(ctx context.Context) (token string, present bool) {
	if ctx == nil {
		return "", false
	}

	seenHeader := false

	// 1. Kratos HTTP server request context.
	if req, ok := khttp.RequestFromServerContext(ctx); ok && req != nil && req.Header != nil {
		if vals := req.Header.Values("Authorization"); len(vals) > 0 {
			seenHeader = true
			for _, v := range vals {
				if strings.TrimSpace(v) != "" {
					return parseBearerHeader(v), true
				}
			}
		}
	}

	// 2. Kratos generic transport server context (HTTP or gRPC transporter).
	if tr, ok := transport.FromServerContext(ctx); ok && tr != nil {
		if hdr := tr.RequestHeader(); hdr != nil {
			if v := headerValueCI(hdr, "Authorization"); v != "" {
				return parseBearerHeader(v), true
			}
			if headerKeyExistsCI(hdr, "Authorization") {
				seenHeader = true
			}
		}
	}

	// 3. Kratos server metadata.
	if md, ok := metadata.FromServerContext(ctx); ok && len(md) > 0 {
		if vals := md.Values("authorization"); len(vals) > 0 {
			seenHeader = true
			for _, v := range vals {
				if strings.TrimSpace(v) != "" {
					return parseBearerHeader(v), true
				}
			}
		}
	}

	// 4. Incoming gRPC metadata.
	if gmd, ok := gmetadata.FromIncomingContext(ctx); ok && len(gmd) > 0 {
		if vals := gmd.Get("authorization"); len(vals) > 0 {
			seenHeader = true
			for _, v := range vals {
				if strings.TrimSpace(v) != "" {
					return parseBearerHeader(v), true
				}
			}
		}
	}

	if seenHeader {
		return "", true
	}
	return "", false
}

// ParseClaimsFromContext extracts the Bearer token from ctx, validates it via ParseAccessToken,
// and optionally checks revocation via checker when checker is non-nil.
// Returns sentinel errors ErrMissingToken, ErrInvalidToken, or ErrTokenRevoked.
func ParseClaimsFromContext(ctx context.Context, checker RevocationChecker) (*Claims, error) {
	token, present := ExtractBearerToken(ctx)
	if !present {
		return nil, ErrMissingToken
	}
	if token == "" {
		return nil, ErrInvalidToken
	}

	claims, err := ParseAccessToken(token)
	if err != nil {
		return nil, err
	}

	if checker != nil {
		revoked, err := checker.IsRevoked(ctx, claims.UID, claims.ID, claims.TokenVersion)
		if err != nil {
			return nil, fmt.Errorf("auth: check token revocation: %w", err)
		}
		if revoked {
			return nil, ErrTokenRevoked
		}
	}

	return claims, nil
}

func parseBearerHeader(authHeader string) string {
	trimmed := strings.TrimSpace(authHeader)
	parts := strings.SplitN(trimmed, " ", 2)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
		return ""
	}
	tok := strings.TrimSpace(parts[1])
	if tok == "" || strings.ContainsAny(tok, " \t\r\n") {
		return ""
	}
	return tok
}

func headerValueCI(hdr transport.Header, key string) string {
	if v := hdr.Get(key); v != "" {
		return v
	}
	if v := hdr.Get(strings.ToLower(key)); v != "" {
		return v
	}
	for _, k := range hdr.Keys() {
		if strings.EqualFold(k, key) {
			if v := hdr.Get(k); v != "" {
				return v
			}
		}
	}
	return ""
}

func headerKeyExistsCI(hdr transport.Header, key string) bool {
	for _, k := range hdr.Keys() {
		if strings.EqualFold(k, key) {
			return true
		}
	}
	return false
}
