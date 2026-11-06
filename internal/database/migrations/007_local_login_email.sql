-- Local development accounts sign in with their provisioned email address.
-- Preserve existing salted hashes while replacing the old nickname usernames.
UPDATE local_credentials c
SET username = u.email, updated_at = now()
FROM users u
WHERE u.id = c.user_id
  AND u.issuer = 'development'
  AND c.username <> u.email;
