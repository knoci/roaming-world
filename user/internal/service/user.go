package service

import (
	"context"
	"errors"
	"io"
	nethttp "net/http"
	"strings"
	"time"

	"github.com/go-kratos/kratos/v2/log"
	"github.com/go-kratos/kratos/v2/transport"
	"github.com/go-kratos/kratos/v2/transport/http"
	"github.com/knoci/roaming-world/pkg/auth"
	pb "github.com/knoci/roaming-world/user/api/user/v1"
	"github.com/knoci/roaming-world/user/internal/biz"
	jwt "github.com/knoci/roaming-world/user/internal/pkg"
)

var allowedAvatarMIMEs = map[string]string{
	"image/jpeg": ".jpg",
	"image/png":  ".png",
	"image/gif":  ".gif",
	"image/webp": ".webp",
}

func validateAvatarMagicBytes(r io.Reader) (string, string, error) {
	buf := make([]byte, 512)
	n, err := io.ReadFull(r, buf)
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
		return "", "", biz.ErrInvalidArgument
	}
	if n == 0 {
		return "", "", biz.ErrInvalidArgument
	}
	detectedType := nethttp.DetectContentType(buf[:n])
	ext, ok := allowedAvatarMIMEs[detectedType]
	if !ok {
		return "", "", biz.ErrInvalidArgument
	}
	return detectedType, ext, nil
}

func NewUserService(user *biz.UserUsecase, social *biz.SocialUsecase, sessions biz.SessionStore, logger log.Logger) *UserService {
	return &UserService{
		user:     user,
		social:   social,
		sessions: sessions,
		revoker:  sessions,
		log:      log.NewHelper(logger),
	}
}

func (s *UserService) issueSessionTokens(ctx context.Context, user *biz.User) (accessToken, refreshToken string, err error) {
	accessToken, _, err = jwt.GenerateToken(user.UID, user.Name, user.Email, user.Avatar, user.Role, user.TokenVersion)
	if err != nil {
		s.log.WithContext(ctx).Errorf("failed to generate access token for user %s: %v", user.UID, err)
		return "", "", biz.ErrInternalError
	}
	if s.sessions != nil {
		refreshToken, err = jwt.GenerateRefreshToken()
		if err != nil {
			s.log.WithContext(ctx).Errorf("failed to generate refresh token for user %s: %v", user.UID, err)
			return "", "", biz.ErrInternalError
		}
		if err = s.sessions.SaveRefreshToken(ctx, user.UID, refreshToken, jwt.DefaultRefreshTTL); err != nil {
			s.log.WithContext(ctx).Errorf("failed to save refresh token for user %s: %v", user.UID, err)
			return "", "", biz.ErrInternalError
		}
	}
	return accessToken, refreshToken, nil
}

func (s *UserService) Register(ctx context.Context, req *pb.RegisterRequest) (*pb.RegisterReply, error) {
	s.log.WithContext(ctx).Infof("Register received: Name=%s, Email=%s", req.Name, req.Email)
	user, err := s.user.RegisterUser(ctx, req)
	if err != nil {
		s.log.WithContext(ctx).Errorf("Register failed: %v", err)
		return nil, err
	}

	token, refreshToken, err := s.issueSessionTokens(ctx, user)
	if err != nil {
		return nil, err
	}

	return &pb.RegisterReply{
		Uid:          user.UID,
		Name:         user.Name,
		Avatar:       user.Avatar,
		Token:        token,
		RefreshToken: refreshToken,
	}, nil
}

func (s *UserService) SendVerificationCode(ctx context.Context, req *pb.SendVerificationCodeRequest) (*pb.SendVerificationCodeReply, error) {
	if tr, ok := transport.FromServerContext(ctx); ok && tr.Kind() == transport.KindHTTP {
		tr.ReplyHeader().Set("Cache-Control", "no-store")
	}
	target, err := s.verificationTarget(ctx, req.Email, req.Purpose)
	if err != nil {
		return nil, err
	}
	if err := s.user.SendVerificationCode(ctx, target); err != nil {
		return nil, err
	}
	return &pb.SendVerificationCodeReply{}, nil
}

