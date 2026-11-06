CREATE UNIQUE INDEX users_org_email_unique ON users(organization_id,lower(email));
