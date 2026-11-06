package domain

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

var (
	ErrNotFound  = errors.New("resource not found")
	ErrForbidden = errors.New("permission denied")
	ErrConflict  = errors.New("resource conflicts with current state")
)

type Invalid struct{ Message string }

func (e Invalid) Error() string                 { return e.Message }
func Invalidf(format string, args ...any) error { return Invalid{fmt.Sprintf(format, args...)} }

type User struct {
	ID               string `json:"id"`
	OrganizationID   string `json:"organization_id"`
	OrganizationSlug string `json:"organization_slug,omitempty"`
	OrganizationName string `json:"organization_name,omitempty"`
	Issuer           string `json:"-"`
	Subject          string `json:"-"`
	Email            string `json:"email,omitempty"`
	DisplayName      string `json:"display_name"`
	Role             string `json:"role"`
	Active           bool   `json:"active"`
	Deleted          bool   `json:"deleted"`
}

type Organization struct {
	ID          string `json:"id"`
	Slug        string `json:"slug"`
	Name        string `json:"name"`
	EmailDomain string `json:"email_domain"`
}

type OrganizationAdminInput struct {
	Slug, Name, EmailDomain string
	AdminEmail, AdminName   string
	Issuer, Subject         string
	Salt, Hash              []byte
	Iterations              int
}

type OrganizationUserInput struct {
	OrganizationID string
	Email          string
	DisplayName    string
	Role           string
	Issuer         string
	Subject        string
	Salt           []byte
	Hash           []byte
	Iterations     int
}

type LocalCredential struct {
	User       User
	Salt       []byte
	Hash       []byte
	Iterations int
}

func (u User) Moderator() bool            { return u.Role == "moderator" || u.Role == "admin" }
func (u User) CanEdit(author string) bool { return u.ID == author }
func (u User) CanRead(p Post) bool {
	if p.OrganizationID != u.OrganizationID || p.Deleted {
		return false
	}
	return !p.Hidden && (p.Kind == "question" || p.Status == "published" || p.AuthorID == u.ID) || u.Moderator()
}

