// Package verification owns verification policy independently of its storage adapter.
package verification

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"github.com/example/auth-service/internal/domain"
	"math/big"
	"strings"
	"time"
)

type Options struct {
	SignupCodeTTL, SignupHardTTL, EmailCodeTTL, EmailHardTTL time.Duration
	// GenerateCode is an explicit test seam. Production uses crypto/rand only.
	GenerateCode func() (string, error)
}
type Service struct {
	repo domain.VerificationRepository
	opts Options
}

var ErrExpired = errors.New("verification proof expired")
var ErrInvalidCode = errors.New("verification code invalid")
var ErrMismatch = errors.New("verification receipt binding mismatch")
var ErrUnavailable = errors.New("verification persistence unavailable")
var errCodeConflict = errors.New("verification code allocation conflict")

func NewService(repo domain.VerificationRepository, opts Options) *Service {
	return &Service{repo: repo, opts: opts}
}
func (s *Service) code() (string, error) {
	if s.opts.GenerateCode != nil {
		return s.opts.GenerateCode()
	}
	n, err := rand.Int(rand.Reader, big.NewInt(10000))
	if err != nil {
		return "", ErrUnavailable
	}
	return fmt.Sprintf("%04d", n.Int64()), nil
}
func norm(s string) string          { return strings.ToLower(strings.TrimSpace(s)) }
func seconds(d time.Duration) int64 { return int64(d / time.Second) }
func (s *Service) run(ctx context.Context, action string, args []interface{}) ([]interface{}, error) {
	if s == nil || s.repo == nil {
		return nil, ErrUnavailable
	}
	data, err := s.repo.Execute(ctx, action, args)
	if err != nil {
		return nil, ErrUnavailable
	}
	if len(data) == 0 {
		return nil, ErrUnavailable
	}
	status, ok := data[0].(string)
	if !ok {
		return nil, ErrUnavailable
	}
	switch status {
	case "ok":
		if len(data) == 2 {
			if tuple, ok := data[1].([]interface{}); ok {
				return tuple, nil
			}
		}
		return nil, ErrUnavailable
	case "not_found":
		return nil, domain.ErrNotFound
	case "expired":
		return nil, ErrExpired
	case "invalid_code":
		return nil, ErrInvalidCode
	case "mismatch":
		return nil, ErrMismatch
	case "code_conflict":
		return nil, errCodeConflict
	default:
		return nil, ErrUnavailable
	}
}
func (s *Service) valid() bool {
	return s != nil && s.opts.SignupCodeTTL >= time.Second && s.opts.SignupHardTTL >= s.opts.SignupCodeTTL && s.opts.EmailCodeTTL >= time.Second && s.opts.EmailHardTTL >= s.opts.EmailCodeTTL
}
func (s *Service) StartSignup(ctx context.Context, email, passwordHash string) error {
	if !s.valid() || norm(email) == "" || strings.TrimSpace(passwordHash) == "" {
		return ErrUnavailable
	}
	for attempt := 0; attempt < 10; attempt++ {
		code, err := s.code()
		if err != nil {
			return err
		}
		_, err = s.run(ctx, "signup_start", []interface{}{norm(email), passwordHash, code, time.Now().UTC().Unix(), seconds(s.opts.SignupCodeTTL), seconds(s.opts.SignupHardTTL)})
		if errors.Is(err, errCodeConflict) {
			continue
		}
		return err
	}
	return ErrUnavailable
}
func (s *Service) ResendSignup(ctx context.Context, email string) error {
	if !s.valid() {
		return ErrUnavailable
	}
	for attempt := 0; attempt < 10; attempt++ {
		code, err := s.code()
		if err != nil {
			return err
		}
		_, err = s.run(ctx, "signup_resend", []interface{}{norm(email), code, time.Now().UTC().Unix(), seconds(s.opts.SignupCodeTTL), seconds(s.opts.SignupHardTTL)})
		if errors.Is(err, errCodeConflict) {
			continue
		}
		return err
	}
	return ErrUnavailable
}
func (s *Service) VerifySignup(ctx context.Context, email, code string) (string, error) {
	if !s.valid() {
		return "", ErrUnavailable
	}
	tuple, err := s.run(ctx, "signup_verify", []interface{}{norm(email), strings.TrimSpace(code), time.Now().UTC().Unix(), seconds(s.opts.SignupHardTTL)})
	if err != nil {
		return "", err
	}
	if len(tuple) != 7 {
		return "", ErrUnavailable
	}
	password, ok := tuple[1].(string)
	if !ok || password == "" {
		return "", ErrUnavailable
	}
	return password, nil
}
func fingerprint(email, code string) string {
	h := sha256.New()
	var b [8]byte
	for _, part := range []string{email, code} {
		binary.BigEndian.PutUint64(b[:], uint64(len(part)))
		_, _ = h.Write(b[:])
		_, _ = h.Write([]byte(part))
	}
	return hex.EncodeToString(h.Sum(nil))
}
func validOperation(s string) bool {
	if s == "00000000-0000-0000-0000-000000000000" || len(s) != 36 || s[8] != '-' || s[13] != '-' || s[18] != '-' || s[23] != '-' || strings.ToLower(s) != s {
		return false
	}
	b, err := hex.DecodeString(strings.ReplaceAll(s, "-", ""))
	return err == nil && len(b) == 16
}
func number(v interface{}) (int64, bool) {
	switch n := v.(type) {
	case uint64:
		if n > uint64(^uint64(0)>>1) {
			return 0, false
		}
		return int64(n), true
	case int64:
		return n, true
	case uint32:
		return int64(n), true
	case int:
		return int64(n), true
	case uint:
		return int64(n), true
	case float64:
		return int64(n), n == float64(int64(n))
	default:
		return 0, false
	}
}
func (s *Service) ConsumeSignupProof(ctx context.Context, email, code, operation string) (*domain.SignupProofReceipt, error) {
	email = norm(email)
	code = strings.TrimSpace(code)
	if !s.valid() || email == "" || code == "" || !validOperation(operation) {
		return nil, ErrMismatch
	}
	now := time.Now().UTC()
	tuple, err := s.run(ctx, "signup_consume", []interface{}{email, code, operation, fingerprint(email, code), now.Unix(), seconds(s.opts.SignupHardTTL)})
	if err != nil {
		return nil, err
	}
	if len(tuple) != 5 {
		return nil, ErrUnavailable
	}
	op, ok1 := tuple[0].(string)
	owner, ok2 := tuple[1].(string)
	fp, ok3 := tuple[2].(string)
	password, ok4 := tuple[3].(string)
	expiry, ok5 := number(tuple[4])
	if !ok1 || !ok2 || !ok3 || !ok4 || !ok5 || op != operation || owner != email || fp != fingerprint(email, code) || password == "" || expiry <= now.Unix() {
		return nil, ErrMismatch
	}
	return &domain.SignupProofReceipt{OperationID: op, Email: owner, PasswordHash: password, ExpiresAt: time.Unix(expiry, 0).UTC()}, nil
}
func uuid() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", ErrUnavailable
	}
	b[6] = (b[6] & 15) | 64
	b[8] = (b[8] & 63) | 128
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[:4], b[4:6], b[6:8], b[8:10], b[10:]), nil
}
func (s *Service) StartEmailChange(ctx context.Context, userID, email string) (string, error) {
	if !s.valid() || userID == "" || norm(email) == "" {
		return "", ErrUnavailable
	}
	for i := 0; i < 10; i++ {
		id, err := uuid()
		if err != nil {
			return "", err
		}
		code, err := s.code()
		if err != nil {
			return "", err
		}
		_, err = s.run(ctx, "email_start", []interface{}{id, userID, norm(email), code, time.Now().UTC().Unix(), seconds(s.opts.EmailCodeTTL), seconds(s.opts.EmailHardTTL)})
		if errors.Is(err, errCodeConflict) {
			continue
		}
		if err != nil {
			return "", err
		}
		return id, nil
	}
	return "", ErrUnavailable
}
func (s *Service) VerifyEmailChange(ctx context.Context, code string) (string, string, error) {
	if !s.valid() {
		return "", "", ErrUnavailable
	}
	tuple, err := s.run(ctx, "email_verify", []interface{}{strings.TrimSpace(code), time.Now().UTC().Unix(), seconds(s.opts.EmailHardTTL)})
	if err != nil {
		return "", "", err
	}
	if len(tuple) != 7 {
		return "", "", ErrUnavailable
	}
	user, ok1 := tuple[1].(string)
	email, ok2 := tuple[2].(string)
	if !ok1 || !ok2 || user == "" || norm(email) == "" {
		return "", "", ErrUnavailable
	}
	return user, email, nil
}
func (s *Service) StartPasswordReset(ctx context.Context, email string) (string, error) {
	if !s.valid() || norm(email) == "" {
		return "", ErrUnavailable
	}
	id, err := uuid()
	if err != nil {
		return "", err
	}
	for attempt := 0; attempt < 10; attempt++ {
		code, err := s.code()
		if err != nil {
			return "", err
		}
		_, err = s.run(ctx, "reset_start", []interface{}{norm(email), id, code, time.Now().UTC().Unix(), seconds(s.opts.EmailCodeTTL), seconds(s.opts.EmailHardTTL)})
		if errors.Is(err, errCodeConflict) {
			continue
		}
		if err != nil {
			return "", err
		}
		return id, nil
	}
	return "", ErrUnavailable
}
func (s *Service) VerifyPasswordReset(ctx context.Context, email, code string) error {
	if !s.valid() {
		return ErrUnavailable
	}
	_, err := s.run(ctx, "reset_verify", []interface{}{norm(email), strings.TrimSpace(code), time.Now().UTC().Unix(), seconds(s.opts.EmailHardTTL)})
	return err
}

var _ domain.VerificationClient = (*Service)(nil)
var _ domain.SignupProofConsumer = (*Service)(nil)
