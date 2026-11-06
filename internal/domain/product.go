package domain

import "time"

type Category struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Slug        string `json:"slug"`
	Description string `json:"description"`
	Active      bool   `json:"active"`
}
type TagInfo struct {
	ID           string   `json:"id"`
	Name         string   `json:"name"`
	Slug         string   `json:"slug"`
	Description  string   `json:"description"`
	Status       string   `json:"status"`
	Aliases      []string `json:"aliases"`
	Usage        int      `json:"usage_count"`
	CanonicalID  *string  `json:"canonical_id"`
	CategoryID   *string  `json:"category_id"`
	CategoryName string   `json:"category_name"`
}
type TagAlias struct {
	Alias string `json:"alias"`
	TagID string `json:"tag_id"`
}
type Comment struct {
	ID         string    `json:"id"`
	ParentID   *string   `json:"parent_id"`
	TargetType string    `json:"target_type"`
	TargetID   string    `json:"target_id"`
	AuthorID   string    `json:"author_id"`
	AuthorName string    `json:"author_name"`
	Body       string    `json:"body"`
	Hidden     bool      `json:"hidden"`
	CreatedAt  time.Time `json:"created_at"`
}
type CommunitySubmission struct {
	ID        string            `json:"id"`
	Kind      string            `json:"kind"`
	Mode      string            `json:"mode"`
	AuthorID  string            `json:"author_id"`
	Author    string            `json:"author_name"`
	People    []CommunityPerson `json:"people"`
	Message   string            `json:"message"`
	Status    string            `json:"status"`
	CreatedAt time.Time         `json:"created_at"`
}
type CommunityPerson struct {
	ID          string `json:"id"`
	DisplayName string `json:"display_name"`
}
type Notification struct {
	ID         string    `json:"id"`
	EventType  string    `json:"event_type"`
	Title      string    `json:"title"`
	TargetType string    `json:"target_type"`
	TargetID   string    `json:"target_id"`
	Read       bool      `json:"read"`
	CreatedAt  time.Time `json:"created_at"`
}
type Report struct {
	ID             string    `json:"id"`
	ReporterID     string    `json:"reporter_id"`
	TargetType     string    `json:"target_type"`
	TargetID       string    `json:"target_id"`
	Reason         string    `json:"reason"`
	Details        string    `json:"details"`
	Status         string    `json:"status"`
	ResolutionNote string    `json:"resolution_note"`
	CreatedAt      time.Time `json:"created_at"`
}
type RewardRule struct {
	EventType  string `json:"event_type"`
	RewardKind string `json:"reward_kind"`
	Points     int    `json:"points"`
	Enabled    bool   `json:"enabled"`
	DailyCap   int    `json:"daily_cap"`
	Version    int    `json:"version"`
}
type LedgerEntry struct {
	ID         int64     `json:"id"`
	EventType  string    `json:"event_type"`
	Delta      int       `json:"delta"`
	SourceType string    `json:"source_type"`
	SourceID   *string   `json:"source_id"`
	CreatedAt  time.Time `json:"created_at"`
}

type CreditLedgerEntry struct {
	ID         int64     `json:"id"`
	EventType  string    `json:"event_type"`
	Delta      float64   `json:"delta"`
	SourceType string    `json:"source_type"`
	SourceID   string    `json:"source_id,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
}
type Profile struct {
	ID          string   `json:"id"`
	DisplayName string   `json:"display_name"`
	Bio         string   `json:"bio"`
	Role        string   `json:"role,omitempty"`
	Active      bool     `json:"active,omitempty"`
	Points      int      `json:"points"`
	Answers     int      `json:"answers_count"`
	Badges      []string `json:"badges"`
	Tags        []string `json:"expertise_tags"`
	Posts       []Post   `json:"posts"`
}
type SearchEvent struct {
	Query       string
	Hash        []byte
	ResultCount int
}
type Media struct {
	ID          string    `json:"id"`
	PostID      string    `json:"post_id"`
	Filename    string    `json:"filename"`
	ContentType string    `json:"content_type"`
	ByteSize    int       `json:"byte_size"`
	Width       int       `json:"width"`
	Height      int       `json:"height"`
	CreatedAt   time.Time `json:"created_at"`
	Data        []byte    `json:"-"`
}
type ReportInput struct {
	TargetType string `json:"target_type"`
	TargetID   string `json:"target_id"`
	Reason     string `json:"reason"`
	Details    string `json:"details"`
}
type VoteSummary struct {
	Helpful int  `json:"helpful"`
	Voted   bool `json:"voted"`
}
type Analytics struct {
	Users               int `json:"users"`
	ActiveUsers         int `json:"active_users"`
	Blogs               int `json:"blogs"`
	Questions           int `json:"questions"`
	SolvedQuestions     int `json:"solved_questions"`
	PendingReports      int `json:"pending_reports"`
	PendingTagProposals int `json:"pending_tag_proposals"`
	Searches            int `json:"searches"`
	ZeroResultSearches  int `json:"zero_result_searches"`
}
