CREATE TABLE users (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
 issuer text NOT NULL,
 subject text NOT NULL,
 email text NOT NULL,
 display_name text NOT NULL CHECK (length(display_name) BETWEEN 1 AND 100),
 role text NOT NULL DEFAULT 'employee' CHECK (role IN ('employee','moderator','admin')),
 active boolean NOT NULL DEFAULT true,
 created_at timestamptz NOT NULL DEFAULT now(),
 updated_at timestamptz NOT NULL DEFAULT now(),
 UNIQUE (issuer, subject)
);
CREATE TABLE tags (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
 name text NOT NULL,
 slug text NOT NULL UNIQUE CHECK (slug ~ '^[a-z0-9]+(-[a-z0-9]+)*$'),
 description text NOT NULL DEFAULT '',
 status text NOT NULL DEFAULT 'active' CHECK (status IN ('active','deprecated')),
 created_by uuid NOT NULL REFERENCES users(id),
 created_at timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX tags_name_unique ON tags(lower(name));
CREATE TABLE tag_proposals (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
 name text NOT NULL,
 slug text NOT NULL,
 description text NOT NULL DEFAULT '',
 reason text NOT NULL,
 proposer_id uuid NOT NULL REFERENCES users(id),
 status text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','approved','rejected')),
 reviewer_id uuid REFERENCES users(id),
 created_at timestamptz NOT NULL DEFAULT now(),
 reviewed_at timestamptz
);
CREATE UNIQUE INDEX pending_tag_slug_unique ON tag_proposals(slug) WHERE status = 'pending';
CREATE UNIQUE INDEX pending_tag_name_unique ON tag_proposals(lower(name)) WHERE status = 'pending';
CREATE TABLE posts (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
 kind text NOT NULL CHECK (kind IN ('blog','question')),
 author_id uuid NOT NULL REFERENCES users(id),
 title text NOT NULL CHECK (length(title) BETWEEN 3 AND 200),
 summary text NOT NULL DEFAULT '',
 body_markdown text NOT NULL,
 body_html text NOT NULL,
 status text NOT NULL,
 hidden boolean NOT NULL DEFAULT false,
 version integer NOT NULL DEFAULT 1 CHECK (version > 0),
 accepted_answer_id uuid,
 created_at timestamptz NOT NULL DEFAULT now(),
 updated_at timestamptz NOT NULL DEFAULT now(),
 CHECK ((kind='blog' AND status IN ('draft','published') AND accepted_answer_id IS NULL)
     OR (kind='question' AND status IN ('open','solved','closed'))),
 CHECK (status <> 'solved' OR accepted_answer_id IS NOT NULL),
 CHECK (accepted_answer_id IS NULL OR status IN ('solved','closed')),
 UNIQUE(id,kind),
 search_vector tsvector GENERATED ALWAYS AS (
  setweight(to_tsvector('english',title),'A') ||
  setweight(to_tsvector('english',summary),'B') ||
  setweight(to_tsvector('english',body_markdown),'C')
 ) STORED
);
CREATE INDEX posts_search ON posts USING gin(search_vector);
CREATE INDEX posts_feed ON posts(kind, status, created_at DESC, id) WHERE NOT hidden;
CREATE INDEX posts_author ON posts(author_id);
CREATE TABLE post_tags (
 post_id uuid NOT NULL REFERENCES posts(id),
 tag_id uuid NOT NULL REFERENCES tags(id),
 PRIMARY KEY(post_id,tag_id)
);
CREATE INDEX post_tags_reverse ON post_tags(tag_id,post_id);
CREATE TABLE answers (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
 question_id uuid NOT NULL,
 question_kind text NOT NULL DEFAULT 'question' CHECK (question_kind='question'),
 author_id uuid NOT NULL REFERENCES users(id),
 body_markdown text NOT NULL,
 body_html text NOT NULL,
 created_at timestamptz NOT NULL DEFAULT now(),
 FOREIGN KEY(question_id,question_kind) REFERENCES posts(id,kind),
 UNIQUE(question_id,id)
);
ALTER TABLE posts ADD CONSTRAINT accepted_answer_belongs_to_question
 FOREIGN KEY(id,accepted_answer_id) REFERENCES answers(question_id,id);
CREATE INDEX answers_question ON answers(question_id,created_at);
CREATE TABLE post_revisions (
 post_id uuid NOT NULL REFERENCES posts(id),
 version integer NOT NULL,
 actor_id uuid NOT NULL REFERENCES users(id),
 snapshot jsonb NOT NULL,
 created_at timestamptz NOT NULL DEFAULT now(),
 PRIMARY KEY(post_id,version)
);
CREATE TABLE audit_logs (
 id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 actor_id uuid NOT NULL REFERENCES users(id),
 action text NOT NULL,
 target_id uuid NOT NULL,
 details jsonb NOT NULL DEFAULT '{}',
 created_at timestamptz NOT NULL DEFAULT now()
);
