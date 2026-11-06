package httpapi

import (
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"net/mail"
	"regexp"
	"strings"

	"company-community/backend/internal/domain"
	"company-community/backend/internal/localauth"
)

var organizationSlugPattern = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)
var organizationDomainPattern = regexp.MustCompile(`^(?:[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$`)

func validWorkEmail(raw string) (string, error) {
	email := strings.ToLower(strings.TrimSpace(raw))
	addr, err := mail.ParseAddress(email)
	if err != nil || addr.Address != email || len(email) > 254 || !strings.Contains(email, "@") {
		return "", domain.Invalidf("enter a valid work email address")
	}
	return email, nil
}

func (a *API) provisionIdentity(email, subject, password string) (issuer, normalizedSubject string, salt, hash []byte, iterations int, err error) {
	if a.DevelopmentAuth || a.LocalAccountAuth {
		if len(password) < 12 || len(password) > 128 {
			return "", "", nil, nil, 0, domain.Invalidf("local account passwords must be 12 to 128 characters")
		}
		if strings.TrimSpace(subject) == "" {
			var raw [24]byte
			if _, err = rand.Read(raw[:]); err != nil {
				return
			}
			normalizedSubject = "local-" + hex.EncodeToString(raw[:])
		} else {
			normalizedSubject = strings.TrimSpace(subject)
		}
		issuer = "development"
		if a.LocalAccountAuth {
			issuer = "local-password"
		}
		salt, hash, err = localauth.Hash(password)
		iterations = localauth.Iterations
		return
	}
	if a.TrustedIssuer == "" || strings.TrimSpace(subject) == "" || len(subject) > 255 || password != "" {
		err = domain.Invalidf("a valid company identity subject is required; passwords are managed by company sign-in")
		return
	}
	return a.TrustedIssuer, strings.TrimSpace(subject), nil, nil, 0, nil
}

func (a *API) listOrganizations(r *http.Request) (any, int, error) {
	if user(r).Role != "platform_admin" {
		return nil, 0, domain.ErrForbidden
	}
	if a.OrganizationAdmin == nil {
		return nil, 0, domain.ErrNotFound
	}
	items, err := a.OrganizationAdmin.Organizations(r.Context(), user(r))
	return map[string]any{"items": items}, http.StatusOK, err
}

func (a *API) listOrganizationUsers(r *http.Request) (any, int, error) {
	if user(r).Role != "platform_admin" && user(r).Role != "admin" {
		return nil, 0, domain.ErrForbidden
	}
	if a.OrganizationAdmin == nil {
		return nil, 0, domain.ErrNotFound
	}
	f, err := filters(r)
	if err != nil {
		return nil, 0, err
	}
	items, err := a.OrganizationAdmin.OrganizationUsers(r.Context(), user(r), r.PathValue("id"), f.Query, f.Limit, f.Offset)
	return page(items, f), http.StatusOK, err
}

func (a *API) createOrganization(r *http.Request) (any, int, error) {
	if user(r).Role != "platform_admin" {
		return nil, 0, domain.ErrForbidden
	}
	if a.OrganizationAdmin == nil {
		return nil, 0, domain.ErrNotFound
	}
	var in struct {
		Slug          string `json:"slug"`
		Name          string `json:"name"`
		EmailDomain   string `json:"email_domain"`
		AdminEmail    string `json:"admin_email"`
		AdminName     string `json:"admin_name"`
		AdminSubject  string `json:"admin_subject"`
		AdminPassword string `json:"admin_password"`
	}
	if err := decode(r, &in); err != nil {
		return nil, 0, err
	}
	in.Slug = strings.ToLower(strings.TrimSpace(in.Slug))
	in.Name = strings.TrimSpace(in.Name)
	in.EmailDomain = strings.ToLower(strings.TrimSpace(in.EmailDomain))
	in.AdminName = strings.TrimSpace(in.AdminName)
	if !organizationSlugPattern.MatchString(in.Slug) || len(in.Slug) > 60 || len(in.Name) < 2 || len(in.Name) > 100 || len(in.EmailDomain) > 253 || !organizationDomainPattern.MatchString(in.EmailDomain) || len(in.AdminName) < 1 || len(in.AdminName) > 100 {
		return nil, 0, domain.Invalidf("organization slug, name, email domain, and initial admin name are required")
	}
	email, err := validWorkEmail(in.AdminEmail)
	if err != nil {
		return nil, 0, err
	}
	if !strings.HasSuffix(email, "@"+in.EmailDomain) {
		return nil, 0, domain.Invalidf("initial admin email must use the organization's email domain")
	}
	issuer, subject, salt, hash, iterations, err := a.provisionIdentity(email, in.AdminSubject, in.AdminPassword)
	if err != nil {
		return nil, 0, err
	}
	org, admin, err := a.OrganizationAdmin.CreateOrganization(r.Context(), user(r), domain.OrganizationAdminInput{Slug: in.Slug, Name: in.Name, EmailDomain: in.EmailDomain, AdminEmail: email, AdminName: in.AdminName, Issuer: issuer, Subject: subject, Salt: salt, Hash: hash, Iterations: iterations})
	return map[string]any{"organization": org, "admin": admin}, http.StatusCreated, err
}

