ALTER TABLE posts ADD CONSTRAINT posts_id_organization_unique UNIQUE(id,organization_id);
ALTER TABLE categories ADD CONSTRAINT categories_id_organization_unique UNIQUE(id,organization_id);

CREATE TABLE post_categories (
 post_id uuid NOT NULL,
 category_id uuid NOT NULL,
 organization_id uuid NOT NULL REFERENCES organizations(id),
 created_at timestamptz NOT NULL DEFAULT now(),
 PRIMARY KEY(post_id,category_id),
 FOREIGN KEY(post_id,organization_id) REFERENCES posts(id,organization_id) ON DELETE CASCADE,
 FOREIGN KEY(category_id,organization_id) REFERENCES categories(id,organization_id)
);
INSERT INTO post_categories(post_id,category_id,organization_id)
SELECT id,category_id,organization_id FROM posts WHERE category_id IS NOT NULL
ON CONFLICT DO NOTHING;
CREATE INDEX post_categories_category ON post_categories(organization_id,category_id,post_id);
