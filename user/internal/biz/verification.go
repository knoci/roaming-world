package biz

import (
	"context"
	"crypto/rand"
	"fmt"
	"math/big"
	"net/mail"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/go-kratos/kratos/v2/errors"
	"github.com/knoci/roaming-world/user/internal/pkg"
)

const (
	VerificationRegister      = "register"
	VerificationResetPassword = "reset_password"
	VerificationChangeEmail   = "change_email"
)

type VerificationTarget struct {
	Email   string
	Purpose string
	UID     string
}

func (target VerificationTarget) validate() error {
	addr, err := mail.ParseAddress(target.Email)
	if err != nil || addr.Address != target.Email || utf8.RuneCountInString(target.Email) > 100 {
		return ErrInvalidArgument
	}
	switch target.Purpose {
	case VerificationRegister, VerificationResetPassword:
		return nil
	case VerificationChangeEmail:
		if target.UID == "" {
			return ErrUnauthorized
		}
		return nil
	default:
		return ErrInvalidArgument
	}
}

func (uc *UserUsecase) SendVerificationCode(ctx context.Context, target VerificationTarget) error {
	if err := target.validate(); err != nil {
		return err
	}
	number, err := rand.Int(rand.Reader, big.NewInt(1_000_000))
	if err != nil {
		return ErrInternalError
	}
	code := fmt.Sprintf("%06d", number.Int64())
	discard, err := uc.repo.IssueVerificationCode(ctx, target, code)
	if err != nil {
		if errors.Is(err, ErrVerificationThrottled) {
			return err
		}
		uc.log.WithContext(ctx).Error("failed to store email verification code")
		return ErrInternalError
	}
	if err := pkg.SendVerificationCode(target.Email, code); err != nil {
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
		defer cancel()
		if err := discard(cleanupCtx); err != nil {
			uc.log.WithContext(ctx).Error("failed to discard undelivered email verification code")
		}
		uc.log.WithContext(ctx).Error("failed to send email verification code")
		return ErrInternalError
	}
	return nil
}

func (uc *UserUsecase) ConfirmEmail(ctx context.Context, target VerificationTarget, code string) error {
	return uc.checkVerificationCode(ctx, target, code, false)
}

func (uc *UserUsecase) checkVerificationCode(ctx context.Context, target VerificationTarget, code string, consume bool) error {
	if err := target.validate(); err != nil {
		return err
	}
	if len(code) != 6 || strings.Trim(code, "0123456789") != "" {
		return ErrVerificationExpired
	}
	err := uc.repo.CheckVerificationCode(ctx, target, code, consume)
	if err == nil || errors.Is(err, ErrVerificationExpired) {
		return err
	}
	uc.log.WithContext(ctx).Error("failed to check email verification code")
	return ErrInternalError
}
