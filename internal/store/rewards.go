package store

import (
	"context"
	"time"

	"company-community/backend/internal/domain"
	"github.com/jackc/pgx/v5"
)

func (s *Store) Points(ctx context.Context, u domain.User, limit, offset int) (int, []domain.LedgerEntry, error) {
	var total int
	if err := s.Pool.QueryRow(ctx, `SELECT COALESCE(sum(delta),0) FROM points_ledger WHERE user_id=$1`, u.ID).Scan(&total); err != nil {
		return 0, nil, err
	}
	rows, err := s.Pool.Query(ctx, `SELECT id,event_type,delta,source_type,source_id::text,created_at FROM points_ledger WHERE user_id=$1 ORDER BY created_at DESC,id DESC LIMIT $2 OFFSET $3`, u.ID, limit, offset)
	if err != nil {
		return 0, nil, err
	}
	defer rows.Close()
	out := []domain.LedgerEntry{}
	for rows.Next() {
		var e domain.LedgerEntry
		if err = rows.Scan(&e.ID, &e.EventType, &e.Delta, &e.SourceType, &e.SourceID, &e.CreatedAt); err != nil {
			return 0, nil, err
		}
		out = append(out, e)
	}
	return total, out, rows.Err()
}
func (s *Store) Credits(ctx context.Context, u domain.User, limit, offset int) (float64, []domain.CreditLedgerEntry, error) {
	var total float64
	if err := s.Pool.QueryRow(ctx, `SELECT COALESCE(sum(delta),0)::float8 FROM credits_ledger WHERE user_id=$1`, u.ID).Scan(&total); err != nil {
		return 0, nil, err
	}
	rows, err := s.Pool.Query(ctx, `SELECT id,event_type,delta::float8,source_type,COALESCE(source_id::text,''),created_at FROM credits_ledger WHERE user_id=$1 ORDER BY created_at DESC,id DESC LIMIT $2 OFFSET $3`, u.ID, limit, offset)
	if err != nil {
		return 0, nil, err
	}
	defer rows.Close()
	out := []domain.CreditLedgerEntry{}
	for rows.Next() {
		var e domain.CreditLedgerEntry
		if err = rows.Scan(&e.ID, &e.EventType, &e.Delta, &e.SourceType, &e.SourceID, &e.CreatedAt); err != nil {
			return 0, nil, err
		}
		out = append(out, e)
	}
	return total, out, rows.Err()
}
func (s *Store) Leaderboard(ctx context.Context, user domain.User, period string, limit, offset int) ([]domain.Profile, error) {
	interval := ""
	if period == "week" {
		interval = " AND l.created_at>=now()-interval '7 days'"
	} else if period == "month" {
		interval = " AND l.created_at>=now()-interval '30 days'"
	}
	rows, err := s.Pool.Query(ctx, `SELECT u.id::text,u.display_name,u.bio,COALESCE(sum(l.delta),0)::int points FROM users u LEFT JOIN points_ledger l ON l.user_id=u.id `+interval+` WHERE u.active AND u.organization_id=$3 GROUP BY u.id ORDER BY points DESC,u.display_name,u.id LIMIT $1 OFFSET $2`, limit, offset, user.OrganizationID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.Profile{}
	for rows.Next() {
		var p domain.Profile
		if err = rows.Scan(&p.ID, &p.DisplayName, &p.Bio, &p.Points); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}
func (s *Store) RewardRules(ctx context.Context, u domain.User) ([]domain.RewardRule, error) {
	rows, err := s.Pool.Query(ctx, `SELECT event_type,points,enabled,daily_cap,version,reward_kind FROM reward_rules WHERE organization_id=$1 ORDER BY event_type`, u.OrganizationID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.RewardRule{}
	for rows.Next() {
		var r domain.RewardRule
		if err = rows.Scan(&r.EventType, &r.Points, &r.Enabled, &r.DailyCap, &r.Version, &r.RewardKind); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
func (s *Store) SetRewardRule(ctx context.Context, u domain.User, r domain.RewardRule) (domain.RewardRule, error) {
	err := s.transaction(ctx, func(tx pgx.Tx) error {
		err := tx.QueryRow(ctx, `UPDATE reward_rules SET points=$2,enabled=$3,daily_cap=$4,reward_kind=$5,version=version+1,updated_by=$6,updated_at=now() WHERE event_type=$1 AND organization_id=$7 RETURNING version`, r.EventType, r.Points, r.Enabled, r.DailyCap, r.RewardKind, u.ID, u.OrganizationID).Scan(&r.Version)
		if err != nil {
			return err
		}
		return audit(ctx, tx, u, "reward_rule.updated", u.ID, map[string]any{"event_type": r.EventType, "points": r.Points, "enabled": r.Enabled, "daily_cap": r.DailyCap, "reward_kind": r.RewardKind, "version": r.Version})
	})
	return r, err
}
func (s *Store) RequestRedemption(ctx context.Context, u domain.User, points float64, description string) (map[string]any, error) {
	var out map[string]any
	err := s.transaction(ctx, func(tx pgx.Tx) error {
		var locked string
		if err := tx.QueryRow(ctx, `SELECT id::text FROM users WHERE id=$1 FOR UPDATE`, u.ID).Scan(&locked); err != nil {
			return err
		}
		var total, reserved float64
		if err := tx.QueryRow(ctx, `SELECT COALESCE(sum(delta),0)::float8 FROM credits_ledger WHERE user_id=$1`, u.ID).Scan(&total); err != nil {
			return err
		}
		if err := tx.QueryRow(ctx, `SELECT COALESCE(sum(points_cost),0) FROM redemption_requests WHERE user_id=$1 AND status IN ('pending','approved')`, u.ID).Scan(&reserved); err != nil {
			return err
		}
		if points != total-reserved {
			return domain.Invalidf("claim the full available credit balance; unclaimed credits carry forward")
		}
		var id string
		var created time.Time
		if err := tx.QueryRow(ctx, `INSERT INTO redemption_requests(user_id,points_cost,reward_description) VALUES($1,$2,$3) RETURNING id::text,created_at`, u.ID, points, description).Scan(&id, &created); err != nil {
			return err
		}
		out = map[string]any{"id": id, "points_cost": points, "reward_description": description, "status": "pending", "created_at": created}
		return nil
	})
	return out, err
}
func (s *Store) Redemptions(ctx context.Context, u domain.User, limit, offset int) ([]map[string]any, error) {
	rows, err := s.Pool.Query(ctx, `SELECT r.id::text,r.user_id::text,u.display_name,r.points_cost,r.reward_description,r.status,r.created_at,r.reviewed_at FROM redemption_requests r JOIN users u ON u.id=r.user_id WHERE u.organization_id=$5 AND (r.user_id=$1 OR $2::boolean) ORDER BY r.created_at DESC,r.id LIMIT $3 OFFSET $4`, u.ID, u.Role == "admin", limit, offset, u.OrganizationID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id, user, name, status, description string
		var cost float64
		var created time.Time
		var reviewed *time.Time
		if err = rows.Scan(&id, &user, &name, &cost, &description, &status, &created, &reviewed); err != nil {
			return nil, err
		}
		out = append(out, map[string]any{"id": id, "user_id": user, "display_name": name, "credits": cost, "points_cost": cost, "reward_description": description, "status": status, "created_at": created, "reviewed_at": reviewed})
	}
	return out, rows.Err()
}
func (s *Store) ReviewRedemption(ctx context.Context, u domain.User, id, decision string) (map[string]any, error) {
	var out map[string]any
	err := s.transaction(ctx, func(tx pgx.Tx) error {
		var owner, description, status string
		var cost float64
		if err := tx.QueryRow(ctx, `SELECT r.user_id::text,r.points_cost,r.reward_description,r.status FROM redemption_requests r JOIN users u ON u.id=r.user_id WHERE r.id=$1 AND u.organization_id=$2 FOR UPDATE OF r`, id, u.OrganizationID).Scan(&owner, &cost, &description, &status); err != nil {
			return translate(err)
		}
		if status == "rejected" || status == "fulfilled" {
			return domain.ErrConflict
		}
		var locked string
		if err := tx.QueryRow(ctx, `SELECT id::text FROM users WHERE id=$1 FOR UPDATE`, owner).Scan(&locked); err != nil {
			return err
		}
		if decision == "approved" {
			var credits, reserved float64
			if err := tx.QueryRow(ctx, `SELECT COALESCE(sum(delta),0)::float8 FROM credits_ledger WHERE user_id=$1`, owner).Scan(&credits); err != nil {
				return err
			}
			if err := tx.QueryRow(ctx, `SELECT COALESCE(sum(points_cost),0) FROM redemption_requests WHERE user_id=$1 AND status IN ('pending','approved') AND id<>$2`, owner, id).Scan(&reserved); err != nil {
				return err
			}
			if credits-reserved < cost {
				return domain.ErrConflict
			}
		}
		if decision == "fulfilled" {
			if status != "approved" {
				return domain.ErrConflict
			}
			var balance float64
			if err := tx.QueryRow(ctx, `SELECT COALESCE(sum(delta),0)::float8 FROM credits_ledger WHERE user_id=$1`, owner).Scan(&balance); err != nil {
				return err
			}
			if balance < cost {
				return domain.ErrConflict
			}
			if _, err := tx.Exec(ctx, `INSERT INTO credits_ledger(user_id,event_type,delta,source_type,source_id,idempotency_key,rule_version) VALUES($1,'credits_claimed',-$2,'redemption',$3,'redemption-claim:'||$3::text,1) ON CONFLICT(idempotency_key) DO NOTHING`, owner, cost, id); err != nil {
				return err
			}
		}
		if _, err := tx.Exec(ctx, `UPDATE redemption_requests SET status=$2,reviewed_by=$3,reviewed_at=now() WHERE id=$1`, id, decision, u.ID); err != nil {
			return err
		}
		if err := audit(ctx, tx, u, "redemption.reviewed", id, map[string]string{"decision": decision}); err != nil {
			return err
		}
		out = map[string]any{"id": id, "user_id": owner, "points_cost": cost, "reward_description": description, "status": decision}
		// TODO: send the fulfillment message through the organization's configured messaging provider.
		return notify(ctx, tx, owner, u.ID, "redemption_reviewed", "Your credit claim was "+decision, "redemption", id, "redemption:"+id+":"+decision)
	})
	return out, err
}
func (s *Store) AdminUsers(ctx context.Context, u domain.User, q string, limit, offset int) ([]domain.User, error) {
	rows, err := s.Pool.Query(ctx, `SELECT `+userColumns+` FROM users WHERE organization_id=$4 AND deleted_at IS NULL AND ($1='' OR position(lower($1) in lower(display_name))>0 OR position(lower($1) in lower(email))>0) ORDER BY display_name,id LIMIT $2 OFFSET $3`, q, limit, offset, u.OrganizationID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.User{}
	for rows.Next() {
		usr, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, usr)
	}
	return out, rows.Err()
}
func (s *Store) SubmitReport(ctx context.Context, u domain.User, in domain.ReportInput) (domain.Report, error) {
	var r domain.Report
	err := s.transaction(ctx, func(tx pgx.Tx) error {
		targetType, targetID := in.TargetType, in.TargetID
		if in.TargetType == "comment" {
			if err := tx.QueryRow(ctx, `SELECT target_type,target_id::text FROM comments WHERE id=$1 AND NOT hidden`, in.TargetID).Scan(&targetType, &targetID); err != nil {
				return translate(err)
			}
		}
		if _, _, err := visibleTarget(ctx, tx, u, targetType, targetID); err != nil {
			return err
		}
		err := tx.QueryRow(ctx, `INSERT INTO reports(reporter_id,target_type,target_id,reason,details) VALUES($1,$2,$3,$4,$5) RETURNING id::text,reporter_id::text,target_type,target_id::text,reason,details,status,resolution_note,created_at`, u.ID, in.TargetType, in.TargetID, in.Reason, in.Details).Scan(&r.ID, &r.ReporterID, &r.TargetType, &r.TargetID, &r.Reason, &r.Details, &r.Status, &r.ResolutionNote, &r.CreatedAt)
		return err
	})
	return r, err
}

const reportColumns = `r.id::text,r.reporter_id::text,r.target_type,r.target_id::text,r.reason,r.details,r.status,r.resolution_note,r.created_at`

func (s *Store) Reports(ctx context.Context, u domain.User, status string, limit, offset int) ([]domain.Report, error) {
	rows, err := s.Pool.Query(ctx, `SELECT `+reportColumns+` FROM reports r JOIN users reporter ON reporter.id=r.reporter_id WHERE reporter.organization_id=$4 AND ($1='' OR r.status=$1) ORDER BY (r.status='pending') DESC,r.created_at,r.id LIMIT $2 OFFSET $3`, status, limit, offset, u.OrganizationID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.Report{}
	for rows.Next() {
		var r domain.Report
		if err = rows.Scan(&r.ID, &r.ReporterID, &r.TargetType, &r.TargetID, &r.Reason, &r.Details, &r.Status, &r.ResolutionNote, &r.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
func (s *Store) ReviewReport(ctx context.Context, u domain.User, id, decision, note string) (domain.Report, error) {
	var out domain.Report
	err := s.transaction(ctx, func(tx pgx.Tx) error {
		var err error
		out, err = scanReport(tx.QueryRow(ctx, `UPDATE reports r SET status=$2,resolution_note=$3,reviewer_id=$4,reviewed_at=now() FROM users reporter WHERE r.id=$1 AND r.status='pending' AND reporter.id=r.reporter_id AND reporter.organization_id=$5 RETURNING `+reportColumns, id, decision, note, u.ID, u.OrganizationID))
		if err != nil {
			return err
		}
		if err = audit(ctx, tx, u, "report.reviewed", id, map[string]string{"decision": decision}); err != nil {
			return err
		}
		if decision == "resolved" {
			switch out.TargetType {
			case "post":
				_, err = tx.Exec(ctx, `UPDATE posts SET hidden=true,version=version+1,updated_at=now() WHERE id=$1 AND organization_id=$2`, out.TargetID, u.OrganizationID)
			case "answer":
				_, err = tx.Exec(ctx, `UPDATE answers SET hidden=true WHERE id=$1 AND EXISTS(SELECT 1 FROM posts p WHERE p.id=answers.question_id AND p.organization_id=$2)`, out.TargetID, u.OrganizationID)
			case "comment":
				_, err = tx.Exec(ctx, `UPDATE comments SET hidden=true,updated_at=now() WHERE id=$1 AND ((target_type='post' AND EXISTS(SELECT 1 FROM posts p WHERE p.id=target_id AND p.organization_id=$2)) OR (target_type='answer' AND EXISTS(SELECT 1 FROM answers a JOIN posts p ON p.id=a.question_id WHERE a.id=target_id AND p.organization_id=$2)))`, out.TargetID, u.OrganizationID)
			}
			if err != nil {
				return err
			}
		}
		return nil
	})
	return out, err
}
func scanReport(row scanner) (domain.Report, error) {
	var r domain.Report
	err := row.Scan(&r.ID, &r.ReporterID, &r.TargetType, &r.TargetID, &r.Reason, &r.Details, &r.Status, &r.ResolutionNote, &r.CreatedAt)
	return r, translate(err)
}
