package content

import (
	"context"
	"errors"
	"strings"
	"testing"

	"company-community/backend/internal/domain"
)

type memoryPosts struct {
	Repository
	p domain.Post
}

func (m *memoryPosts) GetPost(context.Context, string) (domain.Post, error) { return m.p, nil }
func (m *memoryPosts) CreatePost(_ context.Context, u domain.User, p domain.Post) (domain.Post, error) {
	p.AuthorID = u.ID
	m.p = p
	return p, nil
}
func (m *memoryPosts) MutatePost(_ context.Context, _ domain.User, _ string, fn func(*domain.Post) error) (domain.Post, error) {
	copy := m.p
	if err := fn(&copy); err != nil {
		return domain.Post{}, err
	}
	m.p = copy
	return copy, nil
}
func TestDraftAndHiddenContentVisibility(t *testing.T) {
	repo := &memoryPosts{p: domain.Post{ID: "post", Kind: "blog", AuthorID: "owner", Status: "draft"}}
	service := New(repo)
	for _, tt := range []struct {
		name   string
		user   domain.User
		hidden bool
		want   error
	}{
		{name: "owner draft", user: domain.User{ID: "owner"}},
		{name: "other employee draft", user: domain.User{ID: "reader"}, want: domain.ErrNotFound},
		{name: "moderator draft", user: domain.User{ID: "moderator", Role: "moderator"}},
		{name: "owner hidden", user: domain.User{ID: "owner"}, hidden: true, want: domain.ErrNotFound},
	} {
		t.Run(tt.name, func(t *testing.T) {
			repo.p.Hidden = tt.hidden
			_, err := service.Get(context.Background(), tt.user, "post")
			if !errors.Is(err, tt.want) {
				t.Fatalf("Get()=%v, want %v", err, tt.want)
			}
		})
	}
}
func TestUpdatesRejectAnotherAuthorAndStaleVersion(t *testing.T) {
	repo := &memoryPosts{p: domain.Post{AuthorID: "owner", Kind: "blog", Status: "draft", Version: 2, Title: "Original"}}
	service := New(repo)
	in := domain.PostInput{Title: "Updated", Body: "Meaningful body content", TagIDs: []string{"11111111-1111-4111-8111-111111111111"}, Version: 1}
	if _, err := service.Update(context.Background(), domain.User{ID: "other", Role: "admin"}, "post", in); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("another author update = %v", err)
	}
	if _, err := service.Update(context.Background(), domain.User{ID: "owner"}, "post", in); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("stale update = %v", err)
	}
	if repo.p.Title != "Original" {
		t.Fatal("failed update changed content")
	}
	in.Version = 2
	if _, err := service.Update(context.Background(), domain.User{ID: "owner"}, "post", in); err != nil {
		t.Fatal(err)
	}
}
func TestMarkdownRenderingRejectsExecutableHTML(t *testing.T) {
	service := New(nil)
	html, err := service.Render("# Heading\n\n<script>alert(1)</script>\n\n[bad](javascript:alert(1))\n\n<img src=x onerror=alert(1)>\n\n```go\nfmt.Println(1)\n```\n\n| A | B |\n| - | - |\n| 1 | 2 |")
	if err != nil {
		t.Fatal(err)
	}
	for _, unsafe := range []string{"<script", "javascript:", "onerror="} {
		if strings.Contains(strings.ToLower(html), unsafe) {
			t.Fatalf("unsafe rendering: %s", html)
		}
	}
	for _, expected := range []string{"<h1>", "<pre>", "<table>"} {
		if !strings.Contains(html, expected) {
			t.Fatalf("missing rich content %s: %s", expected, html)
		}
	}
}

func TestQuestionCanBeCreatedWithoutSeparateTitle(t *testing.T) {
	repo := &memoryPosts{}
	service := New(repo)
	p, err := service.Create(context.Background(), domain.User{ID: "author"}, "question", domain.PostInput{
		Body:       "How can I trace a slow PostgreSQL query?\n\nI have checked the application logs.",
		TagIDs:     []string{"11111111-1111-4111-8111-111111111111"},
		CategoryID: "22222222-2222-4222-8222-222222222222",
	})
	if err != nil {
		t.Fatal(err)
	}
	if p.Title != "How can I trace a slow PostgreSQL query? I have checked the application logs." {
		t.Fatalf("derived title = %q", p.Title)
	}
}
