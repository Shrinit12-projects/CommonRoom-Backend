//go:build integration

package integration

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"testing"
	"time"

	"company-community/backend/internal/content"
	"company-community/backend/internal/database"
	"company-community/backend/internal/domain"
	"company-community/backend/internal/httpapi"
	"company-community/backend/internal/identity"
	"company-community/backend/internal/product"
	"company-community/backend/internal/search"
	"company-community/backend/internal/store"
	"company-community/backend/internal/tags"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Only this test binary uses subject-as-token authentication. The application uses
// the real OIDC verifier or the explicitly configured loopback development token.
type testVerifier struct{}

func (testVerifier) Verify(_ context.Context, token string) (identity.Principal, error) {
	return identity.Principal{Issuer: "integration", Subject: token}, nil
}

type client struct {
	t       *testing.T
	handler http.Handler
}

func (c client) call(method, path, token string, body any, want int) json.RawMessage {
	c.t.Helper()
	var input bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&input).Encode(body); err != nil {
			c.t.Fatal(err)
		}
	}
	r := httptest.NewRequest(method, path, &input)
	r.Header.Set("Content-Type", "application/json")
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	w := httptest.NewRecorder()
	c.handler.ServeHTTP(w, r)
	if w.Code != want {
		c.t.Fatalf("%s %s: status %d, want %d: %s", method, path, w.Code, want, w.Body.String())
	}
	var envelope struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &envelope); err != nil {
		c.t.Fatal(err)
	}
	return envelope.Data
}
func decode[T any](t *testing.T, raw json.RawMessage) T {
	t.Helper()
	var out T
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func setup(t *testing.T) (context.Context, *pgxpool.Pool, *store.Store, client) {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Fatal("TEST_DATABASE_URL must point to a disposable PostgreSQL database")
	}
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	var b [8]byte
	if _, err = rand.Read(b[:]); err != nil {
		t.Fatal(err)
	}
	schema := "test_" + hex.EncodeToString(b[:])
	quoted := pgx.Identifier{schema}.Sanitize()
	if _, err = conn.Exec(ctx, "CREATE SCHEMA "+quoted); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_, err := conn.Exec(cleanup, "DROP SCHEMA "+quoted+" CASCADE")
		if err != nil {
			t.Error(err)
		}
		_ = conn.Close(cleanup)
	})
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	for i := 0; i < 2; i++ {
		if err = database.Migrate(ctx, pool); err != nil {
			t.Fatal(err)
		}
	}
	repo := store.New(pool)
	for _, u := range []domain.User{{Issuer: "integration", Subject: "admin", Email: "admin@mindorigin.in", DisplayName: "Admin", Role: "admin"}, {Issuer: "integration", Subject: "author", Email: "author@mindorigin.in", DisplayName: "Author", Role: "employee"}, {Issuer: "integration", Subject: "reader", Email: "reader@mindorigin.in", DisplayName: "Reader", Role: "employee"}, {Issuer: "integration", Subject: "firstock", Email: "reader@firstock.in", DisplayName: "Firstock reader", Role: "employee"}} {
		if _, err = repo.Provision(ctx, u); err != nil {
			t.Fatal(err)
		}
	}
	api := &httpapi.API{Auth: identity.Authenticator{Verifier: testVerifier{}, Users: repo}, Content: content.New(repo), Tags: tags.New(repo), Search: search.New(repo), Admin: repo, Product: product.New(repo), Ping: pool.Ping, Logger: slog.New(slog.NewTextHandler(os.Stderr, nil))}
	return ctx, pool, repo, client{t, api.Handler()}
}

