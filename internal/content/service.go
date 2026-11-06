package content

import (
	"bytes"
	"context"
	"errors"
	"strings"

	"company-community/backend/internal/domain"
	"github.com/microcosm-cc/bluemonday"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
)

type Repository interface {
	CreatePost(context.Context, domain.User, domain.Post) (domain.Post, error)
	GetPost(context.Context, string) (domain.Post, error)
	ListPosts(context.Context, domain.User, domain.Filter) ([]domain.Post, error)
	MutatePost(context.Context, domain.User, string, func(*domain.Post) error) (domain.Post, error)
	CreateAnswer(context.Context, domain.User, string, string, string) (domain.Answer, error)
	ListAnswers(context.Context, domain.User, string, int, int) ([]domain.Answer, error)
	AcceptAnswer(context.Context, domain.User, string, string) (domain.Post, error)
	Revisions(context.Context, domain.User, string, int, int) ([]map[string]any, error)
}
type Service struct {
	repo      Repository
	markdown  goldmark.Markdown
	sanitizer *bluemonday.Policy
}

func questionTitle(body string) string {
	plain := strings.Join(strings.Fields(strings.NewReplacer("#", "", "*", "", "`", "", "_", "", ">", "").Replace(body)), " ")
	if len([]rune(plain)) > 200 {
		plain = string([]rune(plain)[:200])
	}
	return plain
}

func categoryIDs(in domain.PostInput) ([]string, error) {
	ids := append([]string(nil), in.CategoryIDs...)
	if in.CategoryID != "" {
		ids = append(ids, in.CategoryID)
	}
	seen := map[string]bool{}
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		if !domain.ValidID(id) {
			return nil, domain.Invalidf("category_ids must contain UUIDs")
		}
		if !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	if len(out) > 8 {
		return nil, domain.Invalidf("choose no more than 8 categories")
	}
	return out, nil
}

func New(repo Repository) *Service {
	return &Service{repo: repo, markdown: goldmark.New(goldmark.WithExtensions(extension.GFM)), sanitizer: bluemonday.UGCPolicy()}
}

