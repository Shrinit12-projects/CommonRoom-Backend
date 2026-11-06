package store

import (
	"context"
	"encoding/json"
	"errors"

	"company-community/backend/internal/domain"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Store struct{ Pool *pgxpool.Pool }

func New(p *pgxpool.Pool) *Store { return &Store{Pool: p} }
func translate(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ErrNotFound
	}
	var pe *pgconn.PgError
	if errors.As(err, &pe) {
		switch pe.Code {
		case "23505", "40001", "40P01":
			return domain.ErrConflict
		case "23503", "23514", "22P02":
			return domain.Invalidf("invalid reference or value")
		}
	}
	return err
}
func (s *Store) transaction(ctx context.Context, fn func(pgx.Tx) error) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return translate(err)
	}
	defer tx.Rollback(ctx)
	if err = fn(tx); err != nil {
		return translate(err)
	}
	return translate(tx.Commit(ctx))
}
func audit(ctx context.Context, tx pgx.Tx, u domain.User, action, id string, details any) error {
	b, err := json.Marshal(details)
	if err != nil {
		return err
	}
	var organizationID any = u.OrganizationID
	if u.Role == "platform_admin" {
		organizationID = nil
	}
	_, err = tx.Exec(ctx, `INSERT INTO audit_logs(actor_id,action,target_id,details,organization_id) VALUES($1,$2,$3,$4,$5)`, u.ID, action, id, b, organizationID)
	return err
}

type scanner interface{ Scan(...any) error }

const postColumns = `p.id::text,p.organization_id::text,p.kind,p.author_id::text,u.display_name,p.title,p.summary,p.body_markdown,p.body_html,p.status,p.hidden,
	ARRAY(SELECT pt.tag_id::text FROM post_tags pt WHERE pt.post_id=p.id ORDER BY pt.tag_id),p.version,p.accepted_answer_id::text,p.created_at,p.updated_at,p.category_id::text,COALESCE((SELECT string_agg(c.name,', ' ORDER BY c.name) FROM post_categories pc JOIN categories c ON c.id=pc.category_id WHERE pc.post_id=p.id),''),p.featured,p.verified,p.duplicate_of::text,
	ARRAY(SELECT pc.category_id::text FROM post_categories pc WHERE pc.post_id=p.id ORDER BY pc.category_id),p.deleted_at IS NOT NULL`

func scanPost(row scanner) (domain.Post, error) {
	var p domain.Post
	err := row.Scan(&p.ID, &p.OrganizationID, &p.Kind, &p.AuthorID, &p.AuthorName, &p.Title, &p.Summary, &p.Body, &p.HTML, &p.Status, &p.Hidden, &p.TagIDs, &p.Version, &p.AcceptedAnswerID, &p.CreatedAt, &p.UpdatedAt, &p.CategoryID, &p.CategoryName, &p.Featured, &p.Verified, &p.DuplicateOf, &p.CategoryIDs, &p.Deleted)
	return p, translate(err)
}

const visible = `(p.deleted_at IS NULL AND ((NOT p.hidden AND (p.kind='question' OR p.status='published' OR p.author_id=$1::uuid)) OR $2::boolean))`

