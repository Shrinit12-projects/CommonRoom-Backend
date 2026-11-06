ALTER TABLE users ADD COLUMN deleted_at timestamptz;
CREATE INDEX users_org_deleted ON users(organization_id,deleted_at,display_name);
