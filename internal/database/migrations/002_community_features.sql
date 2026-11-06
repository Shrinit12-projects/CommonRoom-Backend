ALTER TABLE users ADD COLUMN bio text NOT NULL DEFAULT '' CHECK (length(bio) <= 1200);
ALTER TABLE tags ADD COLUMN merged_into uuid REFERENCES tags(id);
ALTER TABLE users ADD COLUMN last_active_at timestamptz;
ALTER TABLE answers ADD COLUMN hidden boolean NOT NULL DEFAULT false;
CREATE INDEX answers_visible ON answers(question_id,created_at) WHERE NOT hidden;
CREATE TABLE categories (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
 name text NOT NULL CHECK (length(name) BETWEEN 2 AND 80),
 slug text NOT NULL UNIQUE CHECK (slug ~ '^[a-z0-9]+(-[a-z0-9]+)*$'),
 description text NOT NULL DEFAULT '' CHECK (length(description) <= 2000),
 active boolean NOT NULL DEFAULT true,
 created_at timestamptz NOT NULL DEFAULT now(),
 updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX categories_name_unique ON categories(lower(name));
ALTER TABLE tags ADD COLUMN category_id uuid REFERENCES categories(id);
ALTER TABLE posts ADD COLUMN category_id uuid REFERENCES categories(id);
ALTER TABLE posts ADD COLUMN featured boolean NOT NULL DEFAULT false;
ALTER TABLE posts ADD COLUMN verified boolean NOT NULL DEFAULT false;
ALTER TABLE posts ADD COLUMN verified_by uuid REFERENCES users(id);
ALTER TABLE posts ADD COLUMN verified_at timestamptz;
ALTER TABLE posts ADD COLUMN duplicate_of uuid REFERENCES posts(id);
ALTER TABLE posts DROP CONSTRAINT posts_check;
ALTER TABLE posts ADD CONSTRAINT posts_status_kind CHECK (
 (kind='blog' AND status IN ('draft','published') AND accepted_answer_id IS NULL)
 OR (kind='question' AND status IN ('open','solved','closed','duplicate'))
);
ALTER TABLE posts ADD CONSTRAINT duplicate_target_question CHECK (
 (status='duplicate' AND duplicate_of IS NOT NULL) OR (status<>'duplicate' AND duplicate_of IS NULL)
);
ALTER TABLE posts ADD CONSTRAINT duplicate_target_fk FOREIGN KEY(duplicate_of,kind) REFERENCES posts(id,kind);
ALTER TABLE posts ADD CONSTRAINT duplicate_not_self CHECK (duplicate_of IS NULL OR duplicate_of<>id);
ALTER TABLE posts ADD COLUMN featured_by uuid REFERENCES users(id);
CREATE INDEX posts_category ON posts(category_id,created_at DESC) WHERE NOT hidden;
CREATE TABLE tag_aliases (
 alias text PRIMARY KEY,
 tag_id uuid NOT NULL REFERENCES tags(id),
 created_at timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX tag_aliases_normalized ON tag_aliases(lower(alias));

CREATE TABLE comments (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
 target_type text NOT NULL CHECK (target_type IN ('post','answer')),
 target_id uuid NOT NULL,
 author_id uuid NOT NULL REFERENCES users(id),
 body text NOT NULL CHECK (length(body) BETWEEN 2 AND 4000),
 hidden boolean NOT NULL DEFAULT false,
 created_at timestamptz NOT NULL DEFAULT now(),
 updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX comments_target ON comments(target_type,target_id,created_at) WHERE NOT hidden;
CREATE INDEX comments_author ON comments(author_id);
CREATE TABLE votes (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
 target_type text NOT NULL CHECK (target_type IN ('post','answer')),
 target_id uuid NOT NULL,
 voter_id uuid NOT NULL REFERENCES users(id),
 helpful boolean NOT NULL DEFAULT true,
 created_at timestamptz NOT NULL DEFAULT now(),
 UNIQUE(target_type,target_id,voter_id)
);
CREATE INDEX votes_target ON votes(target_type,target_id);
CREATE TABLE bookmarks (
 user_id uuid NOT NULL REFERENCES users(id),
 post_id uuid NOT NULL REFERENCES posts(id),
 created_at timestamptz NOT NULL DEFAULT now(),
 PRIMARY KEY(user_id,post_id)
);
CREATE TABLE tag_follows (
 user_id uuid NOT NULL REFERENCES users(id),
 tag_id uuid NOT NULL REFERENCES tags(id),
 created_at timestamptz NOT NULL DEFAULT now(),
 PRIMARY KEY(user_id,tag_id)
);

CREATE TABLE notifications (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
 recipient_id uuid NOT NULL REFERENCES users(id),
 actor_id uuid REFERENCES users(id),
 event_type text NOT NULL,
 title text NOT NULL CHECK (length(title) BETWEEN 1 AND 200),
 target_type text NOT NULL,
 target_id uuid NOT NULL,
 read_at timestamptz,
 created_at timestamptz NOT NULL DEFAULT now(),
 dedupe_key text UNIQUE
);
CREATE INDEX notifications_recipient ON notifications(recipient_id,created_at DESC);
CREATE TABLE notification_preferences (
 user_id uuid PRIMARY KEY REFERENCES users(id),
 in_app_enabled boolean NOT NULL DEFAULT true,
 muted_tags uuid[] NOT NULL DEFAULT '{}',
 updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE reward_rules (
 event_type text PRIMARY KEY,
 points integer NOT NULL CHECK (points BETWEEN 0 AND 10000),
 enabled boolean NOT NULL DEFAULT true,
 daily_cap integer NOT NULL DEFAULT 100 CHECK (daily_cap BETWEEN 0 AND 100000),
 version integer NOT NULL DEFAULT 1,
 updated_by uuid REFERENCES users(id),
 updated_at timestamptz NOT NULL DEFAULT now()
);
INSERT INTO reward_rules(event_type,points,daily_cap) VALUES
 ('blog_published',50,100),('question_created',3,30),('answer_created',5,100),
 ('helpful_vote_blog',3,100),('helpful_vote_answer',2,100),('blog_bookmarked',1,100),
 ('answer_accepted',20,100),('featured_blog',30,100)
 ON CONFLICT DO NOTHING;
CREATE TABLE points_ledger (
 id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 user_id uuid NOT NULL REFERENCES users(id),
 event_type text NOT NULL,
 delta integer NOT NULL CHECK (delta <> 0),
 source_type text NOT NULL,
 source_id uuid,
 idempotency_key text NOT NULL UNIQUE,
 rule_version integer NOT NULL,
 reversal_of bigint REFERENCES points_ledger(id),
 created_at timestamptz NOT NULL DEFAULT now(),
 UNIQUE(reversal_of)
);
CREATE INDEX points_ledger_user_date ON points_ledger(user_id,created_at DESC);
CREATE TABLE badges (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
 slug text NOT NULL UNIQUE,
 name text NOT NULL,
 description text NOT NULL,
 threshold_points integer NOT NULL CHECK (threshold_points >= 0),
 active boolean NOT NULL DEFAULT true
);
INSERT INTO badges(slug,name,description,threshold_points) VALUES
 ('first-steps','First steps','Your first contribution',1),
 ('helpful-contributor','Helpful contributor','Earned 50 contribution points',50),
 ('knowledge-builder','Knowledge builder','Earned 250 contribution points',250),
 ('community-expert','Community expert','Earned 1000 contribution points',1000)
 ON CONFLICT DO NOTHING;
CREATE TABLE user_badges (
 user_id uuid NOT NULL REFERENCES users(id),
 badge_id uuid NOT NULL REFERENCES badges(id),
 awarded_at timestamptz NOT NULL DEFAULT now(),
 PRIMARY KEY(user_id,badge_id)
);

CREATE TABLE reports (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
 reporter_id uuid NOT NULL REFERENCES users(id),
 target_type text NOT NULL CHECK (target_type IN ('post','answer','comment')),
 target_id uuid NOT NULL,
 reason text NOT NULL CHECK (reason IN ('spam','abuse','confidential_data','incorrect','duplicate','other')),
 details text NOT NULL DEFAULT '' CHECK (length(details) <= 2000),
 status text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','resolved','dismissed')),
 reviewer_id uuid REFERENCES users(id),
 resolution_note text NOT NULL DEFAULT '',
 created_at timestamptz NOT NULL DEFAULT now(),
 reviewed_at timestamptz
);
CREATE UNIQUE INDEX one_open_report_per_reporter ON reports(reporter_id,target_type,target_id) WHERE status='pending';
CREATE INDEX reports_queue ON reports(status,created_at);
CREATE TABLE search_events (
 id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 query_hash bytea NOT NULL,
 result_count integer NOT NULL CHECK (result_count >= 0),
 created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX search_events_date ON search_events(created_at);
CREATE TABLE media_files (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
 owner_id uuid NOT NULL REFERENCES users(id),
 post_id uuid NOT NULL REFERENCES posts(id),
 filename text NOT NULL,
 content_type text NOT NULL CHECK (content_type IN ('image/png','image/jpeg','image/gif')),
 byte_size integer NOT NULL CHECK (byte_size BETWEEN 1 AND 5242880),
 width integer NOT NULL CHECK (width BETWEEN 1 AND 12000),
 height integer NOT NULL CHECK (height BETWEEN 1 AND 12000),
 data bytea NOT NULL,
 created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX media_post ON media_files(post_id,created_at);

CREATE TABLE content_editors (
 post_id uuid NOT NULL REFERENCES posts(id),
 user_id uuid NOT NULL REFERENCES users(id),
 granted_by uuid NOT NULL REFERENCES users(id),
 created_at timestamptz NOT NULL DEFAULT now(),
 PRIMARY KEY(post_id,user_id)
);
CREATE TABLE redemption_requests (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
 user_id uuid NOT NULL REFERENCES users(id),
 points_cost integer NOT NULL CHECK(points_cost>0),
 reward_description text NOT NULL,
 status text NOT NULL DEFAULT 'pending' CHECK(status IN ('pending','approved','rejected','fulfilled')),
 reviewed_by uuid REFERENCES users(id),
 created_at timestamptz NOT NULL DEFAULT now(),
 reviewed_at timestamptz
);
ALTER TABLE reward_rules ADD COLUMN reward_kind text NOT NULL DEFAULT 'recognition' CHECK (reward_kind IN ('recognition','credit'));
