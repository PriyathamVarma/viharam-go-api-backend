package auth

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

var (
	ErrDeletionPending     = errors.New("an account deletion request is already pending")
	ErrDeletionNotReady    = errors.New("the seven-day review period has not elapsed")
	ErrDeletionNotFound    = errors.New("account deletion request not found")
	ErrDeletionUnsupported = errors.New("account deletion workflow is only available with PostgreSQL")
)

type DeletionRequest struct {
	ID          string     `json:"id"`
	UserID      string     `json:"userId"`
	Status      string     `json:"status"`
	RequestedAt time.Time  `json:"requestedAt"`
	EligibleAt  time.Time  `json:"eligibleAt"`
	ReviewedAt  *time.Time `json:"reviewedAt,omitempty"`
	CompletedAt *time.Time `json:"completedAt,omitempty"`
	Reason      string     `json:"reason,omitempty"`
}

type AccountDeletionRepository interface {
	RequestDeletion(context.Context, string) (DeletionRequest, error)
	ListDeletionRequests(context.Context) ([]DeletionRequest, error)
	ApproveDeletion(context.Context, string, string) (DeletionRequest, string, error)
	RejectDeletion(context.Context, string, string) (DeletionRequest, error)
}

func (r *Repository) RequestDeletion(ctx context.Context, userID string) (DeletionRequest, error) {
	var d DeletionRequest
	err := r.db.Pool.QueryRow(ctx, `
		INSERT INTO account_deletion_requests (user_id)
		SELECT id FROM users WHERE id=$1::uuid AND status='active'
		RETURNING id::text,user_id::text,status,requested_at,eligible_at,reviewed_at,completed_at,COALESCE(reason,'')`, userID).
		Scan(&d.ID, &d.UserID, &d.Status, &d.RequestedAt, &d.EligibleAt, &d.ReviewedAt, &d.CompletedAt, &d.Reason)
	if err == nil {
		_, err = r.db.Pool.Exec(ctx, `UPDATE users SET status='suspended', updated_at=now() WHERE id=$1::uuid`, userID)
		return d, err
	}
	if d, findErr := r.findPendingDeletion(ctx, userID); findErr == nil {
		return d, ErrDeletionPending
	}
	return DeletionRequest{}, err
}

func (r *Repository) findPendingDeletion(ctx context.Context, userID string) (DeletionRequest, error) {
	var d DeletionRequest
	err := r.db.Pool.QueryRow(ctx, `SELECT id::text,user_id::text,status,requested_at,eligible_at,reviewed_at,completed_at,COALESCE(reason,'') FROM account_deletion_requests WHERE user_id=$1::uuid AND status='pending'`, userID).
		Scan(&d.ID, &d.UserID, &d.Status, &d.RequestedAt, &d.EligibleAt, &d.ReviewedAt, &d.CompletedAt, &d.Reason)
	return d, err
}

