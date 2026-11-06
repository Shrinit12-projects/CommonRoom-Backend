ALTER TABLE audit_logs ADD COLUMN organization_id uuid REFERENCES organizations(id);
UPDATE audit_logs a SET organization_id=u.organization_id FROM users u WHERE u.id=a.actor_id;
CREATE INDEX audit_logs_org_recent ON audit_logs(organization_id,id DESC);