type Post struct {
	ID               string    `json:"id"`
	OrganizationID   string    `json:"-"`
	Kind             string    `json:"kind"`
	AuthorID         string    `json:"author_id"`
	AuthorName       string    `json:"author_name"`
	Title            string    `json:"title"`
	Summary          string    `json:"summary"`
	Body             string    `json:"body_markdown"`
	HTML             string    `json:"body_html"`
	Status           string    `json:"status"`
	Hidden           bool      `json:"hidden"`
	Deleted          bool      `json:"deleted"`
	TagIDs           []string  `json:"tag_ids"`
	Version          int       `json:"version"`
	AcceptedAnswerID *string   `json:"accepted_answer_id"`
	CategoryID       *string   `json:"category_id"`
	CategoryName     string    `json:"category_name,omitempty"`
	CategoryIDs      []string  `json:"category_ids"`
	Featured         bool      `json:"featured"`
	Verified         bool      `json:"verified"`
	DuplicateOf      *string   `json:"duplicate_of"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
}
type PostInput struct {
	Title       string   `json:"title"`
	Summary     string   `json:"summary"`
	Body        string   `json:"body_markdown"`
	TagIDs      []string `json:"tag_ids"`
	CategoryID  string   `json:"category_id"`
	CategoryIDs []string `json:"category_ids"`
	Version     int      `json:"version"`
}

func (p *PostInput) Validate() error {
	p.Title = strings.TrimSpace(p.Title)
	p.Summary = strings.TrimSpace(p.Summary)
	p.Body = strings.TrimSpace(p.Body)
	if utf8.RuneCountInString(p.Title) < 3 || utf8.RuneCountInString(p.Title) > 200 {
		return Invalidf("title must contain 3 to 200 characters")
	}
	if len(p.Body) < 10 || len(p.Body) > 200000 {
		return Invalidf("body_markdown must contain 10 to 200000 bytes")
	}
	if utf8.RuneCountInString(p.Summary) > 500 {
		return Invalidf("summary must not exceed 500 characters")
	}
	if len(p.TagIDs) < 1 || len(p.TagIDs) > 8 {
		return Invalidf("select 1 to 8 active tags")
	}
	seen := map[string]bool{}
	for _, id := range p.TagIDs {
		if !ValidID(id) || seen[id] {
			return Invalidf("tag_ids must contain distinct UUIDs")
		}
		seen[id] = true
	}
	return nil
}
func Transition(kind, current, action string) (string, error) {
	if kind == "blog" {
		if action == "publish" && (current == "draft" || current == "published") {
			return "published", nil
		}
		if action == "unpublish" && (current == "draft" || current == "published") {
			return "draft", nil
		}
	}
	if kind == "question" {
		if action == "close" && (current == "open" || current == "solved" || current == "closed") {
			return "closed", nil
		}
		if action == "reopen" && (current == "closed" || current == "solved" || current == "duplicate") {
			return "open", nil
		}
	}
	return "", ErrConflict
}

var idPattern = regexp.MustCompile(`^[a-fA-F0-9]{8}-[a-fA-F0-9]{4}-[a-fA-F0-9]{4}-[a-fA-F0-9]{4}-[a-fA-F0-9]{12}$`)

func ValidID(s string) bool { return idPattern.MatchString(s) }

type Answer struct {
	ID         string    `json:"id"`
	QuestionID string    `json:"question_id"`
	AuthorID   string    `json:"author_id"`
	AuthorName string    `json:"author_name"`
	Body       string    `json:"body_markdown"`
	HTML       string    `json:"body_html"`
	Hidden     bool      `json:"hidden"`
	CreatedAt  time.Time `json:"created_at"`
}
type Tag struct {
	ID          string  `json:"id"`
	Name        string  `json:"name"`
	Slug        string  `json:"slug"`
	Description string  `json:"description"`
	Status      string  `json:"status"`
	MergedInto  *string `json:"merged_into,omitempty"`
	CategoryID  *string `json:"category_id,omitempty"`
}
type TagInput struct {
	Name        string `json:"name"`
	Slug        string `json:"slug"`
	Description string `json:"description"`
	Reason      string `json:"reason"`
}

func (t *TagInput) Validate() error {
	t.Name = strings.TrimSpace(t.Name)
	t.Description = strings.TrimSpace(t.Description)
	t.Reason = strings.TrimSpace(t.Reason)
	t.Slug = strings.ToLower(strings.TrimSpace(t.Slug))
	if utf8.RuneCountInString(t.Name) < 1 || utf8.RuneCountInString(t.Name) > 60 || len(t.Slug) > 60 || !regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`).MatchString(t.Slug) {
		return Invalidf("name must contain 1 to 60 characters; slug must contain lowercase letters, digits and single hyphens (maximum 60 characters)")
	}
	if len(t.Description) > 2000 || len(t.Reason) > 2000 {
		return Invalidf("description and reason must not exceed 2000 bytes")
	}
	return nil
}

type Proposal struct {
	ID string `json:"id"`
	TagInput
	ProposerID string    `json:"proposer_id"`
	Status     string    `json:"status"`
	CreatedAt  time.Time `json:"created_at"`
}
type SearchResult struct {
	ID         string    `json:"id"`
	Kind       string    `json:"kind"`
	Title      string    `json:"title"`
	Snippet    string    `json:"snippet"`
	AuthorName string    `json:"author_name"`
	Status     string    `json:"status"`
	UpdatedAt  time.Time `json:"updated_at"`
}
type Filter struct {
	OrganizationID string
	Kind           string
	Query          string
	TagID          string
	CategoryID     string
	AuthorID       string
	Status         string
	Sort           string
	Limit          int
	Offset         int
	From           string
	To             string
}
