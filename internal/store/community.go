package store

import (
	"context"
	"errors"

	"company-community/backend/internal/domain"
	"github.com/jackc/pgx/v5"
)

func notify(ctx context.Context, tx pgx.Tx, recipient, actor, event, title, targetType, targetID, dedupe string) error {
	if recipient == "" || recipient == actor {
		return nil
	}
	var actorID any
	if actor != "" {
		actorID = actor
	}
	var enabled bool
	if err := tx.QueryRow(ctx, `SELECT COALESCE((SELECT in_app_enabled FROM notification_preferences WHERE user_id=$1),true)`, recipient).Scan(&enabled); err != nil {
		return err
	}
	if !enabled {
		return nil
	}
	if _, err := tx.Exec(ctx, `INSERT INTO notifications(recipient_id,actor_id,event_type,title,target_type,target_id,dedupe_key) VALUES($1,$2,$3,$4,$5,$6,NULLIF($7,'')) ON CONFLICT(dedupe_key) DO NOTHING`, recipient, actorID, event, title, targetType, targetID, dedupe); err != nil {
		return err
	}
	return nil
}
func followNotifications(ctx context.Context, tx pgx.Tx, actor, id string, tagIDs []string, title, targetType string) error {
	for _, tagID := range tagIDs {
		rows, err := tx.Query(ctx, `SELECT f.user_id::text FROM tag_follows f LEFT JOIN notification_preferences np ON np.user_id=f.user_id WHERE f.tag_id=$1 AND f.user_id<>$2 AND COALESCE(np.in_app_enabled,true) AND NOT ($1=ANY(COALESCE(np.muted_tags,'{}'::uuid[])))`, tagID, actor)
		if err != nil {
			return err
		}
		users := []string{}
		for rows.Next() {
			var u string
			if err = rows.Scan(&u); err != nil {
				rows.Close()
				return err
			}
			users = append(users, u)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		for _, u := range users {
			if err = notify(ctx, tx, u, actor, "tagged_content", title, targetType, id, "tag:"+tagID+":"+id+":"+u); err != nil {
				return err
			}
		}
	}
	return nil
}
func award(ctx context.Context, tx pgx.Tx, uid, event, sourceType string, sourceID any, key string) error {
	if uid == "" || key == "" {
		return nil
	}
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, uid); err != nil {
		return err
	}
	var rule domain.RewardRule
	if err := tx.QueryRow(ctx, `SELECT event_type,points,enabled,daily_cap,version,reward_kind FROM reward_rules WHERE event_type=$1 AND organization_id=(SELECT organization_id FROM users WHERE id=$2)`, event, uid).Scan(&rule.EventType, &rule.Points, &rule.Enabled, &rule.DailyCap, &rule.Version, &rule.RewardKind); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		return err
	}
	if !rule.Enabled || rule.Points <= 0 {
		return nil
	}
	var today int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM points_ledger WHERE user_id=$1 AND event_type=$2 AND delta>0 AND created_at>=date_trunc('day',now())`, uid, event).Scan(&today); err != nil {
		return err
	}
	if today >= rule.DailyCap {
		return nil
	}
	var inserted int64
	if err := tx.QueryRow(ctx, `INSERT INTO points_ledger(user_id,event_type,delta,source_type,source_id,idempotency_key,rule_version) VALUES($1,$2,$3,$4,$5,$6,$7) ON CONFLICT(idempotency_key) DO NOTHING RETURNING id`, uid, event, rule.Points, sourceType, sourceID, key, rule.Version).Scan(&inserted); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		return err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO credits_ledger(user_id,event_type,delta,source_type,source_id,idempotency_key,rule_version) VALUES($1,$2,$3::numeric*1.5,$4,$5,$6,$7) ON CONFLICT(idempotency_key) DO NOTHING`, uid, event, rule.Points, sourceType, sourceID, key+":credits", rule.Version); err != nil {
		return err
	}
	rows, err := tx.Query(ctx, `WITH inserted AS (
	 INSERT INTO user_badges(user_id,badge_id)
	 SELECT $1,id FROM badges WHERE active AND threshold_points<=(SELECT COALESCE(sum(delta),0) FROM points_ledger WHERE user_id=$1)
	 ON CONFLICT DO NOTHING RETURNING badge_id
	)
	SELECT inserted.badge_id::text,b.name FROM inserted JOIN badges b ON b.id=inserted.badge_id`, uid)
	if err != nil {
		return err
	}
	type earned struct{ id, name string }
	newBadges := []earned{}
	for rows.Next() {
		var b earned
		if err = rows.Scan(&b.id, &b.name); err != nil {
			rows.Close()
			return err
		}
		newBadges = append(newBadges, b)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, b := range newBadges {
		if err = notify(ctx, tx, uid, "", "badge_earned", "You earned the "+b.name+" badge", "user_badge", b.id, "badge:"+uid+":"+b.id); err != nil {
			return err
		}
	}
	return nil
}
func reverseAward(ctx context.Context, tx pgx.Tx, uid, event, sourceType string, sourceID any, key string) error {
	var pointID int64
	var points, version int
	err := tx.QueryRow(ctx, `SELECT id,delta,rule_version FROM points_ledger WHERE idempotency_key=$1 AND delta>0`, key).Scan(&pointID, &points, &version)
	if err == nil {
		if _, err = tx.Exec(ctx, `INSERT INTO points_ledger(user_id,event_type,delta,source_type,source_id,idempotency_key,rule_version,reversal_of) VALUES($1,$2,$3,$4,$5,$6,$7,$8) ON CONFLICT(idempotency_key) DO NOTHING`, uid, event, -points, sourceType, sourceID, key+":reversed", version, pointID); err != nil {
			return err
		}
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	var creditID int64
	var credits float64
	err = tx.QueryRow(ctx, `SELECT id,delta::float8,rule_version FROM credits_ledger WHERE idempotency_key=$1 AND delta>0`, key+":credits").Scan(&creditID, &credits, &version)
	if errors.Is(err, pgx.ErrNoRows) {
		// Reverse awards written by the earlier credit-only reward implementation.
		err = tx.QueryRow(ctx, `SELECT id,delta::float8,rule_version FROM credits_ledger WHERE idempotency_key=$1 AND delta>0`, key).Scan(&creditID, &credits, &version)
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO credits_ledger(user_id,event_type,delta,source_type,source_id,idempotency_key,rule_version,reversal_of) VALUES($1,$2,-$3,$4,$5,$6,$7,$8) ON CONFLICT(idempotency_key) DO NOTHING`, uid, event, credits, sourceType, sourceID, key+":credits:reversed", version, creditID)
	return err
}
func (s *Store) Categories(ctx context.Context, orgID string, all bool, limit, offset int) ([]domain.Category, error) {
	rows, err := s.Pool.Query(ctx, `SELECT id::text,name,slug,description,active FROM categories WHERE organization_id=$1 AND (active OR $2) ORDER BY name,id LIMIT $3 OFFSET $4`, orgID, all, limit, offset)
	if err != nil {
		return nil, translate(err)
	}
	defer rows.Close()
	out := []domain.Category{}
	for rows.Next() {
		var c domain.Category
		if err = rows.Scan(&c.ID, &c.Name, &c.Slug, &c.Description, &c.Active); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}
func (s *Store) SaveCategory(ctx context.Context, u domain.User, c domain.Category) (domain.Category, error) {
	var out domain.Category
	err := s.transaction(ctx, func(tx pgx.Tx) error {
		err := tx.QueryRow(ctx, `INSERT INTO categories(name,slug,description,organization_id) VALUES($1,$2,$3,$4) RETURNING id::text,name,slug,description,active`, c.Name, c.Slug, c.Description, u.OrganizationID).Scan(&out.ID, &out.Name, &out.Slug, &out.Description, &out.Active)
		if err != nil {
			return err
		}
		return audit(ctx, tx, u, "category.created", out.ID, map[string]string{"slug": out.Slug})
	})
	return out, err
}
func (s *Store) SetCategory(ctx context.Context, u domain.User, id string, active bool) (domain.Category, error) {
	var out domain.Category
	err := s.transaction(ctx, func(tx pgx.Tx) error {
		err := tx.QueryRow(ctx, `UPDATE categories SET active=$2,updated_at=now() WHERE id=$1 AND organization_id=$3 RETURNING id::text,name,slug,description,active`, id, active, u.OrganizationID).Scan(&out.ID, &out.Name, &out.Slug, &out.Description, &out.Active)
		if err != nil {
			return err
		}
		return audit(ctx, tx, u, "category.status", id, map[string]bool{"active": active})
	})
	return out, err
}
func (s *Store) Profile(ctx context.Context, requester domain.User, id string) (domain.Profile, error) {
	var p domain.Profile
	err := s.Pool.QueryRow(ctx, `SELECT id::text,display_name,bio FROM users WHERE id=$1 AND organization_id=$4 AND (active OR id=$2::uuid OR $3::boolean)`, id, requester.ID, requester.Moderator(), requester.OrganizationID).Scan(&p.ID, &p.DisplayName, &p.Bio)
	if err != nil {
		return p, translate(err)
	}
	p.Badges = []string{}
	p.Tags = []string{}
	p.Posts = []domain.Post{}
	if err = s.Pool.QueryRow(ctx, `SELECT COALESCE(sum(delta),0) FROM points_ledger WHERE user_id=$1`, id).Scan(&p.Points); err != nil {
		return p, err
	}
	if err = s.Pool.QueryRow(ctx, `SELECT count(*) FROM answers a JOIN posts q ON q.id=a.question_id WHERE a.author_id=$1 AND NOT a.hidden AND NOT q.hidden AND q.deleted_at IS NULL`, id).Scan(&p.Answers); err != nil {
		return p, err
	}
	rows, err := s.Pool.Query(ctx, `SELECT name FROM user_badges ub JOIN badges b ON b.id=ub.badge_id WHERE ub.user_id=$1 ORDER BY ub.awarded_at`, id)
	if err != nil {
		return p, err
	}
	for rows.Next() {
		var name string
		if err = rows.Scan(&name); err != nil {
			rows.Close()
			return p, err
		}
		p.Badges = append(p.Badges, name)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return p, err
	}
	rows, err = s.Pool.Query(ctx, `SELECT t.name FROM tags t JOIN post_tags pt ON pt.tag_id=t.id JOIN posts po ON po.id=pt.post_id WHERE po.author_id=$1 AND NOT po.hidden AND po.deleted_at IS NULL AND (po.kind='question' OR po.status='published') GROUP BY t.id ORDER BY count(*) DESC,t.name LIMIT 8`, id)
	if err != nil {
		return p, err
	}
	for rows.Next() {
		var name string
		if err = rows.Scan(&name); err != nil {
			rows.Close()
			return p, err
		}
		p.Tags = append(p.Tags, name)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return p, err
	}
	rows, err = s.Pool.Query(ctx, `SELECT `+postColumns+` FROM posts p JOIN users u ON u.id=p.author_id WHERE p.author_id=$1 AND p.kind='blog' AND p.status='published' AND NOT p.hidden AND p.deleted_at IS NULL ORDER BY p.created_at DESC LIMIT 20`, id)
	if err != nil {
		return p, err
	}
	defer rows.Close()
	for rows.Next() {
		post, err := scanPost(rows)
		if err != nil {
			return p, err
		}
		p.Posts = append(p.Posts, post)
	}
	return p, rows.Err()
}
func (s *Store) UpdateProfile(ctx context.Context, u domain.User, name, bio string) (domain.Profile, error) {
	var p domain.Profile
	err := s.transaction(ctx, func(tx pgx.Tx) error {
		err := tx.QueryRow(ctx, `UPDATE users SET display_name=$2,bio=$3,updated_at=now() WHERE id=$1 RETURNING id::text,display_name,bio`, u.ID, name, bio).Scan(&p.ID, &p.DisplayName, &p.Bio)
		if err != nil {
			return err
		}
		return audit(ctx, tx, u, "profile.updated", u.ID, map[string]any{})
	})
	if err != nil {
		return p, err
	}
	return s.Profile(ctx, u, u.ID)
}
func (s *Store) People(ctx context.Context, user domain.User, f domain.Filter) ([]domain.Profile, error) {
	rows, err := s.Pool.Query(ctx, `SELECT u.id::text,u.display_name,u.bio,COALESCE((SELECT sum(delta) FROM points_ledger l WHERE l.user_id=u.id),0) points FROM users u WHERE u.organization_id=$4 AND u.active AND ($1='' OR position(lower($1) in lower(u.display_name))>0 OR position(lower($1) in lower(u.bio))>0) ORDER BY points DESC,u.display_name,u.id LIMIT $2 OFFSET $3`, f.Query, f.Limit, f.Offset, user.OrganizationID)
	if err != nil {
		return nil, translate(err)
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
func visibleTarget(ctx context.Context, tx pgx.Tx, u domain.User, targetType, targetID string) (domain.Post, string, error) {
	if targetType == "post" {
		p, err := lockedPost(ctx, tx, targetID)
		if err != nil {
			return p, "", err
		}
		if !u.CanRead(p) {
			return p, "", domain.ErrNotFound
		}
		return p, p.AuthorID, nil
	}
	var questionID, authorID string
	var hidden bool
	err := tx.QueryRow(ctx, `SELECT a.question_id::text,a.author_id::text,a.hidden FROM answers a JOIN posts p ON p.id=a.question_id WHERE a.id=$1 AND p.organization_id=$2`, targetID, u.OrganizationID).Scan(&questionID, &authorID, &hidden)
	if err != nil {
		return domain.Post{}, "", translate(err)
	}
	if hidden {
		return domain.Post{}, "", domain.ErrNotFound
	}
	p, err := lockedPost(ctx, tx, questionID)
	if err != nil {
		return p, "", err
	}
	if !u.CanRead(p) {
		return p, "", domain.ErrNotFound
	}
	return p, authorID, nil
}
func (s *Store) Comment(ctx context.Context, u domain.User, kind, id, body string) (domain.Comment, error) {
	return s.comment(ctx, u, kind, id, body, "")
}
func (s *Store) ReplyComment(ctx context.Context, u domain.User, parentID, body string) (domain.Comment, error) {
	var kind, target string
	if err := s.Pool.QueryRow(ctx, `SELECT c.target_type,c.target_id::text FROM comments c JOIN posts p ON (c.target_type='post' AND p.id=c.target_id) OR (c.target_type='answer' AND p.id=(SELECT question_id FROM answers WHERE id=c.target_id)) WHERE c.id=$1 AND NOT c.hidden AND p.organization_id=$2`, parentID, u.OrganizationID).Scan(&kind, &target); err != nil {
		return domain.Comment{}, translate(err)
	}
	return s.comment(ctx, u, kind, target, body, parentID)
}
func (s *Store) comment(ctx context.Context, u domain.User, kind, id, body, parentID string) (domain.Comment, error) {
	var c domain.Comment
	err := s.transaction(ctx, func(tx pgx.Tx) error {
		p, owner, err := visibleTarget(ctx, tx, u, kind, id)
		if err != nil {
			return err
		}
		if p.Hidden {
			return domain.ErrNotFound
		}
		err = tx.QueryRow(ctx, `INSERT INTO comments(target_type,target_id,author_id,body,parent_comment_id) VALUES($1,$2,$3,$4,NULLIF($5,'')::uuid) RETURNING id::text,parent_comment_id::text,target_type,target_id::text,author_id::text,$6,body,hidden,created_at`, kind, id, u.ID, body, parentID, u.DisplayName).Scan(&c.ID, &c.ParentID, &c.TargetType, &c.TargetID, &c.AuthorID, &c.AuthorName, &c.Body, &c.Hidden, &c.CreatedAt)
		if err != nil {
			return err
		}
		return notify(ctx, tx, owner, u.ID, "new_comment", "New comment", "post", p.ID, "comment:"+c.ID)
	})
	return c, err
}
func (s *Store) Comments(ctx context.Context, u domain.User, kind, id string, limit, offset int) ([]domain.Comment, error) {
	var questionID string
	if kind == "post" {
		p, err := s.GetPost(ctx, id)
		if err != nil {
			return nil, err
		}
		if !u.CanRead(p) {
			return nil, domain.ErrNotFound
		}
		questionID = id
	} else {
		if err := s.Pool.QueryRow(ctx, `SELECT question_id::text FROM answers WHERE id=$1 AND NOT hidden`, id).Scan(&questionID); err != nil {
			return nil, translate(err)
		}
		p, err := s.GetPost(ctx, questionID)
		if err != nil || !u.CanRead(p) {
			return nil, domain.ErrNotFound
		}
	}
	rows, err := s.Pool.Query(ctx, `SELECT c.id::text,c.parent_comment_id::text,c.target_type,c.target_id::text,c.author_id::text,u.display_name,c.body,c.hidden,c.created_at FROM comments c JOIN users u ON u.id=c.author_id JOIN posts p ON (c.target_type='post' AND p.id=c.target_id) OR (c.target_type='answer' AND p.id=(SELECT a.question_id FROM answers a WHERE a.id=c.target_id)) WHERE c.target_type=$1 AND c.target_id=$2 AND p.organization_id=$6 AND p.deleted_at IS NULL AND u.organization_id=$6 AND (NOT c.hidden OR $5::boolean) ORDER BY c.created_at,c.id LIMIT $3 OFFSET $4`, kind, id, limit, offset, u.Moderator(), u.OrganizationID)
	if err != nil {
		return nil, translate(err)
	}
	defer rows.Close()
	out := []domain.Comment{}
	for rows.Next() {
		var c domain.Comment
		if err = rows.Scan(&c.ID, &c.ParentID, &c.TargetType, &c.TargetID, &c.AuthorID, &c.AuthorName, &c.Body, &c.Hidden, &c.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	_ = questionID
	return out, rows.Err()
}
func (s *Store) SetCommentHidden(ctx context.Context, u domain.User, id string, hidden bool) (domain.Comment, error) {
	var c domain.Comment
	err := s.transaction(ctx, func(tx pgx.Tx) error {
		err := tx.QueryRow(ctx, `UPDATE comments c SET hidden=$2,updated_at=now() FROM users u WHERE c.id=$1 AND u.id=c.author_id AND u.organization_id=$3 AND ((c.target_type='post' AND EXISTS(SELECT 1 FROM posts p WHERE p.id=c.target_id AND p.organization_id=$3)) OR (c.target_type='answer' AND EXISTS(SELECT 1 FROM answers a JOIN posts p ON p.id=a.question_id WHERE a.id=c.target_id AND p.organization_id=$3))) RETURNING c.id::text,c.target_type,c.target_id::text,c.author_id::text,u.display_name,c.body,c.hidden,c.created_at`, id, hidden, u.OrganizationID).Scan(&c.ID, &c.TargetType, &c.TargetID, &c.AuthorID, &c.AuthorName, &c.Body, &c.Hidden, &c.CreatedAt)
		if err != nil {
			return err
		}
		return audit(ctx, tx, u, "comment.visibility", id, map[string]bool{"hidden": hidden})
	})
	return c, err
}
func (s *Store) Vote(ctx context.Context, u domain.User, kind, id string, helpful bool) (domain.VoteSummary, error) {
	var result domain.VoteSummary
	err := s.transaction(ctx, func(tx pgx.Tx) error {
		p, owner, err := visibleTarget(ctx, tx, u, kind, id)
		if err != nil {
			return err
		}
		if p.Hidden || p.Kind == "blog" && p.Status != "published" || p.Kind == "question" && p.Status == "closed" || owner == u.ID {
			return domain.ErrForbidden
		}
		event := "helpful_vote_blog"
		if kind == "answer" {
			event = "helpful_vote_answer"
		}
		key := "vote:" + kind + ":" + id + ":" + u.ID
		if helpful {
			var inserted string
			err = tx.QueryRow(ctx, `INSERT INTO votes(target_type,target_id,voter_id,helpful) VALUES($1,$2,$3,true) ON CONFLICT(target_type,target_id,voter_id) DO NOTHING RETURNING id::text`, kind, id, u.ID).Scan(&inserted)
			if err != nil && !errors.Is(err, pgx.ErrNoRows) {
				return err
			}
			if err == nil {
				if err = award(ctx, tx, owner, event, kind, id, key); err != nil {
					return err
				}
				if err = notify(ctx, tx, owner, u.ID, "helpful_vote", "Your contribution received a helpful vote", kind, id, "helpful:"+key); err != nil {
					return err
				}
			}
		} else {
			var existed bool
			if err = tx.QueryRow(ctx, `DELETE FROM votes WHERE target_type=$1 AND target_id=$2 AND voter_id=$3 RETURNING true`, kind, id, u.ID).Scan(&existed); err != nil && !errors.Is(err, pgx.ErrNoRows) {
				return err
			}
			if existed {
				if err = reverseAward(ctx, tx, owner, event+"_reversed", kind, id, key); err != nil {
					return err
				}
			}
		}
		return tx.QueryRow(ctx, `SELECT count(*) FILTER (WHERE helpful),COALESCE(bool_or(voter_id=$3),false) FROM votes WHERE target_type=$1 AND target_id=$2`, kind, id, u.ID).Scan(&result.Helpful, &result.Voted)
	})
	return result, err
}
func (s *Store) Bookmark(ctx context.Context, u domain.User, id string, on bool) error {
	return s.transaction(ctx, func(tx pgx.Tx) error {
		p, _, err := visibleTarget(ctx, tx, u, "post", id)
		if err != nil {
			return err
		}
		if p.Hidden || p.Kind == "blog" && p.Status != "published" {
			return domain.ErrNotFound
		}
		if on {
			if _, err = tx.Exec(ctx, `INSERT INTO bookmarks(user_id,post_id) VALUES($1,$2) ON CONFLICT DO NOTHING`, u.ID, id); err != nil {
				return err
			}
			if p.AuthorID != u.ID {
				if err = award(ctx, tx, p.AuthorID, "blog_bookmarked", "post", id, "bookmark:"+id+":"+u.ID); err != nil {
					return err
				}
			}
		} else {
			if _, err = tx.Exec(ctx, `DELETE FROM bookmarks WHERE user_id=$1 AND post_id=$2`, u.ID, id); err != nil {
				return err
			}
			if p.AuthorID != u.ID {
				if err = reverseAward(ctx, tx, p.AuthorID, "blog_bookmark_reversed", "post", id, "bookmark:"+id+":"+u.ID); err != nil {
					return err
				}
			}
		}
		return nil
	})
}
func (s *Store) Bookmarks(ctx context.Context, u domain.User, limit, offset int) ([]domain.Post, error) {
	rows, err := s.Pool.Query(ctx, `SELECT `+postColumns+` FROM bookmarks b JOIN posts p ON p.id=b.post_id JOIN users u ON u.id=p.author_id WHERE b.user_id=$1 AND p.organization_id=$4 AND p.deleted_at IS NULL AND NOT p.hidden AND (p.kind='question' OR p.status='published') ORDER BY b.created_at DESC LIMIT $2 OFFSET $3`, u.ID, limit, offset, u.OrganizationID)
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
func (s *Store) FollowTag(ctx context.Context, u domain.User, id string, on bool) error {
	if on {
		return s.transaction(ctx, func(tx pgx.Tx) error {
			var status string
			if err := tx.QueryRow(ctx, `SELECT status FROM tags WHERE id=$1 AND organization_id=$2 FOR SHARE`, id, u.OrganizationID).Scan(&status); err != nil {
				return err
			}
			if status != "active" {
				return domain.ErrNotFound
			}
			_, err := tx.Exec(ctx, `INSERT INTO tag_follows(user_id,tag_id) SELECT $1,t.id FROM tags t WHERE t.id=$2 AND t.organization_id=$3 ON CONFLICT DO NOTHING`, u.ID, id, u.OrganizationID)
			return err
		})
	}
	_, err := s.Pool.Exec(ctx, `DELETE FROM tag_follows f USING tags t WHERE f.user_id=$1 AND f.tag_id=$2 AND t.id=f.tag_id AND t.organization_id=$3`, u.ID, id, u.OrganizationID)
	return translate(err)
}
func (s *Store) FollowedTags(ctx context.Context, u domain.User, limit, offset int) ([]domain.Tag, error) {
	rows, err := s.Pool.Query(ctx, `SELECT `+tagColumns+` FROM tag_follows f JOIN tags t ON t.id=f.tag_id WHERE f.user_id=$1 AND t.organization_id=$4 ORDER BY f.created_at DESC LIMIT $2 OFFSET $3`, u.ID, limit, offset, u.OrganizationID)
	if err != nil {
		return nil, translate(err)
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
func (s *Store) Notifications(ctx context.Context, u domain.User, limit, offset int) ([]domain.Notification, error) {
	rows, err := s.Pool.Query(ctx, `SELECT id::text,event_type,title,target_type,target_id::text,read_at IS NOT NULL,created_at FROM notifications WHERE recipient_id=$1 ORDER BY created_at DESC LIMIT $2 OFFSET $3`, u.ID, limit, offset)
	if err != nil {
		return nil, translate(err)
	}
	defer rows.Close()
	out := []domain.Notification{}
	for rows.Next() {
		var n domain.Notification
		if err = rows.Scan(&n.ID, &n.EventType, &n.Title, &n.TargetType, &n.TargetID, &n.Read, &n.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}
func (s *Store) MarkNotification(ctx context.Context, u domain.User, id string) error {
	_, err := s.Pool.Exec(ctx, `UPDATE notifications SET read_at=COALESCE(read_at,now()) WHERE id=$1 AND recipient_id=$2`, id, u.ID)
	return translate(err)
}
func (s *Store) MarkAllNotifications(ctx context.Context, u domain.User) error {
	_, err := s.Pool.Exec(ctx, `UPDATE notifications SET read_at=now() WHERE recipient_id=$1 AND read_at IS NULL`, u.ID)
	return translate(err)
}