func (s *UserService) Login(ctx context.Context, req *pb.LoginRequest) (*pb.LoginReply, error) {
	s.log.WithContext(ctx).Infof("Login received: Email=%s", req.Email)
	user, err := s.user.Authenticate(ctx, req)
	if err != nil {
		s.log.WithContext(ctx).Errorf("Login failed: %v", err)
		return nil, err
	}

	token, refreshToken, err := s.issueSessionTokens(ctx, user)
	if err != nil {
		return nil, err
	}

	return &pb.LoginReply{
		Uid:          user.UID,
		Name:         user.Name,
		Avatar:       user.Avatar,
		Token:        token,
		RefreshToken: refreshToken,
	}, nil
}

func (s *UserService) RefreshToken(ctx context.Context, req *pb.RefreshTokenRequest) (*pb.RefreshTokenReply, error) {
	rawRefresh := strings.TrimSpace(req.GetRefreshToken())
	if rawRefresh == "" || s.sessions == nil {
		return nil, biz.ErrUnauthorized
	}

	uid, err := s.sessions.ConsumeRefreshToken(ctx, rawRefresh)
	if err != nil {
		if errors.Is(err, auth.ErrRefreshTokenInvalid) {
			return nil, biz.ErrUnauthorized
		}
		s.log.WithContext(ctx).Errorf("userService: consume refresh token failed: %v", err)
		return nil, biz.ErrInternalError
	}

	user, err := s.user.FindByUID(ctx, uid)
	if err != nil {
		if errors.Is(err, biz.ErrUserNotFound) {
			return nil, biz.ErrUnauthorized
		}
		s.log.WithContext(ctx).Errorf("userService: find user for refresh failed: %v", err)
		return nil, biz.ErrInternalError
	}
	if user.Status == "banned" {
		return nil, biz.ErrUserBanned
	}

	accessToken, newRefreshToken, err := s.issueSessionTokens(ctx, user)
	if err != nil {
		return nil, err
	}

	return &pb.RefreshTokenReply{
		Token:        accessToken,
		RefreshToken: newRefreshToken,
	}, nil
}

func (s *UserService) Logout(ctx context.Context, req *pb.LogoutRequest) (*pb.LogoutReply, error) {
	claims, err := s.getJwtClaim(ctx)
	if err != nil {
		return nil, biz.ErrUnauthorized
	}

	if s.sessions != nil {
		if claims.ID != "" && claims.ExpiresAt != nil {
			if remaining := time.Until(claims.ExpiresAt.Time); remaining > 0 {
				if err := s.sessions.RevokeAccessToken(ctx, claims.ID, remaining); err != nil {
					s.log.WithContext(ctx).Errorf("userService: revoke access token failed: %v", err)
					return nil, biz.ErrInternalError
				}
			}
		}
		if rawRefresh := strings.TrimSpace(req.GetRefreshToken()); rawRefresh != "" {
			if _, err := s.sessions.ConsumeRefreshToken(ctx, rawRefresh); err != nil && !errors.Is(err, auth.ErrRefreshTokenInvalid) {
				s.log.WithContext(ctx).Errorf("userService: revoke refresh token failed: %v", err)
				return nil, biz.ErrInternalError
			}
		}
	}

	return &pb.LogoutReply{Status: "退出成功"}, nil
}

func (s *UserService) FindUser(ctx context.Context, req *pb.FindUserRequest) (*pb.FindUserReply, error) {
	s.log.WithContext(ctx).Infof("userService: FindUser received: Uid=%s", req.Keyword)
	user, err := s.user.FindUser(ctx, req.Keyword)
	if err != nil {
		s.log.WithContext(ctx).Errorf("userService: FindUser failed: %v", err)
		return nil, err
	}
	return &pb.FindUserReply{
		Uid:    user.Uid,
		Name:   user.Name,
		Avatar: user.Avatar,
	}, nil
}

func (s *UserService) DeleteUser(ctx context.Context, req *pb.DeleteUserRequest) (*pb.DeleteUserReply, error) {
	claims, err := s.getJwtClaim(ctx)
	if err != nil {
		return nil, biz.ErrUnauthorized
	}

	s.log.WithContext(ctx).Infof("userService: DeleteUser received: Uid=%s", claims.UID)
	if _, err := s.user.DeleteUser(ctx, claims.UID); err != nil {
		s.log.WithContext(ctx).Errorf("userService: DeleteUser failed: %v", err)
		return nil, err
	}
	return &pb.DeleteUserReply{Status: "删除成功"}, nil
}

