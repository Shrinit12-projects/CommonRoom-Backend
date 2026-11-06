-- Keep historical authorship and audit references, but disable seeded demo employee
-- accounts and remove their local credentials. Real employees can be added in the admin UI.
UPDATE users
SET active=false, updated_at=now()
WHERE issuer='development'
  AND subject IN ('mindorigin-alex','mindorigin-priya','firstock-rahul');

DELETE FROM local_credentials
WHERE user_id IN (
  SELECT id FROM users
  WHERE issuer='development'
    AND subject IN ('mindorigin-alex','mindorigin-priya','firstock-rahul')
);
