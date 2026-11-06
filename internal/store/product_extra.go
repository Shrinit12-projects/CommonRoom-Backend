package store

import (
	"context"

	"company-community/backend/internal/domain"
	"github.com/jackc/pgx/v5"
)

func (s *Store) TagDetail(ctx context.Context, u domain.User, id string) (domain.TagInfo, error) {
	var t domain.TagInfo
	err := s.Pool.QueryRow(ctx, `SELECT t.id::text,t.name,t.slug,t.description,t.status,COALESCE((SELECT array_agg(ta.alias ORDER BY ta.alias) FROM tag_aliases ta WHERE ta.tag_id=t.id AND ta.organization_id=t.organization_id),'{}'),(SELECT count(*) FROM post_tags pt JOIN posts p ON p.id=pt.post_id WHERE pt.tag_id=t.id AND p.organization_id=t.organization_id),t.merged_into::text,t.category_id::text,COALESCE(c.name,'') FROM tags t LEFT JOIN categories c ON c.id=t.category_id WHERE t.id=$1 AND t.organization_id=$2`, id, u.OrganizationID).Scan(&t.ID, &t.Name, &t.Slug, &t.Description, &t.Status, &t.Aliases, &t.Usage, &t.CanonicalID, &t.CategoryID, &t.CategoryName)
	return t, translate(err)
}
func (s *Store) SetTagAlias(ctx context.Context, u domain.User, id, alias string) (domain.Tag, error) {
	var t domain.Tag
	err := s.transaction(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `INSERT INTO tag_aliases(alias,tag_id,organization_id) SELECT $1,id,organization_id FROM tags WHERE id=$2 AND organization_id=$3 ON CONFLICT(organization_id,alias) DO UPDATE SET tag_id=EXCLUDED.tag_id`, alias, id, u.OrganizationID); err != nil {
			return err
		}
		var err error
		t, err = scanTag(tx.QueryRow(ctx, `SELECT `+tagColumns+` FROM tags WHERE id=$1 AND organization_id=$2`, id, u.OrganizationID))
		if err != nil {
			return err
		}
		return audit(ctx, tx, u, "tag.alias_added", id, map[string]string{"alias": alias})
	})
	return t, err
}
func (s *Store) MergeTag(ctx context.Context, u domain.User, source, target string) (domain.Tag, error) {
	var out domain.Tag
	err := s.transaction(ctx, func(tx pgx.Tx) error {
		var status, targetStatus string
		if err := tx.QueryRow(ctx, `SELECT status FROM tags WHERE id=$1 AND organization_id=$2 FOR UPDATE`, source, u.OrganizationID).Scan(&status); err != nil {
			return err
		}
		if status != "active" {
			return domain.ErrConflict
		}
		if err := tx.QueryRow(ctx, `SELECT status FROM tags WHERE id=$1 AND organization_id=$2 FOR UPDATE`, target, u.OrganizationID).Scan(&targetStatus); err != nil {
			return err
		}
		if targetStatus != "active" {
			return domain.Invalidf("merge target must be an active canonical tag")
		}
		if _, err := tx.Exec(ctx, `INSERT INTO post_tags(post_id,tag_id) SELECT post_id,$2 FROM post_tags WHERE tag_id=$1 ON CONFLICT DO NOTHING`, source, target); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO tag_aliases(alias,tag_id,organization_id) SELECT name,$2,organization_id FROM tags WHERE id=$1 ON CONFLICT(organization_id,alias) DO UPDATE SET tag_id=EXCLUDED.tag_id`, source, target); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE tag_aliases SET tag_id=$2 WHERE tag_id=$1`, source, target); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `DELETE FROM post_tags WHERE tag_id=$1`, source); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE tags SET status='deprecated',merged_into=$2 WHERE id=$1`, source, target); err != nil {
			return err
		}
		var err error
		out, err = scanTag(tx.QueryRow(ctx, `SELECT `+tagColumns+` FROM tags WHERE id=$1`, target))
		if err != nil {
			return err
		}
		return audit(ctx, tx, u, "tag.merged", source, map[string]string{"target_id": target})
	})
	return out, err
}
func (s *Store) ContentEditorial(ctx context.Context, u domain.User, id, action string, enabled bool) (domain.Post, error) {
	var out domain.Post
	err := s.transaction(ctx, func(tx pgx.Tx) error {
		var err error
		if action == "featured" {
			_, err = tx.Exec(ctx, `UPDATE posts SET featured=$2,featured_by=$3,updated_at=now() WHERE id=$1 AND organization_id=$4`, id, enabled, u.ID, u.OrganizationID)
		} else {
			_, err = tx.Exec(ctx, `UPDATE posts SET verified=$2,verified_by=$3,verified_at=CASE WHEN $2 THEN now() ELSE NULL END,updated_at=now() WHERE id=$1 AND organization_id=$4`, id, enabled, u.ID, u.OrganizationID)
		}
		if err != nil {
			return err
		}
		out, err = scanPost(tx.QueryRow(ctx, `SELECT `+postColumns+` FROM posts p JOIN users u ON u.id=p.author_id WHERE p.id=$1 AND p.organization_id=$2`, id, u.OrganizationID))
		if err != nil {
			return err
		}
		if err = audit(ctx, tx, u, "content."+action, id, map[string]bool{"enabled": enabled}); err != nil {
			return err
		}
		if action == "featured" && out.Kind == "blog" {
			if enabled {
				return award(ctx, tx, out.AuthorID, "featured_blog", "post", id, "featured:"+id)
			}
			return reverseAward(ctx, tx, out.AuthorID, "featured_blog_reversed", "post", id, "featured:"+id)
		}
		return nil
	})
	return out, err
}
func (s *Store) SetDuplicate(ctx context.Context, u domain.User, id, target string) (domain.Post, error) {
	var out domain.Post
	err := s.transaction(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(732981)`); err != nil {
			return err
		}
		p, err := lockedPost(ctx, tx, id)
		if err != nil {
			return err
		}
		if !u.CanRead(p) || p.Kind != "question" || p.Hidden || p.Status != "open" || p.AuthorID != u.ID && !u.Moderator() {
			return domain.ErrForbidden
		}
		canonical, err := lockedPost(ctx, tx, target)
		if err != nil {
			return err
		}
		if !u.CanRead(canonical) || canonical.Kind != "question" || canonical.Hidden || canonical.Status == "duplicate" {
			return domain.Invalidf("duplicate target must be a visible question which is not itself a duplicate")
		}
		oldAccepted := p.AcceptedAnswerID
		p.Status = "duplicate"
		p.DuplicateOf = &target
		p.AcceptedAnswerID = nil
		if err = savePost(ctx, tx, u, &p); err != nil {
			return err
		}
		if oldAccepted != nil {
			var author string
			if err = tx.QueryRow(ctx, `SELECT author_id::text FROM answers WHERE id=$1`, *oldAccepted).Scan(&author); err != nil {
				return err
			}
			if err = reverseAward(ctx, tx, author, "answer_acceptance_reversed", "answer", *oldAccepted, "accepted:"+*oldAccepted); err != nil {
				return err
			}
		}
		if err = audit(ctx, tx, u, "question.marked_duplicate", id, map[string]string{"duplicate_of": target}); err != nil {
			return err
		}
		out = p
		return nil
	})
	return out, err
}
func (s *Store) Analytics(ctx context.Context, u domain.User) (domain.Analytics, error) {
	var a domain.Analytics
	err := s.Pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM users WHERE organization_id=$1 AND deleted_at IS NULL),(SELECT count(*) FROM users WHERE organization_id=$1 AND active AND last_active_at>now()-interval '30 days'),(SELECT count(*) FROM posts WHERE organization_id=$1 AND kind='blog' AND status='published' AND NOT hidden),(SELECT count(*) FROM posts WHERE organization_id=$1 AND kind='question' AND NOT hidden),(SELECT count(*) FROM posts WHERE organization_id=$1 AND kind='question' AND status='solved' AND NOT hidden),(SELECT count(*) FROM reports r JOIN users u ON u.id=r.reporter_id WHERE u.organization_id=$1 AND r.status='pending'),(SELECT count(*) FROM tag_proposals WHERE organization_id=$1 AND status='pending'),(SELECT count(*) FROM search_events WHERE organization_id=$1),(SELECT count(*) FROM search_events WHERE organization_id=$1 AND result_count=0)`, u.OrganizationID).Scan(&a.Users, &a.ActiveUsers, &a.Blogs, &a.Questions, &a.SolvedQuestions, &a.PendingReports, &a.PendingTagProposals, &a.Searches, &a.ZeroResultSearches)
	return a, err
}
func (s *Store) UploadMedia(ctx context.Context, u domain.User, postID, name, contentType string, data []byte, w, h int) (domain.Media, error) {
	var m domain.Media
	err := s.transaction(ctx, func(tx pgx.Tx) error {
		p, err := lockedPost(ctx, tx, postID)
		if err != nil {
			return err
		}
		if p.OrganizationID != u.OrganizationID || p.AuthorID != u.ID && !u.Moderator() {
			return domain.ErrForbidden
		}
		err = tx.QueryRow(ctx, `INSERT INTO media_files(owner_id,post_id,filename,content_type,byte_size,width,height,data) VALUES($1,$2,$3,$4,$5,$6,$7,$8) RETURNING id::text,post_id::text,filename,content_type,byte_size,width,height,created_at`, u.ID, postID, name, contentType, len(data), w, h, data).Scan(&m.ID, &m.PostID, &m.Filename, &m.ContentType, &m.ByteSize, &m.Width, &m.Height, &m.CreatedAt)
		return err
	})
	return m, translate(err)
}
func (s *Store) GetMedia(ctx context.Context, u domain.User, id string) (domain.Media, error) {
	var m domain.Media
	err := s.Pool.QueryRow(ctx, `SELECT m.id::text,m.post_id::text,m.filename,m.content_type,m.byte_size,m.width,m.height,m.created_at,m.data FROM media_files m JOIN posts p ON p.id=m.post_id WHERE m.id=$1 AND p.organization_id=$4 AND p.deleted_at IS NULL AND ((NOT p.hidden AND (p.kind='question' OR p.status='published' OR p.author_id=$2)) OR $3)`, id, u.ID, u.Moderator(), u.OrganizationID).Scan(&m.ID, &m.PostID, &m.Filename, &m.ContentType, &m.ByteSize, &m.Width, &m.Height, &m.CreatedAt, &m.Data)
	return m, translate(err)
}
func (s *Store) SetAnswerHidden(ctx context.Context, u domain.User, id string, hidden bool) (domain.Answer, error) {
	var out domain.Answer
	err := s.transaction(ctx, func(tx pgx.Tx) error {
		var err error
		out, err = scanAnswer(tx.QueryRow(ctx, `UPDATE answers a SET hidden=$2 FROM users u WHERE a.id=$1 AND u.id=a.author_id AND EXISTS(SELECT 1 FROM posts p WHERE p.id=a.question_id AND p.organization_id=$3) RETURNING a.id::text,a.question_id::text,a.author_id::text,u.display_name,a.body_markdown,a.body_html,a.created_at,a.hidden`, id, hidden, u.OrganizationID))
		if err != nil {
			return err
		}
		return audit(ctx, tx, u, "answer.visibility", id, map[string]bool{"hidden": hidden})
	})
	return out, err
}
