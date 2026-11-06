package store

import (
	"company-community/backend/internal/domain"
	"context"
	"github.com/jackc/pgx/v5"
)

func (s *Store) SubmitCommunity(ctx context.Context, u domain.User, kind, mode string, people []string, message string) (domain.CommunitySubmission, error) {
	var out domain.CommunitySubmission
	err := s.transaction(ctx, func(tx pgx.Tx) error {
		out = domain.CommunitySubmission{Kind: kind, Mode: mode, AuthorID: u.ID, Author: u.DisplayName, Message: message, Status: "pending"}
		if err := tx.QueryRow(ctx, `INSERT INTO community_submissions(kind,mode,author_id,message,organization_id) VALUES($1,$2,$3,$4,$5) RETURNING id::text,created_at`, kind, mode, u.ID, message, u.OrganizationID).Scan(&out.ID, &out.CreatedAt); err != nil {
			return err
		}
		role := "recipient"
		if kind == "feedback" && mode == "request" {
			role = "feedback_provider"
		}
		for _, id := range people {
			if _, err := tx.Exec(ctx, `INSERT INTO community_submission_people(submission_id,user_id,role) SELECT $1,id,$3 FROM users WHERE id=$2 AND active AND organization_id=$4`, out.ID, id, role, u.OrganizationID); err != nil {
				return err
			}
			var found bool
			if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM community_submission_people WHERE submission_id=$1 AND user_id=$2 AND role=$3)`, out.ID, id, role).Scan(&found); err != nil {
				return err
			}
			if !found {
				return domain.Invalidf("all selected teammates must be active")
			}
		}
		return nil
	})
	return out, err
}

func (s *Store) CommunitySubmissions(ctx context.Context, u domain.User, kind string, pending bool, limit, offset int) ([]domain.CommunitySubmission, error) {
	if pending && u.Role != "admin" {
		return nil, domain.ErrForbidden
	}
	rows, err := s.Pool.Query(ctx, `SELECT cs.id::text,cs.kind,cs.mode,cs.author_id::text,author.display_name,cs.message,cs.status,cs.created_at FROM community_submissions cs JOIN users author ON author.id=cs.author_id WHERE cs.organization_id=$7 AND cs.kind=$1 AND ((($2::boolean AND cs.status='pending') AND $3::boolean) OR (NOT $2::boolean AND ((cs.kind='appreciation' AND cs.status='approved') OR cs.author_id=$4 OR (cs.status='approved' AND EXISTS(SELECT 1 FROM community_submission_people csp WHERE csp.submission_id=cs.id AND csp.user_id=$4))))) ORDER BY cs.created_at DESC,cs.id LIMIT $5 OFFSET $6`, kind, pending, u.Role == "admin", u.ID, limit, offset, u.OrganizationID)
	if err != nil {
		return nil, translate(err)
	}
	defer rows.Close()
	out := []domain.CommunitySubmission{}
	for rows.Next() {
		var x domain.CommunitySubmission
		if err = rows.Scan(&x.ID, &x.Kind, &x.Mode, &x.AuthorID, &x.Author, &x.Message, &x.Status, &x.CreatedAt); err != nil {
			return nil, err
		}
		people, err := s.Pool.Query(ctx, `SELECT u.id::text,u.display_name FROM community_submission_people csp JOIN users u ON u.id=csp.user_id JOIN community_submissions cs ON cs.id=csp.submission_id WHERE csp.submission_id=$1 AND cs.organization_id=$2 ORDER BY u.display_name,u.id`, x.ID, u.OrganizationID)
		if err != nil {
			return nil, err
		}
		for people.Next() {
			var p domain.CommunityPerson
			if err = people.Scan(&p.ID, &p.DisplayName); err != nil {
				people.Close()
				return nil, err
			}
			x.People = append(x.People, p)
		}
		err = people.Err()
		people.Close()
		if err != nil {
			return nil, err
		}
		out = append(out, x)
	}
	return out, rows.Err()
}

func (s *Store) ReviewCommunity(ctx context.Context, u domain.User, id, decision string) (domain.CommunitySubmission, error) {
	var out domain.CommunitySubmission
	err := s.transaction(ctx, func(tx pgx.Tx) error {
		err := tx.QueryRow(ctx, `UPDATE community_submissions cs SET status=$2,reviewer_id=$3,reviewed_at=now() FROM users author WHERE cs.id=$1 AND cs.status='pending' AND cs.organization_id=$4 AND author.id=cs.author_id RETURNING cs.id::text,cs.kind,cs.mode,cs.author_id::text,author.display_name,cs.message,cs.status,cs.created_at`, id, decision, u.ID, u.OrganizationID).Scan(&out.ID, &out.Kind, &out.Mode, &out.AuthorID, &out.Author, &out.Message, &out.Status, &out.CreatedAt)
		if err != nil {
			return translate(err)
		}
		if err = audit(ctx, tx, u, "community_submission.reviewed", id, map[string]string{"decision": decision, "kind": out.Kind}); err != nil {
			return err
		}
		if decision == "approved" {
			rows, e := tx.Query(ctx, `SELECT user_id::text FROM community_submission_people WHERE submission_id=$1`, id)
			if e != nil {
				return e
			}
			ids := []string{out.AuthorID}
			for rows.Next() {
				var recipient string
				if e = rows.Scan(&recipient); e != nil {
					rows.Close()
					return e
				}
				ids = append(ids, recipient)
			}
			e = rows.Err()
			rows.Close()
			if e != nil {
				return e
			}
			event := "feedback_approved"
			title := "Feedback is ready to view"
			if out.Kind == "appreciation" {
				event = "appreciation_approved"
				title = "A team appreciation was approved"
			}
			for _, recipient := range ids {
				if e = notify(ctx, tx, recipient, u.ID, event, title, "community_submission", id, "community-submission:"+id+":"+recipient); e != nil {
					return e
				}
			}
		}
		return nil
	})
	return out, err
}