func (r *Repository) ListDeletionRequests(ctx context.Context) ([]DeletionRequest, error) {
	rows, err := r.db.Pool.Query(ctx, `SELECT id::text,user_id::text,status,requested_at,eligible_at,reviewed_at,completed_at,COALESCE(reason,'') FROM account_deletion_requests WHERE status='pending' ORDER BY eligible_at ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []DeletionRequest
	for rows.Next() {
		var d DeletionRequest
		if err := rows.Scan(&d.ID, &d.UserID, &d.Status, &d.RequestedAt, &d.EligibleAt, &d.ReviewedAt, &d.CompletedAt, &d.Reason); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

func (r *Repository) RejectDeletion(ctx context.Context, requestID, reviewerID string) (DeletionRequest, error) {
	var d DeletionRequest
	err := r.db.Pool.QueryRow(ctx, `UPDATE account_deletion_requests SET status='rejected',reviewed_at=now(),reviewed_by=$2::uuid WHERE id=$1::uuid AND status='pending' RETURNING id::text,user_id::text,status,requested_at,eligible_at,reviewed_at,completed_at,COALESCE(reason,'')`, requestID, reviewerID).
		Scan(&d.ID, &d.UserID, &d.Status, &d.RequestedAt, &d.EligibleAt, &d.ReviewedAt, &d.CompletedAt, &d.Reason)
	if errors.Is(err, pgx.ErrNoRows) {
		return DeletionRequest{}, ErrDeletionNotFound
	}
	if err != nil {
		return DeletionRequest{}, err
	}
	_, err = r.db.Pool.Exec(ctx, `UPDATE users SET status='active',updated_at=now() WHERE id=$1::uuid`, d.UserID)
	return d, err
}

func (r *Repository) ApproveDeletion(ctx context.Context, requestID, reviewerID string) (DeletionRequest, string, error) {
	tx, err := r.db.Pool.Begin(ctx)
	if err != nil {
		return DeletionRequest{}, "", err
	}
	defer tx.Rollback(ctx)

	var d DeletionRequest
	var provider, subject string
	err = tx.QueryRow(ctx, `SELECT id::text,user_id::text,status,requested_at,eligible_at,reviewed_at,completed_at,COALESCE(reason,''),COALESCE((SELECT auth_provider FROM users WHERE id=account_deletion_requests.user_id),'') ,COALESCE((SELECT auth_subject FROM users WHERE id=account_deletion_requests.user_id),'') FROM account_deletion_requests WHERE id=$1::uuid FOR UPDATE`, requestID).
		Scan(&d.ID, &d.UserID, &d.Status, &d.RequestedAt, &d.EligibleAt, &d.ReviewedAt, &d.CompletedAt, &d.Reason, &provider, &subject)
	if errors.Is(err, pgx.ErrNoRows) {
		return DeletionRequest{}, "", ErrDeletionNotFound
	}
	if err != nil {
		return DeletionRequest{}, "", err
	}
	if d.Status != "pending" {
		return DeletionRequest{}, "", ErrDeletionNotFound
	}
	if time.Now().Before(d.EligibleAt) {
		return DeletionRequest{}, "", ErrDeletionNotReady
	}

	// Delete dependent user-owned records first; the user requested a complete
	// purge, including listings, bookings, reviews, support and audit records.
	statements := []string{
		`DELETE FROM property_reviews WHERE traveler_id=$1::uuid OR booking_id IN (SELECT id FROM property_bookings WHERE traveler_id=$1::uuid OR host_id=$1::uuid OR property_id IN (SELECT id FROM properties WHERE host_id=$1::uuid))`,
		`DELETE FROM experience_reviews WHERE traveler_id=$1::uuid`,
		`DELETE FROM experience_bookings WHERE traveler_id=$1::uuid OR ambassador_id=$1::uuid OR experience_id IN (SELECT id FROM experiences WHERE host_id=$1::uuid)`,
		`DELETE FROM booking_locks WHERE traveler_id=$1::uuid OR experience_id IN (SELECT id FROM experiences WHERE host_id=$1::uuid)`,
		`DELETE FROM property_bookings WHERE traveler_id=$1::uuid OR host_id=$1::uuid OR property_id IN (SELECT id FROM properties WHERE host_id=$1::uuid)`,
		`DELETE FROM support_tickets WHERE user_id=$1::uuid`,
		`DELETE FROM incidents WHERE reporter_id=$1::uuid`,
		`DELETE FROM referrals WHERE referrer_id=$1::uuid OR referee_id=$1::uuid`,
		`DELETE FROM assignment_messages WHERE sender_user_id=$1::uuid`,
		`DELETE FROM ambassador_visits WHERE ambassador_user_id=$1::uuid OR host_profile_id IN (SELECT id FROM host_profiles WHERE user_id=$1::uuid)`,
		`DELETE FROM audit_logs WHERE actor_id=$1::uuid`,
		`DELETE FROM document_records WHERE owner_id=$1::uuid`,
		`DELETE FROM loyalty_profiles WHERE user_id=$1::uuid`,
		`DELETE FROM experiences WHERE host_id=$1::uuid`,
		`DELETE FROM properties WHERE host_id=$1::uuid`,
		`DELETE FROM host_profiles WHERE user_id=$1::uuid`,
		`DELETE FROM account_deletion_requests WHERE user_id=$1::uuid`,
		`DELETE FROM users WHERE id=$1::uuid`,
	}
	for _, statement := range statements {
		if _, err := tx.Exec(ctx, statement, d.UserID); err != nil {
			return DeletionRequest{}, "", fmt.Errorf("delete account data: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return DeletionRequest{}, "", err
	}
	now := time.Now().UTC()
	d.Status = "completed"
	d.CompletedAt = &now
	return d, provider + ":" + subject, nil
}