func (s *Store) GetPost(ctx context.Context, id string) (domain.Post, error) {
	return scanPost(s.Pool.QueryRow(ctx, `SELECT `+postColumns+` FROM posts p JOIN users u ON u.id=p.author_id WHERE p.id=$1`, id))
}
func categoryValue(id *string) string {
	if id == nil {
		return ""
	}
	return *id
}
func (s *Store) ListPosts(ctx context.Context, u domain.User, f domain.Filter) ([]domain.Post, error) {
	order := `p.created_at DESC,p.id`
	if f.Sort == "updated" {
		order = `p.updated_at DESC,p.id`
	}
	if f.Sort == "helpful" {
		order = `COALESCE((SELECT count(*) FROM votes v WHERE v.target_type='post' AND v.target_id=p.id),0) DESC,p.updated_at DESC,p.id`
	}
	if f.Sort == "unanswered" {
		order = `(SELECT count(*) FROM answers a WHERE a.question_id=p.id)=0 DESC,p.updated_at DESC,p.id`
	}
	rows, err := s.Pool.Query(ctx, `SELECT `+postColumns+` FROM posts p JOIN users u ON u.id=p.author_id WHERE `+visible+`
 AND ($3='' OR p.kind=$3) AND ($4='' OR p.status=$4)
 AND ($5='' OR p.author_id=NULLIF($5,'')::uuid)
 AND ($6='' OR EXISTS(SELECT 1 FROM post_tags pt WHERE pt.post_id=p.id AND pt.tag_id=NULLIF($6,'')::uuid))
	AND ($7='' OR EXISTS(SELECT 1 FROM post_categories pc WHERE pc.post_id=p.id AND pc.category_id=NULLIF($7,'')::uuid)) AND ($8='' OR p.created_at>=NULLIF($8,'')::date) AND ($9='' OR p.created_at<NULLIF($9,'')::date+interval '1 day')
	AND ($12='' OR p.search_vector @@ websearch_to_tsquery('english',$12) OR starts_with(lower(p.title),lower($12)) OR EXISTS(SELECT 1 FROM post_tags pt JOIN tags t ON t.id=pt.tag_id WHERE pt.post_id=p.id AND (starts_with(lower(t.name),lower($12)) OR starts_with(t.slug,lower($12)))) )
		AND p.organization_id=$13
		ORDER BY `+order+` LIMIT $10 OFFSET $11`, u.ID, u.Moderator(), f.Kind, f.Status, f.AuthorID, f.TagID, f.CategoryID, f.From, f.To, f.Limit, f.Offset, f.Query, u.OrganizationID)
	if err != nil {
		return nil, translate(err)
	}
	defer rows.Close()
	out := []domain.Post{}
	for rows.Next() {
		p, err := scanPost(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}
func validateTags(ctx context.Context, tx pgx.Tx, orgID string, ids []string) error {
	// Lock selected tags so deactivation cannot race publication or association changes.
	rows, err := tx.Query(ctx, `SELECT id FROM tags WHERE organization_id=$1 AND id=ANY($2::uuid[]) AND status='active' ORDER BY id FOR SHARE`, orgID, ids)
	if err != nil {
		return err
	}
	count := 0
	for rows.Next() {
		count++
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	if count != len(ids) {
		return domain.Invalidf("all selected tags must exist and be active")
	}
	return nil
}
func validateCategory(ctx context.Context, tx pgx.Tx, orgID, id string) error {
	if id == "" {
		return nil
	}
	var active bool
	if err := tx.QueryRow(ctx, `SELECT active FROM categories WHERE id=$1 AND organization_id=$2 FOR SHARE`, id, orgID).Scan(&active); err != nil {
		return err
	}
	if !active {
		return domain.Invalidf("category must be active")
	}
	return nil
}
func validateCategories(ctx context.Context, tx pgx.Tx, orgID string, ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	rows, err := tx.Query(ctx, `SELECT id FROM categories WHERE organization_id=$1 AND id=ANY($2::uuid[]) AND active ORDER BY id FOR SHARE`, orgID, ids)
	if err != nil {
		return err
	}
	count := 0
	for rows.Next() {
		count++
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	if count != len(ids) {
		return domain.Invalidf("all selected categories must exist and be active in this organization")
	}
	return nil
}
func replaceCategories(ctx context.Context, tx pgx.Tx, p domain.Post) error {
	if _, err := tx.Exec(ctx, `DELETE FROM post_categories WHERE post_id=$1`, p.ID); err != nil {
		return err
	}
	for _, id := range p.CategoryIDs {
		if _, err := tx.Exec(ctx, `INSERT INTO post_categories(post_id,category_id,organization_id) VALUES($1,$2,$3)`, p.ID, id, p.OrganizationID); err != nil {
			return err
		}
	}
	return nil
}
func sameIDs(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	seen := make(map[string]int, len(a))
	for _, id := range a {
		seen[id]++
	}
	for _, id := range b {
		seen[id]--
		if seen[id] < 0 {
			return false
		}
	}
	return true
}
func replaceTags(ctx context.Context, tx pgx.Tx, p domain.Post) error {
	if _, err := tx.Exec(ctx, `DELETE FROM post_tags WHERE post_id=$1`, p.ID); err != nil {
		return err
	}
	for _, id := range p.TagIDs {
		if _, err := tx.Exec(ctx, `INSERT INTO post_tags(post_id,tag_id) VALUES($1,$2)`, p.ID, id); err != nil {
			return err
		}
	}
	return nil
}
func revision(ctx context.Context, tx pgx.Tx, u domain.User, p domain.Post) error {
	b, err := json.Marshal(p)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO post_revisions(post_id,version,actor_id,snapshot) VALUES($1,$2,$3,$4)`, p.ID, p.Version, u.ID, b)
	return err
}
func (s *Store) CreatePost(ctx context.Context, u domain.User, p domain.Post) (domain.Post, error) {
	err := s.transaction(ctx, func(tx pgx.Tx) error {
		if err := validateTags(ctx, tx, u.OrganizationID, p.TagIDs); err != nil {
			return err
		}
		if err := validateCategories(ctx, tx, u.OrganizationID, p.CategoryIDs); err != nil {
			return err
		}
		err := tx.QueryRow(ctx, `INSERT INTO posts(kind,author_id,title,summary,body_markdown,body_html,status,category_id,organization_id) VALUES($1,$2,$3,$4,$5,$6,$7,NULLIF($8,'')::uuid,$9) RETURNING id::text,version,created_at,updated_at`, p.Kind, u.ID, p.Title, p.Summary, p.Body, p.HTML, p.Status, categoryValue(p.CategoryID), u.OrganizationID).Scan(&p.ID, &p.Version, &p.CreatedAt, &p.UpdatedAt)
		if err != nil {
			return err
		}
		p.AuthorName, p.OrganizationID = u.DisplayName, u.OrganizationID
		if err = replaceCategories(ctx, tx, p); err != nil {
			return err
		}
		if err = replaceTags(ctx, tx, p); err != nil {
			return err
		}
		if err = revision(ctx, tx, u, p); err != nil {
			return err
		}
		if p.Kind == "question" {
			if err = award(ctx, tx, u.ID, "question_created", "post", p.ID, "question:"+p.ID); err != nil {
				return err
			}
			if err = followNotifications(ctx, tx, u.ID, p.ID, p.TagIDs, p.Title, "post"); err != nil {
				return err
			}
		}
		return nil
	})
	return p, err
}
func lockedPost(ctx context.Context, tx pgx.Tx, id string) (domain.Post, error) {
	return scanPost(tx.QueryRow(ctx, `SELECT `+postColumns+` FROM posts p JOIN users u ON u.id=p.author_id WHERE p.id=$1 FOR UPDATE OF p`, id))
}
func savePost(ctx context.Context, tx pgx.Tx, u domain.User, p *domain.Post) error {
	err := tx.QueryRow(ctx, `UPDATE posts SET title=$2,summary=$3,body_markdown=$4,body_html=$5,status=$6,hidden=$7,accepted_answer_id=$8,category_id=$9,duplicate_of=$10,deleted_at=CASE WHEN $11 THEN COALESCE(deleted_at,now()) ELSE NULL END,version=version+1,updated_at=now() WHERE id=$1 RETURNING version,updated_at`, p.ID, p.Title, p.Summary, p.Body, p.HTML, p.Status, p.Hidden, p.AcceptedAnswerID, p.CategoryID, p.DuplicateOf, p.Deleted).Scan(&p.Version, &p.UpdatedAt)
	if err != nil {
		return err
	}
	return revision(ctx, tx, u, *p)
}
func (s *Store) MutatePost(ctx context.Context, u domain.User, id string, fn func(*domain.Post) error) (domain.Post, error) {
	var p domain.Post
	err := s.transaction(ctx, func(tx pgx.Tx) error {
		var err error
		p, err = lockedPost(ctx, tx, id)
		if err != nil {
			return err
		}
		if !u.CanRead(p) {
			return domain.ErrNotFound
		}
		old := p
		if err = fn(&p); err != nil {
			return err
		}
		if !sameIDs(old.CategoryIDs, p.CategoryIDs) {
			if err = validateCategories(ctx, tx, u.OrganizationID, p.CategoryIDs); err != nil {
				return err
			}
			if err = replaceCategories(ctx, tx, p); err != nil {
				return err
			}
		}
		// Retired tags may remain on historical content; only newly added tags must be active.
		previous := map[string]bool{}
		for _, id := range old.TagIDs {
			previous[id] = true
		}
		added := []string{}
		for _, id := range p.TagIDs {
			if !previous[id] {
				added = append(added, id)
			}
		}
		if old.Status != "published" && p.Status == "published" {
			added = p.TagIDs
		}
		if err = validateTags(ctx, tx, u.OrganizationID, added); err != nil {
			return err
		}
		if err = replaceTags(ctx, tx, p); err != nil {
			return err
		}
		if err = savePost(ctx, tx, u, &p); err != nil {
			return err
		}
		if !old.Deleted && p.Deleted {
			if err = audit(ctx, tx, u, "post.deleted", p.ID, map[string]string{"kind": p.Kind, "author_id": p.AuthorID}); err != nil {
				return err
			}
		}
		if old.AcceptedAnswerID != nil && p.AcceptedAnswerID == nil {
			var author string
			if err = tx.QueryRow(ctx, `SELECT author_id::text FROM answers WHERE id=$1`, *old.AcceptedAnswerID).Scan(&author); err != nil {
				return err
			}
			if err = reverseAward(ctx, tx, author, "answer_acceptance_reversed", "answer", *old.AcceptedAnswerID, "accepted:"+*old.AcceptedAnswerID); err != nil {
				return err
			}
		}
		if old.Kind == "blog" && old.Status != "published" && p.Status == "published" {
			if err = award(ctx, tx, p.AuthorID, "blog_published", "post", p.ID, "blog-published:"+p.ID); err != nil {
				return err
			}
			if err = followNotifications(ctx, tx, u.ID, p.ID, p.TagIDs, p.Title, "post"); err != nil {
				return err
			}
		}
		if old.Hidden != p.Hidden {
			return audit(ctx, tx, u, "content.visibility", id, map[string]any{"hidden": p.Hidden})
		}
		return nil
	})
	return p, err
}
func scanAnswer(row scanner) (domain.Answer, error) {
	var a domain.Answer
	err := row.Scan(&a.ID, &a.QuestionID, &a.AuthorID, &a.AuthorName, &a.Body, &a.HTML, &a.CreatedAt, &a.Hidden)
	return a, translate(err)
}

const answerColumns = `a.id::text,a.question_id::text,a.author_id::text,u.display_name,a.body_markdown,a.body_html,a.created_at,a.hidden`

func (s *Store) CreateAnswer(ctx context.Context, u domain.User, id, body, html string) (domain.Answer, error) {
	var a domain.Answer
	err := s.transaction(ctx, func(tx pgx.Tx) error {
		p, err := lockedPost(ctx, tx, id)
		if err != nil {
			return err
		}
		if !u.CanRead(p) {
			return domain.ErrNotFound
		}
		if p.Kind != "question" || p.Hidden || p.Status == "closed" {
			return domain.ErrConflict
		}
		a = domain.Answer{QuestionID: id, AuthorID: u.ID, AuthorName: u.DisplayName, Body: body, HTML: html}
		if err = tx.QueryRow(ctx, `INSERT INTO answers(question_id,author_id,body_markdown,body_html) VALUES($1,$2,$3,$4) RETURNING id::text,created_at`, id, u.ID, body, html).Scan(&a.ID, &a.CreatedAt); err != nil {
			return err
		}
		if err = award(ctx, tx, u.ID, "answer_created", "answer", a.ID, "answer:"+a.ID); err != nil {
			return err
		}
		if err = notify(ctx, tx, p.AuthorID, u.ID, "new_answer", "Your question received an answer", "post", p.ID, "answer-notice:"+a.ID); err != nil {
			return err
		}
		return nil
	})
	return a, err
}
func (s *Store) ListAnswers(ctx context.Context, u domain.User, id string, limit, offset int) ([]domain.Answer, error) {
	p, err := s.GetPost(ctx, id)
	if err != nil {
		return nil, err
	}
	if !u.CanRead(p) || p.Kind != "question" {
		return nil, domain.ErrNotFound
	}
	rows, err := s.Pool.Query(ctx, `SELECT `+answerColumns+` FROM answers a JOIN users u ON u.id=a.author_id JOIN posts p ON p.id=a.question_id WHERE a.question_id=$3 AND p.organization_id=$6 AND u.organization_id=$6 AND (NOT a.hidden OR $2::boolean) AND `+visible+` ORDER BY (a.id=p.accepted_answer_id) DESC NULLS LAST,a.created_at,a.id LIMIT $4 OFFSET $5`, u.ID, u.Moderator(), id, limit, offset, u.OrganizationID)
	if err != nil {
		return nil, translate(err)
	}
	defer rows.Close()
	out := []domain.Answer{}
	for rows.Next() {
		a, err := scanAnswer(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}
func (s *Store) AcceptAnswer(ctx context.Context, u domain.User, id, answerID string) (domain.Post, error) {
	var p domain.Post
	err := s.transaction(ctx, func(tx pgx.Tx) error {
		var err error
		p, err = lockedPost(ctx, tx, id)
		if err != nil {
			return err
		}
		if !u.CanRead(p) {
			return domain.ErrNotFound
		}
		if p.AuthorID != u.ID && !u.Moderator() {
			return domain.ErrForbidden
		}
		if p.Kind != "question" || p.Hidden || p.Status == "closed" {
			return domain.ErrConflict
		}
		var exists bool
		if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM answers WHERE id=$1 AND question_id=$2)`, answerID, id).Scan(&exists); err != nil {
			return err
		}
		if !exists {
			return domain.Invalidf("answer must belong to this question")
		}
		if p.AcceptedAnswerID != nil && *p.AcceptedAnswerID == answerID {
			return nil
		}
		p.AcceptedAnswerID = &answerID
		p.Status = "solved"
		if err = savePost(ctx, tx, u, &p); err != nil {
			return err
		}
		var answerAuthor string
		if err = tx.QueryRow(ctx, `SELECT author_id::text FROM answers WHERE id=$1`, answerID).Scan(&answerAuthor); err != nil {
			return err
		}
		if err = award(ctx, tx, answerAuthor, "answer_accepted", "answer", answerID, "accepted:"+answerID); err != nil {
			return err
		}
		if err = notify(ctx, tx, answerAuthor, u.ID, "answer_accepted", "Your answer was accepted", "post", p.ID, "accepted-notice:"+answerID); err != nil {
			return err
		}
		return audit(ctx, tx, u, "question.answer_accepted", id, map[string]string{"answer_id": answerID})
	})
	return p, err
}
func (s *Store) Revisions(ctx context.Context, u domain.User, id string, limit, offset int) ([]map[string]any, error) {
	p, err := s.GetPost(ctx, id)
	if err != nil {
		return nil, err
	}
	if !u.CanRead(p) || p.AuthorID != u.ID && !u.Moderator() {
		return nil, domain.ErrForbidden
	}
	rows, err := s.Pool.Query(ctx, `SELECT r.snapshot FROM post_revisions r JOIN posts p ON p.id=r.post_id WHERE p.id=$1 AND (p.author_id=$2::uuid OR $3::boolean) ORDER BY r.version DESC LIMIT $4 OFFSET $5`, id, u.ID, u.Moderator(), limit, offset)
	if err != nil {
		return nil, translate(err)
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var b []byte
		if err = rows.Scan(&b); err != nil {
			return nil, err
		}
		var snapshot map[string]any
		if err = json.Unmarshal(b, &snapshot); err != nil {
			return nil, err
		}
		out = append(out, snapshot)
	}
	return out, rows.Err()
}
