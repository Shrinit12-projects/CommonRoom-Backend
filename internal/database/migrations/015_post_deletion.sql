ALTER TABLE posts ADD COLUMN deleted_at timestamptz;
CREATE INDEX posts_active_feed ON posts(organization_id,kind,status,created_at DESC,id) WHERE deleted_at IS NULL AND NOT hidden;
