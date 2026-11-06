package store

import (
	"context"
	"strings"

	"company-community/backend/internal/domain"
	"github.com/jackc/pgx/v5"
)

func (s *Store) Organizations(ctx context.Context, actor domain.User) ([]domain.Organization, error) {
	if actor.Role != "platform_admin" {
		return nil, domain.ErrForbidden
	}
	rows, err := s.Pool.Query(ctx, `SELECT id::text,slug,name,email_domain FROM organizations ORDER BY name,id`)
	if err != nil {
		return nil, translate(err)
	}
	defer rows.Close()
	out := []domain.Organization{}
	for rows.Next() {
		var o domain.Organization
		if err := rows.Scan(&o.ID, &o.Slug, &o.Name, &o.EmailDomain); err != nil {
			return nil, translate(err)
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

func (s *Store) OrganizationUsers(ctx context.Context, actor domain.User, orgID, q string, limit, offset int) ([]domain.User, error) {
	if actor.Role == "admin" && orgID != actor.OrganizationID {
		return nil, domain.ErrForbidden
	}
	if actor.Role != "admin" && actor.Role != "platform_admin" {
		return nil, domain.ErrForbidden
	}
	rows, err := s.Pool.Query(ctx, `SELECT `+userColumns+` FROM users WHERE organization_id=$1 AND deleted_at IS NULL AND ($2='' OR position(lower($2) in lower(display_name))>0 OR position(lower($2) in lower(email))>0) ORDER BY display_name,id LIMIT $3 OFFSET $4`, orgID, q, limit, offset)
	if err != nil {
		return nil, translate(err)
	}
	defer rows.Close()
	out := []domain.User{}
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

func (s *Store) CreateOrganization(ctx context.Context, actor domain.User, in domain.OrganizationAdminInput) (domain.Organization, domain.User, error) {
	if actor.Role != "platform_admin" {
		return domain.Organization{}, domain.User{}, domain.ErrForbidden
	}
	var org domain.Organization
	var admin domain.User
	err := s.transaction(ctx, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `INSERT INTO organizations(slug,name,email_domain) VALUES($1,$2,$3) RETURNING id::text,slug,name,email_domain`, in.Slug, in.Name, in.EmailDomain).Scan(&org.ID, &org.Slug, &org.Name, &org.EmailDomain); err != nil {
			return err
		}
		var err error
		admin, err = scanUser(tx.QueryRow(ctx, `INSERT INTO users(issuer,subject,email,display_name,role,organization_id) VALUES($1,$2,$3,$4,'admin',$5) RETURNING `+userColumns, in.Issuer, in.Subject, in.AdminEmail, in.AdminName, org.ID))
		if err != nil {
			return err
		}
		if in.Iterations > 0 {
			if len(in.Salt) != 16 || len(in.Hash) != 32 || in.Iterations < 200000 {
				return domain.Invalidf("invalid local login credential")
			}
			if _, err = tx.Exec(ctx, `INSERT INTO local_credentials(user_id,username,password_salt,password_hash,iterations) VALUES($1,$2,$3,$4,$5)`, admin.ID, in.AdminEmail, in.Salt, in.Hash, in.Iterations); err != nil {
				return err
			}
		}
		if _, err = tx.Exec(ctx, `INSERT INTO categories(name,slug,description,organization_id) SELECT name,slug,description,$1 FROM categories WHERE organization_id=(SELECT id FROM organizations WHERE slug='mindorigin') ON CONFLICT DO NOTHING`, org.ID); err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, `INSERT INTO tags(name,slug,description,status,created_by,category_id,organization_id) SELECT t.name,t.slug,t.description,t.status,$2,tc.id,$1 FROM tags t LEFT JOIN categories mc ON mc.id=t.category_id AND mc.organization_id=t.organization_id LEFT JOIN categories tc ON tc.slug=mc.slug AND tc.organization_id=$1 WHERE t.organization_id=(SELECT id FROM organizations WHERE slug='mindorigin') ON CONFLICT DO NOTHING`, org.ID, admin.ID); err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, `INSERT INTO reward_rules(event_type,points,enabled,daily_cap,version,updated_by,updated_at,reward_kind,organization_id) SELECT event_type,points,enabled,daily_cap,version,$2,now(),reward_kind,$1 FROM reward_rules WHERE organization_id=(SELECT id FROM organizations WHERE slug='mindorigin') ON CONFLICT DO NOTHING`, org.ID, admin.ID); err != nil {
			return err
		}
		if err = audit(ctx, tx, actor, "organization.created", org.ID, map[string]string{"slug": org.Slug, "email_domain": org.EmailDomain, "initial_admin_id": admin.ID}); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return domain.Organization{}, domain.User{}, err
	}
	admin.OrganizationID = org.ID
	admin.OrganizationSlug = org.Slug
	admin.OrganizationName = org.Name
	return org, admin, nil
}

func (s *Store) CreateOrganizationUser(ctx context.Context, actor domain.User, in domain.OrganizationUserInput) (domain.User, error) {
	if actor.Role != "admin" && actor.Role != "platform_admin" {
		return domain.User{}, domain.ErrForbidden
	}
	if in.Role != "employee" && in.Role != "moderator" && in.Role != "admin" {
		return domain.User{}, domain.Invalidf("invalid organization role")
	}
	var out domain.User
	err := s.transaction(ctx, func(tx pgx.Tx) error {
		orgID := actor.OrganizationID
		if actor.Role == "platform_admin" {
			orgID = in.OrganizationID
			if strings.TrimSpace(orgID) == "" {
				return domain.Invalidf("organization is required")
			}
		} else if in.OrganizationID != "" && in.OrganizationID != actor.OrganizationID {
			return domain.ErrForbidden
		}
		var domainName string
		if err := tx.QueryRow(ctx, `SELECT email_domain FROM organizations WHERE id=$1`, orgID).Scan(&domainName); err != nil {
			return translate(err)
		}
		at := strings.LastIndex(in.Email, "@")
		if at < 1 || !strings.EqualFold(in.Email[at+1:], domainName) {
			return domain.Invalidf("email domain must match the organization")
		}
		var err error
		out, err = scanUser(tx.QueryRow(ctx, `INSERT INTO users(issuer,subject,email,display_name,role,organization_id) VALUES($1,$2,$3,$4,$5,$6) RETURNING `+userColumns, in.Issuer, in.Subject, in.Email, in.DisplayName, in.Role, orgID))
		if err != nil {
			return err
		}
		if in.Iterations > 0 {
			if len(in.Salt) != 16 || len(in.Hash) != 32 || in.Iterations < 200000 {
				return domain.Invalidf("invalid local login credential")
			}
			if _, err = tx.Exec(ctx, `INSERT INTO local_credentials(user_id,username,password_salt,password_hash,iterations) VALUES($1,$2,$3,$4,$5)`, out.ID, in.Email, in.Salt, in.Hash, in.Iterations); err != nil {
				return err
			}
		}
		return audit(ctx, tx, actor, "identity.provisioned", out.ID, map[string]string{"role": out.Role, "organization_id": orgID})
	})
	if err == nil {
		err = s.enrichOrganization(ctx, &out)
	}
	return out, err
}

func scanUser(row scanner) (domain.User, error) {
	var u domain.User
	err := row.Scan(&u.ID, &u.Issuer, &u.Subject, &u.Email, &u.DisplayName, &u.Role, &u.Active, &u.OrganizationID, &u.Deleted)
	return u, translate(err)
}

const userColumns = `id::text,issuer,subject,email,display_name,role,active,COALESCE(organization_id::text,''),deleted_at IS NOT NULL`

func (s *Store) FindIdentity(ctx context.Context, issuer, subject string) (domain.User, error) {
	u, err := scanUser(s.Pool.QueryRow(ctx, `UPDATE users SET last_active_at=now() WHERE issuer=$1 AND subject=$2 AND active RETURNING `+userColumns, issuer, subject))
	if err == nil {
		err = s.enrichOrganization(ctx, &u)
	}
	return u, err
}
func (s *Store) enrichOrganization(ctx context.Context, u *domain.User) error {
	if u.OrganizationID == "" {
		return nil
	}
	return s.Pool.QueryRow(ctx, `SELECT slug,name FROM organizations WHERE id=$1`, u.OrganizationID).Scan(&u.OrganizationSlug, &u.OrganizationName)
}
func (s *Store) Provision(ctx context.Context, in domain.User) (domain.User, error) {
	if in.Role != "employee" && in.Role != "moderator" && in.Role != "admin" && in.Role != "platform_admin" {
		return domain.User{}, domain.Invalidf("invalid role")
	}
	var u domain.User
	err := s.transaction(ctx, func(tx pgx.Tx) error {
		var err error
		var orgID string
		var organization any
		if in.Role != "platform_admin" {
			if err = tx.QueryRow(ctx, `SELECT id::text FROM organizations WHERE email_domain=lower(split_part($1,'@',2))`, in.Email).Scan(&orgID); err != nil {
				return domain.Invalidf("email domain is not configured for an organization")
			}
			organization = orgID
		}
		u, err = scanUser(tx.QueryRow(ctx, `INSERT INTO users(issuer,subject,email,display_name,role,organization_id) VALUES($1,$2,$3,$4,$5,$6) RETURNING `+userColumns, in.Issuer, in.Subject, in.Email, in.DisplayName, in.Role, organization))
		if err != nil {
			return err
		}
		return audit(ctx, tx, u, "identity.provisioned", u.ID, map[string]string{"origin": "operator_cli", "role": u.Role})
	})
	if err == nil {
		err = s.enrichOrganization(ctx, &u)
	}
	return u, err
}

func (s *Store) ProvisionLocal(ctx context.Context, in domain.User, salt, hash []byte, iterations int) (domain.User, error) {
	if (in.Role != "employee" && in.Role != "moderator" && in.Role != "admin" && in.Role != "platform_admin") || in.Issuer != "local-password" || strings.TrimSpace(in.Subject) == "" || len(salt) != 16 || len(hash) != 32 || iterations < 200000 {
		return domain.User{}, domain.Invalidf("invalid local account")
	}
	var u domain.User
	err := s.transaction(ctx, func(tx pgx.Tx) error {
		var orgID any
		if in.Role != "platform_admin" {
			var found string
			if err := tx.QueryRow(ctx, `SELECT id::text FROM organizations WHERE email_domain=lower(split_part($1,'@',2))`, in.Email).Scan(&found); err != nil {
				return domain.Invalidf("email domain is not configured for an organization")
			}
			orgID = found
		}
		var err error
		u, err = scanUser(tx.QueryRow(ctx, `INSERT INTO users(issuer,subject,email,display_name,role,organization_id) VALUES($1,$2,$3,$4,$5,$6) RETURNING `+userColumns, in.Issuer, in.Subject, in.Email, in.DisplayName, in.Role, orgID))
		if err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, `INSERT INTO local_credentials(user_id,username,password_salt,password_hash,iterations) VALUES($1,$2,$3,$4,$5)`, u.ID, in.Email, salt, hash, iterations); err != nil {
			return err
		}
		return audit(ctx, tx, u, "identity.provisioned", u.ID, map[string]string{"origin": "operator_cli", "role": u.Role})
	})
	if err == nil {
		err = s.enrichOrganization(ctx, &u)
	}
	return u, err
}

