package store

import (
	"context"

	"company-community/backend/internal/domain"
	"github.com/jackc/pgx/v5"
)

func scanTag(row scanner) (domain.Tag, error) {
	var t domain.Tag
	err := row.Scan(&t.ID, &t.Name, &t.Slug, &t.Description, &t.Status)
	return t, translate(err)
}

const tagColumns = `id::text,name,slug,description,status`

func (s *Store) Tags(ctx context.Context, orgID string, limit, offset int) ([]domain.Tag, error) {
	rows, err := s.Pool.Query(ctx, `SELECT `+tagColumns+` FROM tags WHERE organization_id=$1 ORDER BY name,id LIMIT $2 OFFSET $3`, orgID, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.Tag{}
	for rows.Next() {
		t, err := scanTag(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}
func insertTag(ctx context.Context, tx pgx.Tx, u domain.User, in domain.TagInput) (domain.Tag, error) {
	t, err := scanTag(tx.QueryRow(ctx, `INSERT INTO tags(name,slug,description,created_by,organization_id) VALUES($1,$2,$3,$4,$5) RETURNING `+tagColumns, in.Name, in.Slug, in.Description, u.ID, u.OrganizationID))
	if err != nil {
		return t, err
	}
	return t, audit(ctx, tx, u, "tag.created", t.ID, map[string]string{"slug": t.Slug})
}
func (s *Store) CreateTag(ctx context.Context, u domain.User, in domain.TagInput) (domain.Tag, error) {
	var t domain.Tag
	err := s.transaction(ctx, func(tx pgx.Tx) error { var err error; t, err = insertTag(ctx, tx, u, in); return err })
	return t, err
}
func scanProposal(row scanner) (domain.Proposal, error) {
	var p domain.Proposal
	err := row.Scan(&p.ID, &p.Name, &p.Slug, &p.Description, &p.Reason, &p.ProposerID, &p.Status, &p.CreatedAt)
	return p, translate(err)
}

const proposalColumns = `id::text,name,slug,description,reason,proposer_id::text,status,created_at`

func (s *Store) ProposeTag(ctx context.Context, u domain.User, in domain.TagInput) (domain.Proposal, error) {
	var p domain.Proposal
	err := s.transaction(ctx, func(tx pgx.Tx) error {
		var exists bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM tags WHERE organization_id=$3 AND (slug=$1 OR lower(name)=lower($2)) UNION ALL SELECT 1 FROM tag_aliases WHERE organization_id=$3 AND (lower(alias)=lower($1) OR lower(alias)=lower($2)))`, in.Slug, in.Name, u.OrganizationID).Scan(&exists); err != nil {
			return err
		}
		if exists {
			return domain.ErrConflict
		}
		var err error
		p, err = scanProposal(tx.QueryRow(ctx, `INSERT INTO tag_proposals(name,slug,description,reason,proposer_id,organization_id) VALUES($1,$2,$3,$4,$5,$6) RETURNING `+proposalColumns, in.Name, in.Slug, in.Description, in.Reason, u.ID, u.OrganizationID))
		return err
	})
	return p, err
}
func (s *Store) Proposals(ctx context.Context, u domain.User, limit, offset int) ([]domain.Proposal, error) {
	rows, err := s.Pool.Query(ctx, `SELECT `+proposalColumns+` FROM tag_proposals WHERE organization_id=$3 AND (proposer_id=$1::uuid OR $2::boolean) ORDER BY created_at DESC,id LIMIT $4 OFFSET $5`, u.ID, u.Moderator(), u.OrganizationID, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.Proposal{}
	for rows.Next() {
		p, err := scanProposal(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}
func (s *Store) ReviewProposal(ctx context.Context, u domain.User, id, decision string) (domain.Proposal, error) {
	var p domain.Proposal
	err := s.transaction(ctx, func(tx pgx.Tx) error {
		var err error
		p, err = scanProposal(tx.QueryRow(ctx, `SELECT `+proposalColumns+` FROM tag_proposals WHERE id=$1 AND organization_id=$2 FOR UPDATE`, id, u.OrganizationID))
		if err != nil {
			return err
		}
		if p.Status == decision {
			return nil
		}
		if p.Status != "pending" {
			return domain.ErrConflict
		}
		if decision == "approved" {
			if _, err = insertTag(ctx, tx, u, p.TagInput); err != nil {
				return err
			}
		}
		if _, err = tx.Exec(ctx, `UPDATE tag_proposals SET status=$2,reviewer_id=$3,reviewed_at=now() WHERE id=$1`, id, decision, u.ID); err != nil {
			return err
		}
		p.Status = decision
		return audit(ctx, tx, u, "tag_proposal.reviewed", id, map[string]string{"decision": decision})
	})
	return p, err
}
func (s *Store) SetTagStatus(ctx context.Context, u domain.User, id, status string) (domain.Tag, error) {
	var t domain.Tag
	err := s.transaction(ctx, func(tx pgx.Tx) error {
		var err error
		t, err = scanTag(tx.QueryRow(ctx, `UPDATE tags SET status=$2 WHERE id=$1 AND organization_id=$3 RETURNING `+tagColumns, id, status, u.OrganizationID))
		if err != nil {
			return err
		}
		return audit(ctx, tx, u, "tag.status", id, map[string]string{"status": status})
	})
	return t, err
}
