INSERT INTO categories(name,slug,description) VALUES
 ('Engineering','engineering','Software development, architecture, and engineering practices.'),
 ('Data & AI','data-ai','Data platforms, analytics, machine learning, and AI.'),
 ('Security','security','Application security, infrastructure security, and privacy.'),
 ('Infrastructure','infrastructure','Cloud, DevOps, reliability, and developer tooling.'),
 ('Company','company','Company practices, onboarding, and cross-team knowledge.')
ON CONFLICT (slug) DO NOTHING;

-- Default tags are platform-provided and do not have an employee creator.
ALTER TABLE tags ALTER COLUMN created_by DROP NOT NULL;
INSERT INTO tags(name,slug,description,category_id,created_by)
SELECT seed.name,seed.slug,seed.description,c.id,NULL
FROM (VALUES
 ('Go','go','Go language and ecosystem.','engineering'),
 ('TypeScript','typescript','TypeScript language and tooling.','engineering'),
 ('React','react','React and frontend development.','engineering'),
 ('API Design','api-design','HTTP APIs, contracts, and service integration.','engineering'),
 ('Architecture','architecture','Software architecture and system design.','engineering'),
 ('PostgreSQL','postgresql','PostgreSQL database design and operations.','data-ai'),
 ('Machine Learning','machine-learning','Machine learning methods and applications.','data-ai'),
 ('Generative AI','generative-ai','Generative AI tools, models, and applications.','data-ai'),
 ('Application Security','application-security','Secure application design and implementation.','security'),
 ('Cloud','cloud','Cloud platforms and services.','infrastructure'),
 ('DevOps','devops','Build, release, and operations practices.','infrastructure'),
 ('Onboarding','onboarding','Guides and knowledge for joining the company.','company')
) AS seed(name,slug,description,category_slug)
JOIN categories c ON c.slug=seed.category_slug
ON CONFLICT (slug) DO NOTHING;
