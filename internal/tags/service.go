package tags

import (
	"context"

	"company-community/backend/internal/domain"
)

type Repository interface {
	Tags(context.Context, string, int, int) ([]domain.Tag, error)
	CreateTag(context.Context, domain.User, domain.TagInput) (domain.Tag, error)
	ProposeTag(context.Context, domain.User, domain.TagInput) (domain.Proposal, error)
	Proposals(context.Context, domain.User, int, int) ([]domain.Proposal, error)
	ReviewProposal(context.Context, domain.User, string, string) (domain.Proposal, error)
	SetTagStatus(context.Context, domain.User, string, string) (domain.Tag, error)
}
type Service struct{ repo Repository }

func New(repo Repository) *Service { return &Service{repo} }
func (s *Service) List(ctx context.Context, u domain.User, limit, offset int) ([]domain.Tag, error) {
	return s.repo.Tags(ctx, u.OrganizationID, limit, offset)
}
func (s *Service) Create(ctx context.Context, u domain.User, in domain.TagInput) (domain.Tag, error) {
	if !u.Moderator() {
		return domain.Tag{}, domain.ErrForbidden
	}
	if err := in.Validate(); err != nil {
		return domain.Tag{}, err
	}
	return s.repo.CreateTag(ctx, u, in)
}
func (s *Service) Propose(ctx context.Context, u domain.User, in domain.TagInput) (domain.Proposal, error) {
	if err := in.Validate(); err != nil {
		return domain.Proposal{}, err
	}
	if in.Reason == "" {
		return domain.Proposal{}, domain.Invalidf("reason is required")
	}
	return s.repo.ProposeTag(ctx, u, in)
}
func (s *Service) Proposals(ctx context.Context, u domain.User, limit, offset int) ([]domain.Proposal, error) {
	return s.repo.Proposals(ctx, u, limit, offset)
}
func (s *Service) Review(ctx context.Context, u domain.User, id, decision string) (domain.Proposal, error) {
	if !u.Moderator() {
		return domain.Proposal{}, domain.ErrForbidden
	}
	if decision != "approved" && decision != "rejected" {
		return domain.Proposal{}, domain.Invalidf("decision must be approved or rejected")
	}
	return s.repo.ReviewProposal(ctx, u, id, decision)
}
func (s *Service) SetStatus(ctx context.Context, u domain.User, id, status string) (domain.Tag, error) {
	if !u.Moderator() {
		return domain.Tag{}, domain.ErrForbidden
	}
	if status != "active" && status != "deprecated" {
		return domain.Tag{}, domain.Invalidf("status must be active or deprecated")
	}
	return s.repo.SetTagStatus(ctx, u, id, status)
}