func (s *UserService) UpdateUserInfo(ctx context.Context, req *pb.UpdateUserInfoRequest) (*pb.UpdateUserInfoReply, error) {
	claims, err := s.getJwtClaim(ctx)
	if err != nil {
		return nil, biz.ErrUnauthorized
	}

	s.log.WithContext(ctx).Infof("userService: UpdateUserInfo received: Uid=%s, Name=%s", claims.UID, claims.Name)
	user, err := s.user.UpdateUserInfo(ctx, claims.UID, req)
	if err != nil {
		s.log.WithContext(ctx).Errorf("userService: UpdateUserInfo failed: %v", err)
		return nil, err
	}
	return &pb.UpdateUserInfoReply{
		Uid:   user.Uid,
		Name:  user.Name,
		Email: user.Email,
	}, nil
}

func (s *UserService) ResetPassword(ctx context.Context, req *pb.ResetPasswordRequest) (*pb.ResetPasswordReply, error) {
	s.log.WithContext(ctx).Infof("userService: ResetPassword received: Email=%s", req.Email)
	err := s.user.ResetPassword(ctx, req)
	if err != nil {
		s.log.WithContext(ctx).Errorf("userService: ResetPassword failed: %v", err)
		return nil, err
	}
	return &pb.ResetPasswordReply{}, nil
}

func (s *UserService) UploadAvatar(ctx context.Context, req *pb.UploadAvatarRequest) (*pb.UploadAvatarReply, error) {
	claims, err := s.getJwtClaim(ctx)
	if err != nil {
		return nil, biz.ErrUnauthorized
	}

	s.log.WithContext(ctx).Infof("userService: UploadAvatar received: Uid=%s", claims.UID)
	httpReq, ok := http.RequestFromServerContext(ctx)
	if !ok || httpReq == nil {
		return nil, biz.ErrUnauthorized
	}
	f, file, err := httpReq.FormFile("avatar")
	if err != nil || f == nil || file == nil {
		return nil, biz.ErrInvalidArgument
	}
	defer f.Close()

	if file.Size <= 0 || file.Size > 3*1024*1024 {
		return nil, biz.ErrInvalidArgument
	}
	if _, _, err := validateAvatarMagicBytes(f); err != nil {
		return nil, biz.ErrInvalidArgument
	}
	rep, err := s.user.UploadAvatar(ctx, claims.UID, file)
	if err != nil {
		s.log.WithContext(ctx).Errorf("userService: UploadAvatar failed: %v", err)
		return nil, err
	}
	return rep, nil
}

func (s *UserService) ConfirmUser(ctx context.Context, req *pb.ConfirmUserRequest) (*pb.ConfirmUserReply, error) {
	if err := s.user.CheckUserExist(ctx, req.Name, req.Email); err != nil {
		return nil, err
	}
	return &pb.ConfirmUserReply{Status: "验证成功"}, nil
}

func (s *UserService) ConfirmEmail(ctx context.Context, req *pb.ConfirmEmailRequest) (*pb.ConfirmEmailReply, error) {
	target, err := s.verificationTarget(ctx, req.Email, req.Purpose)
	if err != nil {
		return nil, err
	}
	if err := s.user.ConfirmEmail(ctx, target, req.Code); err != nil {
		return nil, err
	}
	return &pb.ConfirmEmailReply{Status: "验证成功"}, nil
}

func (s *UserService) verificationTarget(ctx context.Context, email, purpose string) (biz.VerificationTarget, error) {
	if purpose == "" {
		purpose = biz.VerificationRegister
	}
	target := biz.VerificationTarget{Email: email, Purpose: purpose}
	if purpose == biz.VerificationChangeEmail {
		claims, err := s.getJwtClaim(ctx)
		if err != nil {
			return biz.VerificationTarget{}, err
		}
		target.UID = claims.UID
	}
	return target, nil
}

func (s *UserService) getJwtClaim(ctx context.Context) (*jwt.Claims, error) {
	var revoker auth.RevocationChecker
	if s != nil {
		revoker = s.revoker
	}
	return GetJwtClaim(ctx, revoker)
}

func GetJwtClaim(ctx context.Context, revokers ...auth.RevocationChecker) (*jwt.Claims, error) {
	var revoker auth.RevocationChecker
	if len(revokers) > 0 {
		revoker = revokers[0]
	}
	claims, err := auth.ParseClaimsFromContext(ctx, revoker)
	if err != nil || claims == nil || strings.TrimSpace(claims.UID) == "" {
		return nil, biz.ErrUnauthorized
	}
	return claims, nil
}