func (a *API) createOrganizationUser(r *http.Request) (any, int, error) {
	if user(r).Role != "platform_admin" && user(r).Role != "admin" {
		return nil, 0, domain.ErrForbidden
	}
	if a.OrganizationAdmin == nil {
		return nil, 0, domain.ErrNotFound
	}
	var in struct {
		OrganizationID string `json:"organization_id"`
		Email          string `json:"email"`
		Name           string `json:"display_name"`
		Role           string `json:"role"`
		Subject        string `json:"subject"`
		Password       string `json:"password"`
	}
	if err := decode(r, &in); err != nil {
		return nil, 0, err
	}
	in.Name = strings.TrimSpace(in.Name)
	email, err := validWorkEmail(in.Email)
	if err != nil {
		return nil, 0, err
	}
	if len(in.Name) < 1 || len(in.Name) > 100 || (in.Role != "employee" && in.Role != "moderator" && in.Role != "admin") {
		return nil, 0, domain.Invalidf("display name and an employee, moderator, or admin role are required")
	}
	issuer, subject, salt, hash, iterations, err := a.provisionIdentity(email, in.Subject, in.Password)
	if err != nil {
		return nil, 0, err
	}
	u, err := a.OrganizationAdmin.CreateOrganizationUser(r.Context(), user(r), domain.OrganizationUserInput{OrganizationID: in.OrganizationID, Email: email, DisplayName: in.Name, Role: in.Role, Issuer: issuer, Subject: subject, Salt: salt, Hash: hash, Iterations: iterations})
	return u, http.StatusCreated, err
}