func TestOrganizationContentIsolation(t *testing.T) {
	_, _, _, c := setup(t)
	mindoriginTags := decode[struct {
		Items []domain.Tag `json:"items"`
	}](t, c.call("GET", "/v1/tags?limit=100", "author", nil, 200))
	firstockTags := decode[struct {
		Items []domain.Tag `json:"items"`
	}](t, c.call("GET", "/v1/tags?limit=100", "firstock", nil, 200))
	if len(mindoriginTags.Items) == 0 || len(firstockTags.Items) == 0 {
		t.Fatal("organization starter tags are missing")
	}
	input := map[string]any{"title": "Mindorigin confidential phrase", "body_markdown": "A cross-organization visibility test.", "summary": "", "tag_ids": []string{mindoriginTags.Items[0].ID}}
	post := decode[domain.Post](t, c.call("POST", "/v1/blogs", "author", input, 201))
	c.call("POST", "/v1/posts/"+post.ID+"/publish", "author", map[string]any{}, 200)
	other := decode[struct {
		Items []domain.Post `json:"items"`
	}](t, c.call("GET", "/v1/posts", "firstock", nil, 200))
	for _, item := range other.Items {
		if item.ID == post.ID {
			t.Fatal("Firstock feed exposed Mindorigin content")
		}
	}
	c.call("GET", "/v1/posts/"+post.ID, "firstock", nil, 404)
	c.call("POST", "/v1/posts/"+post.ID+"/publish", "firstock", map[string]any{}, 404)
	c.call("GET", "/v1/posts/"+post.ID+"/comments", "firstock", nil, 404)
	c.call("POST", "/v1/posts/"+post.ID+"/comments", "firstock", map[string]any{"body": "Cross-organization comment"}, 404)
	c.call("POST", "/v1/posts/"+post.ID+"/votes", "firstock", map[string]any{"helpful": true}, 404)
	search := decode[struct {
		Items []domain.SearchResult `json:"items"`
	}](t, c.call("GET", "/v1/search?q=confidential", "firstock", nil, 200))
	if len(search.Items) != 0 {
		t.Fatal("Firstock search exposed Mindorigin content")
	}
	c.call("GET", "/v1/people/"+post.AuthorID, "firstock", nil, 404)
	categories := decode[struct {
		Items []domain.Category `json:"items"`
	}](t, c.call("GET", "/v1/categories?limit=100", "author", nil, 200))
	if len(categories.Items) == 0 {
		t.Fatal("Mindorigin categories are missing")
	}
	question := decode[domain.Post](t, c.call("POST", "/v1/questions", "author", map[string]any{"body_markdown": "How do we keep this question private across organizations?", "category_id": categories.Items[0].ID, "tag_ids": []string{mindoriginTags.Items[0].ID}}, 201))
	answer := decode[domain.Answer](t, c.call("POST", "/v1/questions/"+question.ID+"/answers", "reader", map[string]any{"body_markdown": "Keep each request scoped to its trusted organization identity."}, 201))
	c.call("GET", "/v1/questions/"+question.ID+"/answers", "firstock", nil, 404)
	c.call("POST", "/v1/questions/"+question.ID+"/answers", "firstock", map[string]any{"body_markdown": "This answer must not cross the tenant boundary."}, 404)
	c.call("GET", "/v1/answers/"+answer.ID+"/comments", "firstock", nil, 404)
	c.call("POST", "/v1/answers/"+answer.ID+"/comments", "firstock", map[string]any{"body": "Cross-organization answer comment"}, 404)
	firstockPost := decode[domain.Post](t, c.call("POST", "/v1/blogs", "firstock", map[string]any{"title": "Firstock private phrase", "body_markdown": "A tenant isolation check.", "tag_ids": []string{firstockTags.Items[0].ID}}, 201))
	c.call("POST", "/v1/posts/"+firstockPost.ID+"/publish", "firstock", map[string]any{}, 200)
	mindorigin := decode[struct {
		Items []domain.Post `json:"items"`
	}](t, c.call("GET", "/v1/posts", "reader", nil, 200))
	for _, item := range mindorigin.Items {
		if item.ID == firstockPost.ID {
			t.Fatal("Mindorigin feed exposed Firstock content")
		}
	}
	c.call("GET", "/v1/posts/"+firstockPost.ID, "reader", nil, 404)
}

