package product

import (
	"context"
	"errors"
	"testing"

	"company-community/backend/internal/domain"
)

type submissionsRepo struct {
	Repository
	called bool
}

func (r *submissionsRepo) CommunitySubmissions(context.Context, domain.User, string, bool, int, int) ([]domain.CommunitySubmission, error) {
	r.called = true
	return []domain.CommunitySubmission{}, nil
}
func (r *submissionsRepo) ReviewCommunity(context.Context, domain.User, string, string) (domain.CommunitySubmission, error) {
	r.called = true
	return domain.CommunitySubmission{}, nil
}

func TestCommunitySubmissionsRequireAdminApproval(t *testing.T) {
	repo := &submissionsRepo{}
	svc := New(repo)
	if _, err := svc.CommunitySubmissions(context.Background(), domain.User{Role: "moderator"}, "feedback", true, 10, 0); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("moderator pending review error = %v", err)
	}
	if repo.called {
		t.Fatal("non-admin reached the pending submissions repository")
	}
	if _, err := svc.ReviewCommunity(context.Background(), domain.User{Role: "moderator"}, "id", "approved"); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("moderator decision error = %v", err)
	}
	if repo.called {
		t.Fatal("non-admin reached the review repository")
	}
	if _, err := svc.CommunitySubmissions(context.Background(), domain.User{Role: "admin"}, "appreciation", true, 10, 0); err != nil {
		t.Fatal(err)
	}
	if !repo.called {
		t.Fatal("admin could not read approval queue")
	}
}
