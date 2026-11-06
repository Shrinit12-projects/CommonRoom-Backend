ALTER TABLE comments ADD COLUMN parent_comment_id uuid REFERENCES comments(id) ON DELETE CASCADE;
CREATE INDEX comments_parent ON comments(parent_comment_id,created_at,id) WHERE NOT hidden;

CREATE TABLE community_submissions (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
 kind text NOT NULL CHECK (kind IN ('feedback','appreciation')),
 mode text NOT NULL CHECK ((kind='feedback' AND mode IN ('request','give')) OR (kind='appreciation' AND mode='give')),
 author_id uuid NOT NULL REFERENCES users(id),
 message text NOT NULL CHECK (length(message) BETWEEN 10 AND 5000),
 status text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','approved','rejected')),
 reviewer_id uuid REFERENCES users(id),
 reviewed_at timestamptz,
 created_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE community_submission_people (
 submission_id uuid NOT NULL REFERENCES community_submissions(id) ON DELETE CASCADE,
 user_id uuid NOT NULL REFERENCES users(id),
 role text NOT NULL CHECK (role IN ('recipient','feedback_provider')),
 PRIMARY KEY(submission_id,user_id,role)
);
CREATE INDEX community_submissions_status ON community_submissions(kind,status,created_at DESC);
CREATE INDEX community_submission_people_user ON community_submission_people(user_id,submission_id);
