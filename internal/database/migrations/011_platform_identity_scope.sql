ALTER TABLE users ALTER COLUMN organization_id DROP NOT NULL;
UPDATE users SET organization_id=NULL WHERE role='platform_admin';
UPDATE users SET email='platform-admin@commonroom.local'
WHERE issuer='development' AND subject='local-platform-admin' AND role='platform_admin';
UPDATE local_credentials SET username='platform-admin@commonroom.local'
WHERE user_id=(SELECT id FROM users WHERE issuer='development' AND subject='local-platform-admin' AND role='platform_admin');
CREATE UNIQUE INDEX users_platform_email_unique ON users(lower(email)) WHERE organization_id IS NULL;