func (s *Store) SetLocalCredential(ctx context.Context, issuer, username, email string, salt, hash []byte, iterations int) error {
	if (issuer != "development" && issuer != "local-password") || len(username) < 3 || len(username) > 100 || len(salt) != 16 || len(hash) != 32 || iterations < 200000 {
		return domain.Invalidf("invalid local login credential")
	}
	_, err := s.Pool.Exec(ctx, `INSERT INTO local_credentials(user_id,username,password_salt,password_hash,iterations)
		 SELECT id,$2,$4,$5,$6 FROM users WHERE issuer=$1 AND email=$3
		 ON CONFLICT(user_id) DO UPDATE SET username=EXCLUDED.username,password_salt=EXCLUDED.password_salt,password_hash=EXCLUDED.password_hash,iterations=EXCLUDED.iterations,updated_at=now()`, issuer, username, email, salt, hash, iterations)
	if err != nil {
		return translate(err)
	}
	return nil
}

func (s *Store) LocalCredential(ctx context.Context, username string) (domain.LocalCredential, error) {
	var credential domain.LocalCredential
	err := s.Pool.QueryRow(ctx, `SELECT `+userColumns+`,c.password_salt,c.password_hash,c.iterations
	 FROM local_credentials c JOIN users u ON u.id=c.user_id
		 WHERE lower(c.username)=lower($1) AND u.issuer IN ('development','local-password') AND u.active AND u.deleted_at IS NULL`, username).
		Scan(&credential.User.ID, &credential.User.Issuer, &credential.User.Subject, &credential.User.Email, &credential.User.DisplayName, &credential.User.Role, &credential.User.Active, &credential.User.OrganizationID, &credential.User.Deleted, &credential.Salt, &credential.Hash, &credential.Iterations)
	if err == nil {
		err = s.enrichOrganization(ctx, &credential.User)
	}
	return credential, translate(err)
}

