ALTER TABLE users DROP CONSTRAINT users_role_check;
ALTER TABLE users ADD CONSTRAINT users_role_check
 CHECK (role IN ('employee','moderator','admin','platform_admin'));
