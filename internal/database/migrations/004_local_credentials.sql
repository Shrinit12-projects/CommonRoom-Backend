-- Local development login credentials are populated by the guarded seed-dev command.
-- Passwords are never stored in plaintext and no shared credentials ship in migrations.
CREATE TABLE local_credentials (
 user_id uuid PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
 username text NOT NULL UNIQUE CHECK (length(username) BETWEEN 3 AND 100),
 password_salt bytea NOT NULL CHECK (octet_length(password_salt) = 16),
 password_hash bytea NOT NULL CHECK (octet_length(password_hash) = 32),
 iterations integer NOT NULL CHECK (iterations >= 200000),
 created_at timestamptz NOT NULL DEFAULT now(),
 updated_at timestamptz NOT NULL DEFAULT now()
);
