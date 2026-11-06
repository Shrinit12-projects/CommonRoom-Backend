package search

import (
	"context"
	"strings"

	"company-community/backend/internal/domain"
)

type Repository interface {
	Search(context.Context, domain.User, domain.Filter) ([]domain.SearchResult, error)
}
type Service struct{ repo Repository }

func New(repo Repository) *Service { return &Service{repo} }
func (s *Service) Search(ctx context.Context, u domain.User, f domain.Filter) ([]domain.SearchResult, error) {
	f.Query = strings.TrimSpace(f.Query)
	if len(f.Query) < 2 || len(f.Query) > 200 {
		return nil, domain.Invalidf("q must contain 2 to 200 bytes")
	}
	return s.repo.Search(ctx, u, f)
}