func TestCommunityFlows(t *testing.T) {
	ctx, pool, repo, c := setup(t)
	c.call("GET", "/v1/me", "", nil, 401)
	c.call("GET", "/v1/me", "not-provisioned", nil, 401)
	admin, err := repo.FindIdentity(ctx, "integration", "admin")
	if err != nil {
		t.Fatal(err)
	}
	author, err := repo.FindIdentity(ctx, "integration", "author")
	if err != nil {
		t.Fatal(err)
	}
	reader, err := repo.FindIdentity(ctx, "integration", "reader")
	if err != nil {
		t.Fatal(err)
	}
	tag := decode[domain.Tag](t, c.call("POST", "/v1/tags", "admin", domain.TagInput{Name: "Integration Go", Slug: "integration-go", Description: "Go programming for integration tests"}, 201))
	categoryPage := decode[struct {
		Items []domain.Category `json:"items"`
	}](t, c.call("GET", "/v1/categories?limit=100", "admin", nil, 200))
	var category domain.Category
	for _, candidate := range categoryPage.Items {
		if candidate.Slug == "engineering" {
			category = candidate
			break
		}
	}
	if category.ID == "" {
		t.Fatal("default engineering category is missing")
	}
	c.call("POST", "/v1/tags", "reader", domain.TagInput{Name: "Not allowed", Slug: "not-allowed"}, 403)
	input := domain.PostInput{Title: "Redis connection pools", Body: "A useful internal guide to Redis connection pools.", TagIDs: []string{tag.ID}}
	blog := decode[domain.Post](t, c.call("POST", "/v1/blogs", "author", input, 201))
	if blog.Status != "draft" || blog.Version != 1 {
		t.Fatalf("invalid new blog: %+v", blog)
	}
	c.call("GET", "/v1/posts/"+blog.ID, "reader", nil, 404)
	drafts := decode[struct {
		Items []domain.Post `json:"items"`
	}](t, c.call("GET", "/v1/posts?type=blog", "reader", nil, 200))
	if len(drafts.Items) != 0 {
		t.Fatal("reader listed another user's draft")
	}
	hiddenSearch := decode[struct {
		Items []domain.SearchResult `json:"items"`
	}](t, c.call("GET", "/v1/search?q=Redis", "reader", nil, 200))
	if len(hiddenSearch.Items) != 0 {
		t.Fatal("draft leaked through search")
	}
	input.Version = 1
	c.call("PUT", "/v1/posts/"+blog.ID, "reader", input, 404)
	published := decode[domain.Post](t, c.call("POST", "/v1/posts/"+blog.ID+"/publish", "author", nil, 200))
	c.call("GET", "/v1/posts/"+blog.ID, "reader", nil, 200)
	results := decode[struct {
		Items []domain.SearchResult `json:"items"`
	}](t, c.call("GET", "/v1/search?q=Redis&tag_id="+tag.ID, "reader", nil, 200))
	if len(results.Items) != 1 {
		t.Fatalf("published search results=%+v", results)
	}
	rootComment := decode[domain.Comment](t, c.call("POST", "/v1/posts/"+blog.ID+"/comments", "reader", map[string]string{"body": "Helpful context for the team."}, 201))
	reply := decode[domain.Comment](t, c.call("POST", "/v1/comments/"+rootComment.ID+"/replies", "author", map[string]string{"body": "Thanks for adding this note."}, 201))
	if reply.ParentID == nil || *reply.ParentID != rootComment.ID {
		t.Fatalf("comment reply parent = %v, want %s", reply.ParentID, rootComment.ID)
	}

	questionInput := domain.PostInput{Body: "How can I trace a slow PostgreSQL query?\n\nI already checked the query plan.", TagIDs: []string{tag.ID}}
	c.call("POST", "/v1/questions", "reader", questionInput, 400)
	questionInput.CategoryID = category.ID
	question := decode[domain.Post](t, c.call("POST", "/v1/questions", "reader", questionInput, 201))
	if question.Title == "" || question.Kind != "question" {
		t.Fatalf("question without a supplied title was not indexed with a derived question: %+v", question)
	}

	feedback := decode[domain.CommunitySubmission](t, c.call("POST", "/v1/feedback", "reader", map[string]any{"mode": "request", "people": []string{author.ID}, "message": "Could you review my recent project update?"}, 201))
	otherFeedback := decode[struct {
		Items []domain.CommunitySubmission `json:"items"`
	}](t, c.call("GET", "/v1/feedback?limit=20", "author", nil, 200))
	if len(otherFeedback.Items) != 0 {
		t.Fatal("feedback request was visible to its recipient before administrator approval")
	}
	c.call("GET", "/v1/feedback?pending=true", "reader", nil, 403)
	c.call("PUT", "/v1/admin/community-submissions/"+feedback.ID, "reader", map[string]string{"decision": "approved"}, 403)
	pendingFeedback := decode[struct {
		Items []domain.CommunitySubmission `json:"items"`
	}](t, c.call("GET", "/v1/feedback?pending=true&limit=20", "admin", nil, 200))
	if len(pendingFeedback.Items) != 1 {
		t.Fatalf("pending feedback submissions = %d, want 1", len(pendingFeedback.Items))
	}
	c.call("PUT", "/v1/admin/community-submissions/"+feedback.ID, "admin", map[string]string{"decision": "approved"}, 200)
	approvedFeedback := decode[struct {
		Items []domain.CommunitySubmission `json:"items"`
	}](t, c.call("GET", "/v1/feedback?limit=20", "author", nil, 200))
	if len(approvedFeedback.Items) != 1 || approvedFeedback.Items[0].ID != feedback.ID {
		t.Fatal("approved feedback was not visible to its selected participant")
	}

	appreciation := decode[domain.CommunitySubmission](t, c.call("POST", "/v1/appreciation", "reader", map[string]any{"people": []string{author.ID, admin.ID}, "message": "Thank you both for helping unblock the release."}, 201))
	publicAppreciation := decode[struct {
		Items []domain.CommunitySubmission `json:"items"`
	}](t, c.call("GET", "/v1/appreciation?limit=20", "author", nil, 200))
	if len(publicAppreciation.Items) != 0 {
		t.Fatal("appreciation became public before administrator approval")
	}
	c.call("PUT", "/v1/admin/community-submissions/"+appreciation.ID, "admin", map[string]string{"decision": "approved"}, 200)
	publicAppreciation = decode[struct {
		Items []domain.CommunitySubmission `json:"items"`
	}](t, c.call("GET", "/v1/appreciation?limit=20", "author", nil, 200))
	if len(publicAppreciation.Items) != 1 || len(publicAppreciation.Items[0].People) != 2 {
		t.Fatal("approved appreciation was not visible with all recipients")
	}
	c.call("PUT", "/v1/posts/"+blog.ID, "author", input, 409)
	input.Version = published.Version
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	service := content.New(repo)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _, err := service.Update(ctx, author, blog.ID, input); errs <- err }()
	}
	wg.Wait()
	close(errs)
	successes, conflicts := 0, 0
	for err := range errs {
		if err == nil {
			successes++
		} else if errors.Is(err, domain.ErrConflict) {
			conflicts++
		} else {
			t.Fatal(err)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("concurrent edits: successes=%d conflicts=%d", successes, conflicts)
	}
	c.call("GET", "/v1/posts/"+blog.ID+"/revisions", "reader", nil, 403)
	revisions := decode[struct {
		Items []map[string]any `json:"items"`
	}](t, c.call("GET", "/v1/posts/"+blog.ID+"/revisions", "author", nil, 200))
	if len(revisions.Items) != 3 {
		t.Fatalf("revision count=%d", len(revisions.Items))
	}
	c.call("PUT", "/v1/moderation/posts/"+blog.ID, "reader", map[string]bool{"hidden": true}, 403)
	c.call("PUT", "/v1/moderation/posts/"+blog.ID, "admin", map[string]bool{"hidden": true}, 200)
	c.call("GET", "/v1/posts/"+blog.ID, "reader", nil, 404)
	hiddenSearch = decode[struct {
		Items []domain.SearchResult `json:"items"`
	}](t, c.call("GET", "/v1/search?q=Redis", "author", nil, 200))
	if len(hiddenSearch.Items) != 0 {
		t.Fatal("hidden post leaked through search")
	}
	c.call("PUT", "/v1/moderation/posts/"+blog.ID, "admin", map[string]bool{"hidden": false}, 200)
	c.call("POST", "/v1/posts/"+blog.ID+"/unpublish", "author", nil, 200)
	c.call("GET", "/v1/posts/"+blog.ID, "reader", nil, 404)

	proposal := decode[domain.Proposal](t, c.call("POST", "/v1/tag-proposals", "author", domain.TagInput{Name: "Integration PostgreSQL", Slug: "integration-postgresql", Reason: "Database discussions"}, 201))
	c.call("PUT", "/v1/tag-proposals/"+proposal.ID+"/decision", "reader", map[string]string{"decision": "approved"}, 403)
	c.call("PUT", "/v1/tag-proposals/"+proposal.ID+"/decision", "admin", map[string]string{"decision": "approved"}, 200)
	c.call("PUT", "/v1/tag-proposals/"+proposal.ID+"/decision", "admin", map[string]string{"decision": "approved"}, 200)
	var approvedCount int
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM tags WHERE slug='integration-postgresql'`).Scan(&approvedCount); err != nil || approvedCount != 1 {
		t.Fatalf("idempotent proposal approval: count=%d err=%v", approvedCount, err)
	}

	input.Title = "How do transactions work?"
	input.CategoryID = category.ID
	input.Version = 0
	q1 := decode[domain.Post](t, c.call("POST", "/v1/questions", "author", input, 201))
	q2 := decode[domain.Post](t, c.call("POST", "/v1/questions", "author", input, 201))
	a1 := decode[domain.Answer](t, c.call("POST", "/v1/questions/"+q1.ID+"/answers", "reader", map[string]string{"body_markdown": "Use database transactions and constraints."}, 201))
	a2 := decode[domain.Answer](t, c.call("POST", "/v1/questions/"+q2.ID+"/answers", "reader", map[string]string{"body_markdown": "This answer belongs to another question."}, 201))
	c.call("PUT", "/v1/questions/"+q1.ID+"/accepted-answer", "reader", map[string]string{"answer_id": a1.ID}, 403)
	c.call("PUT", "/v1/questions/"+q1.ID+"/accepted-answer", "author", map[string]string{"answer_id": a2.ID}, 400)
	// Independently exercise the database constraint, bypassing the service.
	if _, err = pool.Exec(ctx, `UPDATE posts SET accepted_answer_id=$2,status='solved' WHERE id=$1`, q1.ID, a2.ID); err == nil {
		t.Fatal("database accepted an answer belonging to another question")
	}
	accepted := decode[domain.Post](t, c.call("PUT", "/v1/questions/"+q1.ID+"/accepted-answer", "author", map[string]string{"answer_id": a1.ID}, 200))
	again := decode[domain.Post](t, c.call("PUT", "/v1/questions/"+q1.ID+"/accepted-answer", "author", map[string]string{"answer_id": a1.ID}, 200))
	if accepted.Status != "solved" || accepted.Version != again.Version {
		t.Fatal("acceptance is not idempotent")
	}
	c.call("GET", "/v1/questions/"+q1.ID+"/answers", "author", nil, 200)
	c.call("POST", "/v1/posts/"+q1.ID+"/close", "author", nil, 200)
	c.call("POST", "/v1/questions/"+q1.ID+"/answers", "reader", map[string]string{"body_markdown": "No answers should be allowed after closing."}, 409)

	c.call("PATCH", "/v1/tags/"+tag.ID, "admin", map[string]string{"status": "deprecated"}, 200)
	c.call("POST", "/v1/blogs", "author", input, 400)
	c.call("POST", "/v1/posts/"+blog.ID+"/publish", "author", nil, 400)
	c.call("GET", "/v1/posts/"+q1.ID, "reader", nil, 200) // Deprecation preserves historical associations.
	c.call("PATCH", "/v1/admin/users/"+admin.ID, "admin", map[string]any{"role": "employee", "active": true}, 409)
	c.call("PATCH", "/v1/admin/users/"+reader.ID, "author", map[string]any{"role": "admin", "active": true}, 403)
	c.call("PATCH", "/v1/admin/users/"+reader.ID, "admin", map[string]any{"role": "employee", "active": false}, 200)
	c.call("GET", "/v1/me", "reader", nil, 401)
	c.call("GET", "/v1/admin/audit-logs", "author", nil, 403)
	logs := decode[struct {
		Items []map[string]any `json:"items"`
	}](t, c.call("GET", "/v1/admin/audit-logs", "admin", nil, 200))
	if len(logs.Items) < 7 {
		t.Fatal("administrative actions are missing audit records")
	}
}
