CREATE TABLE organizations (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
 slug text NOT NULL UNIQUE CHECK (slug ~ '^[a-z0-9]+(-[a-z0-9]+)*$'),
 name text NOT NULL,
 email_domain text NOT NULL UNIQUE CHECK (email_domain=lower(email_domain) AND position('@' in email_domain)=0),
 created_at timestamptz NOT NULL DEFAULT now()
);
INSERT INTO organizations(slug,name,email_domain) VALUES
 ('mindorigin','Mindorigin','mindorigin.in'),
 ('firstock','Firstock','firstock.in');

ALTER TABLE users ADD COLUMN organization_id uuid REFERENCES organizations(id);
UPDATE users SET organization_id=(SELECT id FROM organizations WHERE email_domain=lower(split_part(email,'@',2)))
 WHERE position('@' in email)>0 AND EXISTS (SELECT 1 FROM organizations o WHERE o.email_domain=lower(split_part(users.email,'@',2)));
-- Existing single-tenant accounts and content stay together in Mindorigin.
UPDATE users SET organization_id=(SELECT id FROM organizations WHERE slug='mindorigin') WHERE organization_id IS NULL;
UPDATE users SET email='admin@mindorigin.in' WHERE issuer='development' AND subject='local-admin';
ALTER TABLE users ALTER COLUMN organization_id SET NOT NULL;
ALTER TABLE users ADD CONSTRAINT users_id_org_unique UNIQUE(id,organization_id);
CREATE INDEX users_org_active ON users(organization_id,active,display_name);

ALTER TABLE posts ADD COLUMN organization_id uuid REFERENCES organizations(id);
UPDATE posts p SET organization_id=u.organization_id FROM users u WHERE u.id=p.author_id;
ALTER TABLE posts ALTER COLUMN organization_id SET NOT NULL;
ALTER TABLE posts ADD CONSTRAINT posts_author_org_fk FOREIGN KEY(author_id,organization_id) REFERENCES users(id,organization_id);
CREATE INDEX posts_org_feed ON posts(organization_id,kind,status,created_at DESC,id) WHERE NOT hidden;

ALTER TABLE tags ADD COLUMN organization_id uuid REFERENCES organizations(id);
UPDATE tags SET organization_id=(SELECT id FROM organizations WHERE slug='mindorigin');
ALTER TABLE tags ALTER COLUMN organization_id SET NOT NULL;
ALTER TABLE tags DROP CONSTRAINT IF EXISTS tags_slug_key;
ALTER TABLE tags ADD CONSTRAINT tags_org_slug_unique UNIQUE(organization_id,slug);
DROP INDEX IF EXISTS tags_name_unique;
CREATE UNIQUE INDEX tags_org_name_unique ON tags(organization_id,lower(name));

ALTER TABLE categories ADD COLUMN organization_id uuid REFERENCES organizations(id);
UPDATE categories SET organization_id=(SELECT id FROM organizations WHERE slug='mindorigin');
ALTER TABLE categories ALTER COLUMN organization_id SET NOT NULL;
ALTER TABLE categories DROP CONSTRAINT IF EXISTS categories_slug_key;
ALTER TABLE categories ADD CONSTRAINT categories_org_slug_unique UNIQUE(organization_id,slug);
DROP INDEX IF EXISTS categories_name_unique;
CREATE UNIQUE INDEX categories_org_name_unique ON categories(organization_id,lower(name));

ALTER TABLE tag_aliases ADD COLUMN organization_id uuid REFERENCES organizations(id);
UPDATE tag_aliases ta SET organization_id=t.organization_id FROM tags t WHERE t.id=ta.tag_id;
ALTER TABLE tag_aliases ALTER COLUMN organization_id SET NOT NULL;
ALTER TABLE tag_aliases DROP CONSTRAINT tag_aliases_pkey;
ALTER TABLE tag_aliases ADD CONSTRAINT tag_aliases_org_alias_pk PRIMARY KEY(organization_id,alias);
DROP INDEX IF EXISTS tag_aliases_normalized;
CREATE UNIQUE INDEX tag_aliases_org_normalized ON tag_aliases(organization_id,lower(alias));

-- Give Firstock an independent copy of the starter taxonomy.
INSERT INTO categories(name,slug,description,organization_id)
 SELECT c.name,c.slug,c.description,o.id FROM categories c CROSS JOIN organizations o
 WHERE c.organization_id=(SELECT id FROM organizations WHERE slug='mindorigin') AND o.slug='firstock';
INSERT INTO tags(name,slug,description,status,created_by,category_id,organization_id)
 SELECT t.name,t.slug,t.description,t.status,t.created_by,fc.id,o.id
 FROM tags t CROSS JOIN organizations o
 LEFT JOIN categories mc ON mc.id=t.category_id
 LEFT JOIN categories fc ON fc.slug=mc.slug AND fc.organization_id=o.id
 WHERE t.organization_id=(SELECT id FROM organizations WHERE slug='mindorigin') AND o.slug='firstock';

ALTER TABLE tag_proposals ADD COLUMN organization_id uuid REFERENCES organizations(id);
UPDATE tag_proposals tp SET organization_id=u.organization_id FROM users u WHERE u.id=tp.proposer_id;
ALTER TABLE tag_proposals ALTER COLUMN organization_id SET NOT NULL;
DROP INDEX IF EXISTS pending_tag_slug_unique;
DROP INDEX IF EXISTS pending_tag_name_unique;
CREATE UNIQUE INDEX pending_tag_slug_unique ON tag_proposals(organization_id,slug) WHERE status='pending';
CREATE UNIQUE INDEX pending_tag_name_unique ON tag_proposals(organization_id,lower(name)) WHERE status='pending';
ALTER TABLE community_submissions ADD COLUMN organization_id uuid REFERENCES organizations(id);
UPDATE community_submissions cs SET organization_id=u.organization_id FROM users u WHERE u.id=cs.author_id;
ALTER TABLE community_submissions ALTER COLUMN organization_id SET NOT NULL;

ALTER TABLE search_events ADD COLUMN organization_id uuid REFERENCES organizations(id);
UPDATE search_events SET organization_id=(SELECT id FROM organizations WHERE slug='mindorigin');
ALTER TABLE search_events ALTER COLUMN organization_id SET NOT NULL;
CREATE INDEX search_events_org_date ON search_events(organization_id,created_at);

ALTER TABLE reward_rules ADD COLUMN organization_id uuid REFERENCES organizations(id);
UPDATE reward_rules SET organization_id=(SELECT id FROM organizations WHERE slug='mindorigin');
ALTER TABLE reward_rules ALTER COLUMN organization_id SET NOT NULL;
ALTER TABLE reward_rules DROP CONSTRAINT reward_rules_pkey;
ALTER TABLE reward_rules ADD CONSTRAINT reward_rules_org_event_pk PRIMARY KEY(organization_id,event_type);
INSERT INTO reward_rules(event_type,points,enabled,daily_cap,version,updated_by,updated_at,reward_kind,organization_id)
 SELECT event_type,points,enabled,daily_cap,version,updated_by,updated_at,reward_kind,(SELECT id FROM organizations WHERE slug='firstock') FROM reward_rules
 WHERE organization_id=(SELECT id FROM organizations WHERE slug='mindorigin');