func (a *API) createPost(kind string) endpoint {
	return func(r *http.Request) (any, int, error) {
		var in domain.PostInput
		if err := decode(r, &in); err != nil {
			return nil, 0, err
		}
		p, err := a.Content.Create(r.Context(), user(r), kind, in)
		return p, http.StatusCreated, err
	}
}
func (a *API) getPost(r *http.Request) (any, int, error) {
	p, err := a.Content.Get(r.Context(), user(r), r.PathValue("id"))
	return p, http.StatusOK, err
}
func (a *API) listPosts(r *http.Request) (any, int, error) {
	f, err := filters(r)
	if err != nil {
		return nil, 0, err
	}
	p, err := a.Content.List(r.Context(), user(r), f)
	return page(p, f), http.StatusOK, err
}
func (a *API) updatePost(r *http.Request) (any, int, error) {
	var in domain.PostInput
	if err := decode(r, &in); err != nil {
		return nil, 0, err
	}
	p, err := a.Content.Update(r.Context(), user(r), r.PathValue("id"), in)
	return p, http.StatusOK, err
}
func (a *API) transition(action string) endpoint {
	return func(r *http.Request) (any, int, error) {
		p, err := a.Content.Transition(r.Context(), user(r), r.PathValue("id"), action)
		return p, http.StatusOK, err
	}
}
func (a *API) revisions(r *http.Request) (any, int, error) {
	f, err := filters(r)
	if err != nil {
		return nil, 0, err
	}
	p, err := a.Content.Revisions(r.Context(), user(r), r.PathValue("id"), f.Limit, f.Offset)
	return page(p, f), http.StatusOK, err
}
func (a *API) answer(r *http.Request) (any, int, error) {
	var in struct {
		Body string `json:"body_markdown"`
	}
	if err := decode(r, &in); err != nil {
		return nil, 0, err
	}
	p, err := a.Content.Answer(r.Context(), user(r), r.PathValue("id"), in.Body)
	return p, http.StatusCreated, err
}
func (a *API) answers(r *http.Request) (any, int, error) {
	f, err := filters(r)
	if err != nil {
		return nil, 0, err
	}
	p, err := a.Content.Answers(r.Context(), user(r), r.PathValue("id"), f.Limit, f.Offset)
	return page(p, f), http.StatusOK, err
}
func (a *API) accept(r *http.Request) (any, int, error) {
	var in struct {
		AnswerID string `json:"answer_id"`
	}
	if err := decode(r, &in); err != nil {
		return nil, 0, err
	}
	if !domain.ValidID(in.AnswerID) {
		return nil, 0, domain.Invalidf("answer_id must be a UUID")
	}
	p, err := a.Content.Accept(r.Context(), user(r), r.PathValue("id"), in.AnswerID)
	return p, http.StatusOK, err
}
func (a *API) listTags(r *http.Request) (any, int, error) {
	f, err := filters(r)
	if err != nil {
		return nil, 0, err
	}
	p, err := a.Tags.List(r.Context(), user(r), f.Limit, f.Offset)
	return page(p, f), http.StatusOK, err
}
func (a *API) createTag(r *http.Request) (any, int, error) {
	var in domain.TagInput
	if err := decode(r, &in); err != nil {
		return nil, 0, err
	}
	p, err := a.Tags.Create(r.Context(), user(r), in)
	return p, http.StatusCreated, err
}
func (a *API) tagStatus(r *http.Request) (any, int, error) {
	var in struct {
		Status string `json:"status"`
	}
	if err := decode(r, &in); err != nil {
		return nil, 0, err
	}
	p, err := a.Tags.SetStatus(r.Context(), user(r), r.PathValue("id"), in.Status)
	return p, http.StatusOK, err
}
func (a *API) proposeTag(r *http.Request) (any, int, error) {
	var in domain.TagInput
	if err := decode(r, &in); err != nil {
		return nil, 0, err
	}
	p, err := a.Tags.Propose(r.Context(), user(r), in)
	return p, http.StatusCreated, err
}
func (a *API) proposals(r *http.Request) (any, int, error) {
	f, err := filters(r)
	if err != nil {
		return nil, 0, err
	}
	p, err := a.Tags.Proposals(r.Context(), user(r), f.Limit, f.Offset)
	return page(p, f), http.StatusOK, err
}
func (a *API) reviewProposal(r *http.Request) (any, int, error) {
	var in struct {
		Decision string `json:"decision"`
	}
	if err := decode(r, &in); err != nil {
		return nil, 0, err
	}
	p, err := a.Tags.Review(r.Context(), user(r), r.PathValue("id"), in.Decision)
	return p, http.StatusOK, err
}
func (a *API) search(r *http.Request) (any, int, error) {
	f, err := filters(r)
	if err != nil {
		return nil, 0, err
	}
	p, err := a.Search.Search(r.Context(), user(r), f)
	return page(p, f), http.StatusOK, err
}
func (a *API) moderate(r *http.Request) (any, int, error) {
	var in struct {
		Hidden *bool `json:"hidden"`
	}
	if err := decode(r, &in); err != nil {
		return nil, 0, err
	}
	if in.Hidden == nil {
		return nil, 0, domain.Invalidf("hidden is required")
	}
	p, err := a.Content.Moderate(r.Context(), user(r), r.PathValue("id"), *in.Hidden)
	return p, http.StatusOK, err
}
func (a *API) updateUser(r *http.Request) (any, int, error) {
	if user(r).Role != "admin" && user(r).Role != "platform_admin" {
		return nil, 0, domain.ErrForbidden
	}
	var in struct {
		Role   string `json:"role"`
		Active *bool  `json:"active"`
	}
	if err := decode(r, &in); err != nil {
		return nil, 0, err
	}
	if in.Role != "employee" && in.Role != "moderator" && in.Role != "admin" || in.Active == nil {
		return nil, 0, domain.Invalidf("role (employee, moderator, admin) and active are required")
	}
	p, err := a.Admin.UpdateUser(r.Context(), user(r), r.PathValue("id"), in.Role, *in.Active)
	return p, http.StatusOK, err
}
func (a *API) audit(r *http.Request) (any, int, error) {
	if user(r).Role != "admin" {
		return nil, 0, domain.ErrForbidden
	}
	f, err := filters(r)
	if err != nil {
		return nil, 0, err
	}
	p, err := a.Admin.Audit(r.Context(), user(r), f.Limit, f.Offset)
	return page(p, f), http.StatusOK, err
}