func (s *Store) UpdateUser(ctx context.Context, actor domain.User, id, role string, active bool) (domain.User, error) {
	if actor.Role != "admin" && actor.Role != "platform_admin" {
		return domain.User{}, domain.ErrForbidden
	}
	if role != "employee" && role != "moderator" && role != "admin" {
		return domain.User{}, domain.Invalidf("invalid organization role")
	}
	var out domain.User
	err := s.transaction(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(710482192)`); err != nil {
			return err
		}
		query := `SELECT ` + userColumns + ` FROM users WHERE id=$1 AND organization_id=$2 AND deleted_at IS NULL FOR UPDATE`
		args := []any{id, actor.OrganizationID}
		if actor.Role == "platform_admin" {
			query = `SELECT ` + userColumns + ` FROM users WHERE id=$1 AND deleted_at IS NULL FOR UPDATE`
			args = []any{id}
		}
		old, err := scanUser(tx.QueryRow(ctx, query, args...))
		if err != nil {
			return err
		}
		if old.Role == "platform_admin" {
			return domain.ErrForbidden
		}
		if old.Role == "admin" && old.Active && (role != "admin" || !active) {
			var count int
			if err = tx.QueryRow(ctx, `SELECT count(*) FROM users WHERE role='admin' AND active AND organization_id=$1`, old.OrganizationID).Scan(&count); err != nil {
				return err
			}
			if count <= 1 {
				return domain.ErrConflict
			}
		}
		orgID := actor.OrganizationID
		if actor.Role == "platform_admin" {
			orgID = old.OrganizationID
		}
		out, err = scanUser(tx.QueryRow(ctx, `UPDATE users SET role=$2,active=$3,updated_at=now() WHERE id=$1 AND organization_id=$4 AND deleted_at IS NULL RETURNING `+userColumns, id, role, active, orgID))
		if err != nil {
			return err
		}
		return audit(ctx, tx, actor, "identity.updated", id, map[string]any{"previous_role": old.Role, "role": role, "active": active})
	})
	if err == nil {
		err = s.enrichOrganization(ctx, &out)
	}
	return out, err
}

func (s *Store) DeleteUser(ctx context.Context, actor domain.User, id string) error {
	if actor.Role != "admin" && actor.Role != "platform_admin" {
		return domain.ErrForbidden
	}
	if id == actor.ID {
		return domain.Invalidf("you cannot delete your own account")
	}
	return s.transaction(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(710482192)`); err != nil {
			return err
		}
		query := `SELECT ` + userColumns + ` FROM users WHERE id=$1 AND organization_id=$2 AND deleted_at IS NULL FOR UPDATE`
		args := []any{id, actor.OrganizationID}
		if actor.Role == "platform_admin" {
			query = `SELECT ` + userColumns + ` FROM users WHERE id=$1 AND deleted_at IS NULL FOR UPDATE`
			args = []any{id}
		}
		target, err := scanUser(tx.QueryRow(ctx, query, args...))
		if err != nil {
			return err
		}
		if target.Role == "platform_admin" {
			return domain.ErrForbidden
		}
		if target.Role == "admin" && target.Active {
			var count int
			if err = tx.QueryRow(ctx, `SELECT count(*) FROM users WHERE role='admin' AND active AND deleted_at IS NULL AND organization_id=$1`, target.OrganizationID).Scan(&count); err != nil {
				return err
			}
			if count <= 1 {
				return domain.ErrConflict
			}
		}
		if _, err = tx.Exec(ctx, `UPDATE users SET active=false,deleted_at=now(),display_name='Deleted account',email='deleted+'||replace(id::text,'-','')||'@deleted.invalid',issuer='deleted',subject=id::text,bio='',updated_at=now() WHERE id=$1`, id); err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, `DELETE FROM local_credentials WHERE user_id=$1`, id); err != nil {
			return err
		}
		return audit(ctx, tx, actor, "identity.deleted", id, map[string]string{"previous_role": target.Role, "organization_id": target.OrganizationID})
	})
}
func (s *Store) Audit(ctx context.Context, actor domain.User, limit, offset int) ([]map[string]any, error) {
	if actor.Role != "admin" {
		return nil, domain.ErrForbidden
	}
	rows, err := s.Pool.Query(ctx, `SELECT a.id,a.actor_id::text,a.action,a.target_id::text,a.details,a.created_at FROM audit_logs a WHERE a.organization_id=$3 ORDER BY a.id DESC LIMIT $1 OFFSET $2`, limit, offset, actor.OrganizationID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		values, err := rows.Values()
		if err != nil {
			return nil, err
		}
		out = append(out, map[string]any{"id": values[0], "actor_id": values[1], "action": values[2], "target_id": values[3], "details": values[4], "created_at": values[5]})
	}
	return out, rows.Err()
}
