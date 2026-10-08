package repo

import (
	"context"
	"errors"
	"time"

	"github.com/example/auth-service/internal/domain"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"gorm.io/gorm/logger"
)

var _ domain.SignupCompletionRepository = (*authUserRepo)(nil)

var errSignupBinding = errors.New("signup completion binding mismatch")

// Signup receipt writes contain credential material. Disable SQL logging for
// these transactions even when the application's development logger is verbose.
func (r *authUserRepo) signupDB(ctx context.Context) *gorm.DB {
	return r.db.WithContext(ctx).Session(&gorm.Session{Logger: logger.Discard})
}

func (r *authUserRepo) BeginSignupCompletion(ctx context.Context, email, proof, operationID, principalID string) (*domain.SignupCompletion, error) {
	var op domain.SignupCompletion
	err := r.signupDB(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec("SELECT pg_advisory_xact_lock(hashtext(?))", email).Error; err != nil {
			return err
		}
		err := tx.Where("email = ? AND proof_fingerprint = ?", email, proof).First(&op).Error
		if err == nil {
			return nil
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		var count int64
		if err := tx.Model(&domain.AuthUser{}).Where("email = ?", email).Count(&count).Error; err != nil {
			return err
		}
		if count != 0 {
			return errSignupBinding
		}
		op = domain.SignupCompletion{OperationID: operationID, PrincipalID: principalID, Email: email, ProofFingerprint: proof, State: domain.SignupPending}
		return tx.Create(&op).Error
	})
	if err != nil {
		return nil, err
	}
	return &op, nil
}

func signupOperation(tx *gorm.DB, id string) (*domain.SignupCompletion, error) {
	var op domain.SignupCompletion
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("operation_id = ?", id).First(&op).Error; err != nil {
		return nil, normalizeError(err)
	}
	return &op, nil
}

func (r *authUserRepo) StoreSignupReceipt(ctx context.Context, id string, receipt *domain.SignupProofReceipt) (*domain.SignupCompletion, error) {
	var persisted *domain.SignupCompletion
	err := r.signupDB(ctx).Transaction(func(tx *gorm.DB) error {
		op, err := signupOperation(tx, id)
		if err != nil {
			return err
		}
		if receipt == nil || receipt.OperationID != id || receipt.Email != op.Email || !receipt.ExpiresAt.After(time.Now().UTC()) {
			return errSignupBinding
		}
		if _, err := bcrypt.Cost([]byte(receipt.PasswordHash)); err != nil {
			return errSignupBinding
		}
		if op.State == domain.SignupCompleted {
			return errSignupBinding
		}
		if op.State == domain.SignupVerified {
			if op.PasswordHash != receipt.PasswordHash || op.ReceiptExpiresAt == nil || !op.ReceiptExpiresAt.Equal(receipt.ExpiresAt) {
				return errSignupBinding
			}
			persisted = op
			return nil
		}
		if op.State != domain.SignupPending || op.PasswordHash != "" || op.ReceiptExpiresAt != nil {
			return errSignupBinding
		}
		op.PasswordHash = receipt.PasswordHash
		op.ReceiptExpiresAt = &receipt.ExpiresAt
		op.State = domain.SignupVerified
		if err := tx.Save(op).Error; err != nil {
			return err
		}
		persisted = op
		return nil
	})
	return persisted, err
}

func exactSignupPrincipal(op *domain.SignupCompletion, user *domain.AuthUser) bool {
	return user.ID == op.PrincipalID && user.Email == op.Email && user.PasswordHash != nil && *user.PasswordHash == op.PasswordHash && op.PasswordHash != ""
}

func (r *authUserRepo) EnsureSignupPrincipal(ctx context.Context, id string) (*domain.AuthUser, error) {
	var user domain.AuthUser
	err := r.signupDB(ctx).Transaction(func(tx *gorm.DB) error {
		op, err := signupOperation(tx, id)
		if err != nil {
			return err
		}
		if op.State != domain.SignupVerified || op.PasswordHash == "" || op.ReceiptExpiresAt == nil {
			return errSignupBinding
		}
		if err := tx.Exec("SELECT pg_advisory_xact_lock(hashtext(?))", op.Email).Error; err != nil {
			return err
		}
		err = tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("email = ? OR id = ?", op.Email, op.PrincipalID).First(&user).Error
		if err == nil {
			if !op.PrincipalCreated || !exactSignupPrincipal(op, &user) {
				return errSignupBinding
			}
			return nil
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		if op.PrincipalCreated {
			return errSignupBinding
		}
		now := time.Now().UTC()
		password := op.PasswordHash
		user = domain.AuthUser{ID: op.PrincipalID, Email: op.Email, PasswordHash: &password, PasswordUpdatedAt: &now}
		if err := tx.Create(&user).Error; err != nil {
			return err
		}
		op.PrincipalCreated = true
		return tx.Save(op).Error
	})
	if err != nil {
		return nil, err
	}
	return &user, nil
}

// requireFreshReceipt is true for code completion. Password-authenticated
// repair may complete after receipt expiry without consuming/replaying proof.
func (r *authUserRepo) CompleteSignup(ctx context.Context, id string, now time.Time, requireFreshReceipt bool) (bool, error) {
	won := false
	err := r.signupDB(ctx).Transaction(func(tx *gorm.DB) error {
		op, err := signupOperation(tx, id)
		if err != nil {
			return err
		}
		if op.State != domain.SignupVerified {
			return nil
		}
		if !op.PrincipalCreated {
			return errSignupBinding
		}
		var user domain.AuthUser
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", op.PrincipalID).First(&user).Error; err != nil {
			return normalizeError(err)
		}
		if !exactSignupPrincipal(op, &user) {
			return errSignupBinding
		}
		q := tx.Model(&domain.SignupCompletion{}).Where("operation_id = ? AND state = ? AND principal_created = ?", id, domain.SignupVerified, true)
		if requireFreshReceipt {
			q = q.Where("receipt_expires_at > ? AND receipt_expires_at > clock_timestamp()", now)
		}
		result := q.Updates(map[string]interface{}{"state": domain.SignupCompleted, "updated_at": now})
		if result.Error != nil {
			return result.Error
		}
		won = result.RowsAffected == 1
		return nil
	})
	return won, err
}

func (r *authUserRepo) SignupPrincipalPending(ctx context.Context, id string) (bool, error) {
	var count int64
	err := r.db.WithContext(ctx).Model(&domain.SignupCompletion{}).Where("principal_id = ? AND state <> ?", id, domain.SignupCompleted).Count(&count).Error
	return count != 0, err
}

func (r *authUserRepo) FindSignupCompletionByPrincipal(ctx context.Context, id string) (*domain.SignupCompletion, error) {
	var op domain.SignupCompletion
	if err := r.db.WithContext(ctx).Where("principal_id = ?", id).First(&op).Error; err != nil {
		return nil, normalizeError(err)
	}
	return &op, nil
}
