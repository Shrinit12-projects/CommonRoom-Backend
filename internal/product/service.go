package product

import (
	"bytes"
	"context"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"math"
	"mime"
	"net/http"
	"regexp"
	"strings"
	"unicode/utf8"

	"company-community/backend/internal/domain"
)

type Repository interface {
	Categories(context.Context, string, bool, int, int) ([]domain.Category, error)
	SaveCategory(context.Context, domain.User, domain.Category) (domain.Category, error)
	SetCategory(context.Context, domain.User, string, bool) (domain.Category, error)
	Profile(context.Context, domain.User, string) (domain.Profile, error)
	UpdateProfile(context.Context, domain.User, string, string) (domain.Profile, error)
	People(context.Context, domain.User, domain.Filter) ([]domain.Profile, error)
	Comment(context.Context, domain.User, string, string, string) (domain.Comment, error)
	ReplyComment(context.Context, domain.User, string, string) (domain.Comment, error)
	Comments(context.Context, domain.User, string, string, int, int) ([]domain.Comment, error)
	SetCommentHidden(context.Context, domain.User, string, bool) (domain.Comment, error)
	SetAnswerHidden(context.Context, domain.User, string, bool) (domain.Answer, error)
	Vote(context.Context, domain.User, string, string, bool) (domain.VoteSummary, error)
	Bookmark(context.Context, domain.User, string, bool) error
	Bookmarks(context.Context, domain.User, int, int) ([]domain.Post, error)
	FollowTag(context.Context, domain.User, string, bool) error
	FollowedTags(context.Context, domain.User, int, int) ([]domain.Tag, error)
	Notifications(context.Context, domain.User, int, int) ([]domain.Notification, error)
	MarkNotification(context.Context, domain.User, string) error
	MarkAllNotifications(context.Context, domain.User) error
	Points(context.Context, domain.User, int, int) (int, []domain.LedgerEntry, error)
	Credits(context.Context, domain.User, int, int) (float64, []domain.CreditLedgerEntry, error)
	Leaderboard(context.Context, domain.User, string, int, int) ([]domain.Profile, error)
	RewardRules(context.Context, domain.User) ([]domain.RewardRule, error)
	SetRewardRule(context.Context, domain.User, domain.RewardRule) (domain.RewardRule, error)
	RequestRedemption(context.Context, domain.User, float64, string) (map[string]any, error)
	Redemptions(context.Context, domain.User, int, int) ([]map[string]any, error)
	ReviewRedemption(context.Context, domain.User, string, string) (map[string]any, error)
	SubmitReport(context.Context, domain.User, domain.ReportInput) (domain.Report, error)
	Reports(context.Context, domain.User, string, int, int) ([]domain.Report, error)
	ReviewReport(context.Context, domain.User, string, string, string) (domain.Report, error)
	AdminUsers(context.Context, domain.User, string, int, int) ([]domain.User, error)
	TagDetail(context.Context, domain.User, string) (domain.TagInfo, error)
	MergeTag(context.Context, domain.User, string, string) (domain.Tag, error)
	SetTagAlias(context.Context, domain.User, string, string) (domain.Tag, error)
	ContentEditorial(context.Context, domain.User, string, string, bool) (domain.Post, error)
	SetDuplicate(context.Context, domain.User, string, string) (domain.Post, error)
	Analytics(context.Context, domain.User) (domain.Analytics, error)
	UploadMedia(context.Context, domain.User, string, string, string, []byte, int, int) (domain.Media, error)
	GetMedia(context.Context, domain.User, string) (domain.Media, error)
	SubmitCommunity(context.Context, domain.User, string, string, []string, string) (domain.CommunitySubmission, error)
	CommunitySubmissions(context.Context, domain.User, string, bool, int, int) ([]domain.CommunitySubmission, error)
	ReviewCommunity(context.Context, domain.User, string, string) (domain.CommunitySubmission, error)
}

type Service struct{ repo Repository }

