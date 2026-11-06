package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strings"

	"company-community/backend/internal/domain"
)

// Search returns only published/visible content and records a privacy-preserving query hash.
func (s *Store) Search(ctx context.Context, u domain.User, f domain.Filter) ([]domain.SearchResult, error) {
	order := `rank DESC,updated_at DESC,id`
	switch f.Sort {
	case "newest":
		order = `created_at DESC,id`
	case "updated":
		order = `updated_at DESC,id`
	case "helpful":
		order = `helpful DESC,updated_at DESC,id`
	case "unanswered":
		order = `unanswered DESC,updated_at DESC,id`
	}
	rows, err := s.Pool.Query(ctx, `WITH query AS (SELECT websearch_to_tsquery('english',$1) AS q), matching AS (
	 SELECT p.id::text AS id,p.kind,p.title,left(p.body_markdown,240) AS snippet,u.display_name AS author_name,p.status,
	 p.created_at,p.updated_at,ts_rank_cd(p.search_vector,query.q) AS rank,
	 COALESCE((SELECT count(*) FROM votes v WHERE v.target_type='post' AND v.target_id=p.id),0)::int AS helpful,
	 ((SELECT count(*) FROM answers a WHERE a.question_id=p.id AND NOT a.hidden)=0) AS unanswered
	 FROM posts p JOIN users u ON u.id=p.author_id CROSS JOIN query
	 WHERE p.deleted_at IS NULL AND NOT p.hidden AND (p.kind='question' OR p.status='published')
	 AND (p.search_vector @@ query.q OR starts_with(lower(p.title),lower($1)) OR starts_with(lower(u.display_name),lower($1))
	   OR EXISTS(SELECT 1 FROM post_tags pt JOIN tags t ON t.id=pt.tag_id WHERE pt.post_id=p.id AND (starts_with(lower(t.name),lower($1)) OR starts_with(t.slug,lower($1))))
	   OR EXISTS(SELECT 1 FROM post_categories pc JOIN categories c ON c.id=pc.category_id WHERE pc.post_id=p.id AND (starts_with(lower(c.name),lower($1)) OR c.description ILIKE '%'||$1||'%'))
	   OR EXISTS(SELECT 1 FROM post_tags pt JOIN tag_aliases ta ON ta.tag_id=pt.tag_id WHERE pt.post_id=p.id AND ta.alias ILIKE '%'||$1||'%'))
	 AND p.organization_id=$11 AND ($2='' OR p.kind=$2) AND ($3='' OR p.status=$3)
	 AND ($4='' OR p.author_id=NULLIF($4,'')::uuid)
	 AND ($5='' OR EXISTS(SELECT 1 FROM post_tags pt WHERE pt.post_id=p.id AND pt.tag_id=NULLIF($5,'')::uuid))
	 AND ($6='' OR EXISTS(SELECT 1 FROM post_categories pc WHERE pc.post_id=p.id AND pc.category_id=NULLIF($6,'')::uuid)) AND ($7='' OR p.created_at>=NULLIF($7,'')::date) AND ($8='' OR p.created_at<NULLIF($8,'')::date+interval '1 day')
	 UNION ALL
	 SELECT q.id::text,'answer',q.title,left(a.body_markdown,240),u.display_name,q.status,a.created_at,q.updated_at,
	 ts_rank_cd(to_tsvector('english',a.body_markdown),query.q),
	 (SELECT count(*)::int FROM votes v WHERE v.target_type='answer' AND v.target_id=a.id),false
	 FROM answers a JOIN posts q ON q.id=a.question_id JOIN users u ON u.id=a.author_id CROSS JOIN query
	 WHERE NOT a.hidden AND q.deleted_at IS NULL AND NOT q.hidden AND (q.kind='question' OR q.status='published')
	 AND u.organization_id=$11
	 AND (to_tsvector('english',a.body_markdown) @@ query.q OR starts_with(lower(a.body_markdown),lower($1)) OR starts_with(lower(q.title),lower($1)) OR starts_with(lower(u.display_name),lower($1)))
	 AND q.organization_id=$11 AND ($2='' OR $2='question' OR $2='answer') AND ($3='' OR q.status=$3) AND ($4='' OR a.author_id=NULLIF($4,'')::uuid)
	 AND ($5='' OR EXISTS(SELECT 1 FROM post_tags pt WHERE pt.post_id=q.id AND pt.tag_id=NULLIF($5,'')::uuid))
	 AND ($6='' OR EXISTS(SELECT 1 FROM post_categories pc WHERE pc.post_id=q.id AND pc.category_id=NULLIF($6,'')::uuid)) AND ($7='' OR a.created_at>=NULLIF($7,'')::date) AND ($8='' OR a.created_at<NULLIF($8,'')::date+interval '1 day')
	)
	SELECT id,kind,title,snippet,author_name,status,updated_at,count(*) OVER(),rank FROM matching
	ORDER BY `+order+` LIMIT $9 OFFSET $10`, f.Query, f.Kind, f.Status, f.AuthorID, f.TagID, f.CategoryID, f.From, f.To, f.Limit, f.Offset, u.OrganizationID)
	if err != nil {
		return nil, translate(err)
	}
	defer rows.Close()
	out := []domain.SearchResult{}
	var total int
	for rows.Next() {
		var r domain.SearchResult
		var rank float32
		if err = rows.Scan(&r.ID, &r.Kind, &r.Title, &r.Snippet, &r.AuthorName, &r.Status, &r.UpdatedAt, &total, &rank); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	rows.Close()
	hash := sha256.Sum256([]byte(strings.ToLower(strings.TrimSpace(f.Query))))
	_, _ = s.Pool.Exec(ctx, `INSERT INTO search_events(query_hash,result_count,organization_id) VALUES(decode($1,'hex'),$2,$3)`, hex.EncodeToString(hash[:]), total, u.OrganizationID)
	return out, nil
}
