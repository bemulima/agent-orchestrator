package usecase

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/example/auth-service/internal/domain"
	"golang.org/x/crypto/bcrypt"
)

// Length framing prevents distinct email/code pairs sharing an encoding. Raw
// proofs are never persisted. This fingerprint is an operation key, not proof.
func signupProofFingerprint(email, code string) string {
	h := sha256.New()
	for _, value := range []string{"auth-signup-proof-v1", email, code} {
		var size [8]byte
		binary.BigEndian.PutUint64(size[:], uint64(len(value)))
		_, _ = h.Write(size[:])
		_, _ = h.Write([]byte(value))
	}
	return hex.EncodeToString(h.Sum(nil))
}

func (s *authService) completeSignupProof(ctx context.Context, traceID, email, code string) (*domain.AuthUser, *Tokens, error) {
	code = strings.TrimSpace(code)
	if s.completions == nil || s.signupProof == nil {
		return nil, nil, errors.New("signup recovery dependencies unavailable")
	}
	if strings.TrimSpace(code) == "" {
		return nil, nil, errors.New("signup proof required")
	}
	operationID, err := GenerateJTI()
	if err != nil {
		return nil, nil, err
	}
	principalID, err := GenerateJTI()
	if err != nil {
		return nil, nil, err
	}
	op, err := s.completions.BeginSignupCompletion(ctx, email, signupProofFingerprint(email, code), operationID, principalID)
	if err != nil {
		return nil, nil, fmt.Errorf("reserve signup operation: %w", err)
	}
	if op == nil || op.OperationID == "" || op.PrincipalID == "" || op.Email != email || op.ProofFingerprint != signupProofFingerprint(email, code) {
		return nil, nil, errors.New("signup operation binding mismatch")
	}
	if op.State == domain.SignupCompleted {
		return nil, nil, errors.New("signup proof already completed")
	}
	if op.State == domain.SignupPending {
		receipt, err := s.signupProof.ConsumeSignupProof(ctx, email, code, op.OperationID)
		if err != nil {
			return nil, nil, fmt.Errorf("consume signup proof: %w", err)
		}
		if receipt == nil || receipt.OperationID != op.OperationID || receipt.Email != op.Email || !receipt.ExpiresAt.After(time.Now().UTC()) {
			return nil, nil, errors.New("signup receipt owner or expiry mismatch")
		}
		if _, err := bcrypt.Cost([]byte(receipt.PasswordHash)); err != nil {
			return nil, nil, errors.New("signup receipt credential invalid")
		}
		reservedPrincipal, fingerprint := op.PrincipalID, op.ProofFingerprint
		op, err = s.completions.StoreSignupReceipt(ctx, op.OperationID, receipt)
		if err != nil {
			return nil, nil, fmt.Errorf("persist signup receipt: %w", err)
		}
		if op == nil || op.PrincipalID != reservedPrincipal || op.ProofFingerprint != fingerprint || op.OperationID != receipt.OperationID || op.Email != receipt.Email || op.PasswordHash != receipt.PasswordHash || op.ReceiptExpiresAt == nil || !op.ReceiptExpiresAt.Equal(receipt.ExpiresAt) {
			return nil, nil, errors.New("persisted signup receipt mismatch")
		}
	}
	if op.State != domain.SignupVerified || op.ReceiptExpiresAt == nil || !op.ReceiptExpiresAt.After(time.Now().UTC()) {
		return nil, nil, errors.New("signup completion unavailable or expired")
	}
	user, err := s.completions.EnsureSignupPrincipal(ctx, op.OperationID)
	if err != nil {
		return nil, nil, fmt.Errorf("ensure signup principal: %w", err)
	}
	if !signupPrincipalMatches(op, user) {
		return nil, nil, errors.New("signup principal binding mismatch")
	}
	if err := s.provisionSignupActor(ctx, user, op.OperationID); err != nil {
		return nil, nil, err
	}
	won, err := s.completions.CompleteSignup(ctx, op.OperationID, time.Now().UTC(), true)
	if err != nil {
		return nil, nil, fmt.Errorf("commit signup completion: %w", err)
	}
	if !won {
		return nil, nil, errors.New("signup already completed or receipt expired")
	}
	tokens, err := s.issueTokens(ctx, user)
	if err != nil {
		return nil, nil, err
	}
	s.logger.Info().Str("trace_id", traceID).Str("user_id", user.ID).Msg("signup verified")
	return user, tokens, nil
}

func signupPrincipalMatches(op *domain.SignupCompletion, user *domain.AuthUser) bool {
	return op != nil && user != nil && user.ID == op.PrincipalID && user.Email == op.Email && user.PasswordHash != nil && *user.PasswordHash == op.PasswordHash && op.PasswordHash != ""
}

// Only password-authenticated SignIn calls this repair. Code receipt expiry
// does not invalidate an already verified Auth credential. Other issuance
// paths share issueTokens' pending gate and cannot repair implicitly.
func (s *authService) repairPasswordSignup(ctx context.Context, user *domain.AuthUser) error {
	if s.completions == nil {
		return errors.New("signup completion persistence unavailable")
	}
	op, err := s.completions.FindSignupCompletionByPrincipal(ctx, user.ID)
	if errors.Is(err, domain.ErrNotFound) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read signup completion: %w", err)
	}
	if op == nil {
		return errors.New("signup completion missing")
	}
	if op.State == domain.SignupCompleted {
		return nil
	}
	if op.State != domain.SignupVerified || !signupPrincipalMatches(op, user) {
		return errors.New("pending signup credential binding mismatch")
	}
	owned, err := s.completions.EnsureSignupPrincipal(ctx, op.OperationID)
	if err != nil {
		return err
	}
	if !signupPrincipalMatches(op, owned) {
		return errors.New("pending signup principal binding mismatch")
	}
	if err := s.provisionSignupActor(ctx, owned, op.OperationID); err != nil {
		return err
	}
	won, err := s.completions.CompleteSignup(ctx, op.OperationID, time.Now().UTC(), false)
	if err != nil {
		return err
	}
	if !won {
		return errors.New("signup completion changed concurrently; retry password signin")
	}
	return nil
}