func New(repo Repository) *Service { return &Service{repo: repo} }
func (s *Service) Categories(ctx context.Context, u domain.User, all bool, limit, offset int) ([]domain.Category, error) {
	if all && !u.Moderator() {
		return nil, domain.ErrForbidden
	}
	return s.repo.Categories(ctx, u.OrganizationID, all, limit, offset)
}
func (s *Service) ReplyComment(ctx context.Context, u domain.User, id, body string) (domain.Comment, error) {
	body = strings.TrimSpace(body)
	if len(body) < 2 || len(body) > 4000 || !domain.ValidID(id) {
		return domain.Comment{}, domain.Invalidf("parent comment or body is invalid")
	}
	return s.repo.ReplyComment(ctx, u, id, body)
}
func (s *Service) SubmitCommunity(ctx context.Context, u domain.User, kind, mode string, people []string, message string) (domain.CommunitySubmission, error) {
	message = strings.TrimSpace(message)
	if kind != "feedback" && kind != "appreciation" || kind == "feedback" && mode != "request" && mode != "give" || kind == "appreciation" && mode != "give" || len(message) < 10 || len(message) > 5000 || len(people) < 1 || len(people) > 20 {
		return domain.CommunitySubmission{}, domain.Invalidf("submission type, recipients, or message is invalid")
	}
	seen := map[string]bool{}
	for _, id := range people {
		if !domain.ValidID(id) || seen[id] || id == u.ID {
			return domain.CommunitySubmission{}, domain.Invalidf("choose distinct teammates other than yourself")
		}
		seen[id] = true
	}
	return s.repo.SubmitCommunity(ctx, u, kind, mode, people, message)
}
func (s *Service) CommunitySubmissions(ctx context.Context, u domain.User, kind string, pending bool, limit, offset int) ([]domain.CommunitySubmission, error) {
	if pending && u.Role != "admin" {
		return nil, domain.ErrForbidden
	}
	if kind != "feedback" && kind != "appreciation" {
		return nil, domain.Invalidf("kind must be feedback or appreciation")
	}
	return s.repo.CommunitySubmissions(ctx, u, kind, pending, limit, offset)
}
func (s *Service) ReviewCommunity(ctx context.Context, u domain.User, id, decision string) (domain.CommunitySubmission, error) {
	if u.Role != "admin" {
		return domain.CommunitySubmission{}, domain.ErrForbidden
	}
	if decision != "approved" && decision != "rejected" {
		return domain.CommunitySubmission{}, domain.Invalidf("decision must be approved or rejected")
	}
	return s.repo.ReviewCommunity(ctx, u, id, decision)
}
func (s *Service) SaveCategory(ctx context.Context, u domain.User, name, slug, description string) (domain.Category, error) {
	if !u.Moderator() {
		return domain.Category{}, domain.ErrForbidden
	}
	name = strings.TrimSpace(name)
	slug = strings.ToLower(strings.TrimSpace(slug))
	description = strings.TrimSpace(description)
	if utf8.RuneCountInString(name) < 2 || utf8.RuneCountInString(name) > 80 || len(slug) > 80 || len(description) > 2000 || !slugPattern.MatchString(slug) {
		return domain.Category{}, domain.Invalidf("category name, slug or description is invalid")
	}
	return s.repo.SaveCategory(ctx, u, domain.Category{Name: name, Slug: slug, Description: description, Active: true})
}
func (s *Service) SetCategory(ctx context.Context, u domain.User, id string, active bool) (domain.Category, error) {
	if !u.Moderator() {
		return domain.Category{}, domain.ErrForbidden
	}
	return s.repo.SetCategory(ctx, u, id, active)
}
func (s *Service) Profile(ctx context.Context, u domain.User, id string) (domain.Profile, error) {
	return s.repo.Profile(ctx, u, id)
}
func (s *Service) UpdateProfile(ctx context.Context, u domain.User, name, bio string) (domain.Profile, error) {
	name = strings.TrimSpace(name)
	bio = strings.TrimSpace(bio)
	if utf8.RuneCountInString(name) < 1 || utf8.RuneCountInString(name) > 100 || len(bio) > 1200 {
		return domain.Profile{}, domain.Invalidf("display_name must be 1 to 100 characters and bio at most 1200 bytes")
	}
	return s.repo.UpdateProfile(ctx, u, name, bio)
}
func (s *Service) People(ctx context.Context, u domain.User, f domain.Filter) ([]domain.Profile, error) {
	return s.repo.People(ctx, u, f)
}
func (s *Service) Comment(ctx context.Context, u domain.User, targetType, targetID, body string) (domain.Comment, error) {
	body = strings.TrimSpace(body)
	if !domain.ValidID(targetID) || targetType != "post" && targetType != "answer" || utf8.RuneCountInString(body) < 2 || utf8.RuneCountInString(body) > 4000 {
		return domain.Comment{}, domain.Invalidf("comment target or body is invalid")
	}
	return s.repo.Comment(ctx, u, targetType, targetID, body)
}
func (s *Service) Comments(ctx context.Context, u domain.User, targetType, targetID string, limit, offset int) ([]domain.Comment, error) {
	if !domain.ValidID(targetID) || targetType != "post" && targetType != "answer" {
		return nil, domain.Invalidf("comment target is invalid")
	}
	return s.repo.Comments(ctx, u, targetType, targetID, limit, offset)
}
func (s *Service) SetCommentHidden(ctx context.Context, u domain.User, id string, hidden bool) (domain.Comment, error) {
	if !u.Moderator() {
		return domain.Comment{}, domain.ErrForbidden
	}
	return s.repo.SetCommentHidden(ctx, u, id, hidden)
}
func (s *Service) SetAnswerHidden(ctx context.Context, u domain.User, id string, hidden bool) (domain.Answer, error) {
	if !u.Moderator() {
		return domain.Answer{}, domain.ErrForbidden
	}
	if !domain.ValidID(id) {
		return domain.Answer{}, domain.Invalidf("answer id must be a UUID")
	}
	return s.repo.SetAnswerHidden(ctx, u, id, hidden)
}
func (s *Service) Vote(ctx context.Context, u domain.User, targetType, targetID string, helpful bool) (domain.VoteSummary, error) {
	if !domain.ValidID(targetID) || targetType != "post" && targetType != "answer" {
		return domain.VoteSummary{}, domain.Invalidf("vote target is invalid")
	}
	return s.repo.Vote(ctx, u, targetType, targetID, helpful)
}
func (s *Service) Bookmark(ctx context.Context, u domain.User, id string, enabled bool) error {
	if !domain.ValidID(id) {
		return domain.Invalidf("post id must be a UUID")
	}
	return s.repo.Bookmark(ctx, u, id, enabled)
}
func (s *Service) Bookmarks(ctx context.Context, u domain.User, limit, offset int) ([]domain.Post, error) {
	return s.repo.Bookmarks(ctx, u, limit, offset)
}
func (s *Service) FollowTag(ctx context.Context, u domain.User, id string, enabled bool) error {
	if !domain.ValidID(id) {
		return domain.Invalidf("tag id must be a UUID")
	}
	return s.repo.FollowTag(ctx, u, id, enabled)
}
func (s *Service) FollowedTags(ctx context.Context, u domain.User, limit, offset int) ([]domain.Tag, error) {
	return s.repo.FollowedTags(ctx, u, limit, offset)
}
func (s *Service) Notifications(ctx context.Context, u domain.User, limit, offset int) ([]domain.Notification, error) {
	return s.repo.Notifications(ctx, u, limit, offset)
}
func (s *Service) MarkNotification(ctx context.Context, u domain.User, id string) error {
	if !domain.ValidID(id) {
		return domain.Invalidf("notification id must be a UUID")
	}
	return s.repo.MarkNotification(ctx, u, id)
}
func (s *Service) MarkAllNotifications(ctx context.Context, u domain.User) error {
	return s.repo.MarkAllNotifications(ctx, u)
}
func (s *Service) Points(ctx context.Context, u domain.User, limit, offset int) (int, []domain.LedgerEntry, error) {
	return s.repo.Points(ctx, u, limit, offset)
}
func (s *Service) Credits(ctx context.Context, u domain.User, limit, offset int) (float64, []domain.CreditLedgerEntry, error) {
	return s.repo.Credits(ctx, u, limit, offset)
}
func (s *Service) Leaderboard(ctx context.Context, u domain.User, period string, limit, offset int) ([]domain.Profile, error) {
	if period != "week" && period != "month" && period != "all" {
		return nil, domain.Invalidf("period must be week, month, or all")
	}
	return s.repo.Leaderboard(ctx, u, period, limit, offset)
}
func (s *Service) RewardRules(ctx context.Context, u domain.User) ([]domain.RewardRule, error) {
	return s.repo.RewardRules(ctx, u)
}
func (s *Service) SetRewardRule(ctx context.Context, u domain.User, in domain.RewardRule) (domain.RewardRule, error) {
	if u.Role != "admin" {
		return domain.RewardRule{}, domain.ErrForbidden
	}
	if in.EventType == "" || in.Points < 0 || in.Points > 10000 || in.DailyCap < 0 || in.DailyCap > 100000 || in.RewardKind != "recognition" && in.RewardKind != "credit" {
		return domain.RewardRule{}, domain.Invalidf("reward rule values are invalid")
	}
	return s.repo.SetRewardRule(ctx, u, in)
}
func (s *Service) RequestRedemption(ctx context.Context, u domain.User, points float64, description string) (map[string]any, error) {
	description = strings.TrimSpace(description)
	if points <= 0 || points > 100000 || math.IsNaN(points) || math.IsInf(points, 0) || len(description) < 3 || len(description) > 200 {
		return nil, domain.Invalidf("claim must have a positive credit amount and a 3 to 200 character description")
	}
	return s.repo.RequestRedemption(ctx, u, points, description)
}
func (s *Service) Redemptions(ctx context.Context, u domain.User, limit, offset int) ([]map[string]any, error) {
	if u.Role != "admin" && u.Role != "moderator" && u.Role != "employee" {
		return nil, domain.ErrForbidden
	}
	return s.repo.Redemptions(ctx, u, limit, offset)
}
func (s *Service) ReviewRedemption(ctx context.Context, u domain.User, id, decision string) (map[string]any, error) {
	if u.Role != "admin" {
		return nil, domain.ErrForbidden
	}
	if decision != "approved" && decision != "rejected" && decision != "fulfilled" {
		return nil, domain.Invalidf("decision must be approved, rejected, or fulfilled")
	}
	return s.repo.ReviewRedemption(ctx, u, id, decision)
}
func (s *Service) SubmitReport(ctx context.Context, u domain.User, in domain.ReportInput) (domain.Report, error) {
	in.Details = strings.TrimSpace(in.Details)
	if !domain.ValidID(in.TargetID) || in.TargetType != "post" && in.TargetType != "answer" && in.TargetType != "comment" {
		return domain.Report{}, domain.Invalidf("report target is invalid")
	}
	switch in.Reason {
	case "spam", "abuse", "confidential_data", "incorrect", "duplicate", "other":
	default:
		return domain.Report{}, domain.Invalidf("report reason is invalid")
	}
	if len(in.Details) > 2000 {
		return domain.Report{}, domain.Invalidf("report details are too long")
	}
	return s.repo.SubmitReport(ctx, u, in)
}
func (s *Service) Reports(ctx context.Context, u domain.User, status string, limit, offset int) ([]domain.Report, error) {
	if !u.Moderator() {
		return nil, domain.ErrForbidden
	}
	if status != "" && status != "pending" && status != "resolved" && status != "dismissed" {
		return nil, domain.Invalidf("report status is invalid")
	}
	return s.repo.Reports(ctx, u, status, limit, offset)
}
func (s *Service) ReviewReport(ctx context.Context, u domain.User, id, decision, note string) (domain.Report, error) {
	if !u.Moderator() {
		return domain.Report{}, domain.ErrForbidden
	}
	if decision != "resolved" && decision != "dismissed" || len(note) > 2000 {
		return domain.Report{}, domain.Invalidf("report decision or note is invalid")
	}
	return s.repo.ReviewReport(ctx, u, id, decision, note)
}
func (s *Service) AdminUsers(ctx context.Context, u domain.User, q string, limit, offset int) ([]domain.User, error) {
	if u.Role != "admin" {
		return nil, domain.ErrForbidden
	}
	return s.repo.AdminUsers(ctx, u, q, limit, offset)
}
func (s *Service) TagDetail(ctx context.Context, u domain.User, id string) (domain.TagInfo, error) {
	if !domain.ValidID(id) {
		return domain.TagInfo{}, domain.Invalidf("tag id must be a UUID")
	}
	return s.repo.TagDetail(ctx, u, id)
}
func (s *Service) MergeTag(ctx context.Context, u domain.User, source, target string) (domain.Tag, error) {
	if !u.Moderator() {
		return domain.Tag{}, domain.ErrForbidden
	}
	if !domain.ValidID(source) || !domain.ValidID(target) || source == target {
		return domain.Tag{}, domain.Invalidf("distinct source and target tag UUIDs are required")
	}
	return s.repo.MergeTag(ctx, u, source, target)
}
func (s *Service) SetTagAlias(ctx context.Context, u domain.User, id, alias string) (domain.Tag, error) {
	if !u.Moderator() {
		return domain.Tag{}, domain.ErrForbidden
	}
	alias = strings.TrimSpace(alias)
	if len(alias) < 2 || len(alias) > 60 {
		return domain.Tag{}, domain.Invalidf("alias must be 2 to 60 characters")
	}
	return s.repo.SetTagAlias(ctx, u, id, alias)
}
func (s *Service) Editorial(ctx context.Context, u domain.User, id, action string, enabled bool) (domain.Post, error) {
	if !u.Moderator() {
		return domain.Post{}, domain.ErrForbidden
	}
	if action != "featured" && action != "verified" {
		return domain.Post{}, domain.Invalidf("editorial action is invalid")
	}
	return s.repo.ContentEditorial(ctx, u, id, action, enabled)
}
func (s *Service) Duplicate(ctx context.Context, u domain.User, id, target string) (domain.Post, error) {
	if !domain.ValidID(id) || !domain.ValidID(target) || id == target {
		return domain.Post{}, domain.Invalidf("distinct question UUIDs are required")
	}
	return s.repo.SetDuplicate(ctx, u, id, target)
}
func (s *Service) Analytics(ctx context.Context, u domain.User) (domain.Analytics, error) {
	if u.Role != "admin" {
		return domain.Analytics{}, domain.ErrForbidden
	}
	return s.repo.Analytics(ctx, u)
}
func (s *Service) UploadMedia(ctx context.Context, u domain.User, postID, filename string, data []byte) (domain.Media, error) {
	if !domain.ValidID(postID) || len(data) == 0 || len(data) > 5<<20 {
		return domain.Media{}, domain.Invalidf("media requires a post UUID and file up to 5 MiB")
	}
	contentType := http.DetectContentType(data)
	exts, _ := mime.ExtensionsByType(contentType)
	if contentType != "image/png" && contentType != "image/jpeg" && contentType != "image/gif" {
		return domain.Media{}, domain.Invalidf("only PNG, JPEG, and GIF images are supported")
	}
	if len(exts) == 0 {
		return domain.Media{}, fmt.Errorf("unsupported image extension")
	}
	width, height, err := imageDimensions(contentType, data)
	if err != nil {
		return domain.Media{}, domain.Invalidf("uploaded image is corrupt")
	}
	if width < 1 || height < 1 || width > 12000 || height > 12000 || int64(width)*int64(height) > 30000000 {
		return domain.Media{}, domain.Invalidf("image dimensions exceed 30 megapixels")
	}
	filename = strings.TrimSpace(strings.ReplaceAll(strings.ReplaceAll(filename, "\\", "/"), "/", ""))
	if len(filename) > 160 {
		filename = filename[:160]
	}
	if filename == "" {
		filename = "image"
	}
	return s.repo.UploadMedia(ctx, u, postID, filename, contentType, data, width, height)
}
func (s *Service) GetMedia(ctx context.Context, u domain.User, id string) (domain.Media, error) {
	if !domain.ValidID(id) {
		return domain.Media{}, domain.Invalidf("media id must be a UUID")
	}
	return s.repo.GetMedia(ctx, u, id)
}

var slugPattern = mustCompileSlug()

func mustCompileSlug() *regexp.Regexp { return regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`) }
func imageDimensions(contentType string, data []byte) (int, int, error) {
	config, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return 0, 0, err
	}
	expected := map[string]string{"image/png": "png", "image/jpeg": "jpeg", "image/gif": "gif"}[contentType]
	if expected == "" || expected != format {
		return 0, 0, fmt.Errorf("image type mismatch")
	}
	return config.Width, config.Height, nil
}