// Render suppresses raw HTML and sanitizes generated HTML as a second boundary.
// Mermaid remains a fenced code block; clients must use a secure renderer if enabled.
func (s *Service) Render(body string) (string, error) {
	var buf bytes.Buffer
	if err := s.markdown.Convert([]byte(body), &buf); err != nil {
		return "", err
	}
	return s.sanitizer.Sanitize(buf.String()), nil
}
func (s *Service) Create(ctx context.Context, u domain.User, kind string, in domain.PostInput) (domain.Post, error) {
	if kind != "blog" && kind != "question" {
		return domain.Post{}, domain.Invalidf("unsupported content type")
	}
	if kind == "question" && strings.TrimSpace(in.Title) == "" {
		in.Title = questionTitle(in.Body)
	}
	selectedCategories, err := categoryIDs(in)
	if err != nil {
		return domain.Post{}, err
	}
	if kind == "question" && len(selectedCategories) == 0 {
		return domain.Post{}, domain.Invalidf("questions require at least one category")
	}
	if err := in.Validate(); err != nil {
		return domain.Post{}, err
	}
	html, err := s.Render(in.Body)
	if err != nil {
		return domain.Post{}, err
	}
	status := "draft"
	if kind == "question" {
		status = "open"
	}
	var category *string
	if len(selectedCategories) > 0 {
		category = &selectedCategories[0]
	}
	return s.repo.CreatePost(ctx, u, domain.Post{Kind: kind, AuthorID: u.ID, Title: in.Title, Summary: in.Summary, Body: in.Body, HTML: html, TagIDs: in.TagIDs, Status: status, CategoryID: category, CategoryIDs: selectedCategories})
}
func (s *Service) Get(ctx context.Context, u domain.User, id string) (domain.Post, error) {
	p, err := s.repo.GetPost(ctx, id)
	if err != nil {
		return p, err
	}
	if !u.CanRead(p) {
		return domain.Post{}, domain.ErrNotFound
	}
	return p, nil
}
func (s *Service) List(ctx context.Context, u domain.User, f domain.Filter) ([]domain.Post, error) {
	return s.repo.ListPosts(ctx, u, f)
}
func (s *Service) Update(ctx context.Context, u domain.User, id string, in domain.PostInput) (domain.Post, error) {
	if p, err := s.repo.GetPost(ctx, id); err == nil && p.Kind == "question" {
		if strings.TrimSpace(in.Title) == "" {
			in.Title = questionTitle(in.Body)
		}
	} else if err != nil && !errors.Is(err, domain.ErrNotFound) {
		return domain.Post{}, err
	}
	if err := in.Validate(); err != nil {
		return domain.Post{}, err
	}
	if in.Version < 1 {
		return domain.Post{}, domain.Invalidf("version is required for updates")
	}
	selectedCategories, err := categoryIDs(in)
	if err != nil {
		return domain.Post{}, err
	}
	html, err := s.Render(in.Body)
	if err != nil {
		return domain.Post{}, err
	}
	return s.repo.MutatePost(ctx, u, id, func(p *domain.Post) error {
		if !u.CanEdit(p.AuthorID) {
			return domain.ErrForbidden
		}
		if p.Hidden {
			return domain.ErrConflict
		}
		if p.Version != in.Version {
			return domain.ErrConflict
		}
		if p.Kind == "question" && len(selectedCategories) == 0 {
			return domain.Invalidf("questions require at least one category")
		}
		p.Title = in.Title
		p.Summary = in.Summary
		p.Body = in.Body
		p.HTML = html
		p.TagIDs = in.TagIDs
		p.CategoryIDs = selectedCategories
		p.CategoryID = nil
		if len(selectedCategories) > 0 {
			p.CategoryID = &selectedCategories[0]
		}
		return nil
	})
}
func (s *Service) Transition(ctx context.Context, u domain.User, id, action string) (domain.Post, error) {
	return s.repo.MutatePost(ctx, u, id, func(p *domain.Post) error {
		if !u.CanEdit(p.AuthorID) && !u.Moderator() {
			return domain.ErrForbidden
		}
		if p.Hidden {
			return domain.ErrConflict
		}
		next, err := domain.Transition(p.Kind, p.Status, action)
		if err != nil {
			return err
		}
		p.Status = next
		if action == "reopen" {
			p.AcceptedAnswerID = nil
			p.DuplicateOf = nil
		}
		return nil
	})
}
func (s *Service) Moderate(ctx context.Context, u domain.User, id string, hidden bool) (domain.Post, error) {
	if !u.Moderator() {
		return domain.Post{}, domain.ErrForbidden
	}
	return s.repo.MutatePost(ctx, u, id, func(p *domain.Post) error { p.Hidden = hidden; return nil })
}
func (s *Service) Delete(ctx context.Context, u domain.User, id string) error {
	_, err := s.repo.MutatePost(ctx, u, id, func(p *domain.Post) error {
		if p.AuthorID != u.ID && u.Role != "admin" {
			return domain.ErrForbidden
		}
		if p.Deleted {
			return domain.ErrNotFound
		}
		p.Deleted = true
		p.Hidden = true
		return nil
	})
	return err
}
func (s *Service) Answer(ctx context.Context, u domain.User, id, body string) (domain.Answer, error) {
	body = strings.TrimSpace(body)
	if len(body) < 10 || len(body) > 200000 {
		return domain.Answer{}, domain.Invalidf("body_markdown must contain 10 to 200000 bytes")
	}
	html, err := s.Render(body)
	if err != nil {
		return domain.Answer{}, err
	}
	return s.repo.CreateAnswer(ctx, u, id, body, html)
}
func (s *Service) Answers(ctx context.Context, u domain.User, id string, limit, offset int) ([]domain.Answer, error) {
	return s.repo.ListAnswers(ctx, u, id, limit, offset)
}
func (s *Service) Accept(ctx context.Context, u domain.User, id, answerID string) (domain.Post, error) {
	return s.repo.AcceptAnswer(ctx, u, id, answerID)
}
func (s *Service) Revisions(ctx context.Context, u domain.User, id string, limit, offset int) ([]map[string]any, error) {
	return s.repo.Revisions(ctx, u, id, limit, offset)
}
