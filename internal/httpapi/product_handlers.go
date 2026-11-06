package httpapi

import (
	"io"
	"mime"
	"net/http"

	"company-community/backend/internal/domain"
)

func (a *API) productPage(r *http.Request) (domain.Filter, error) { return filters(r) }
func (a *API) productRoutes(mux *http.ServeMux, register func(string, endpoint)) {
	register("GET /v1/categories", func(r *http.Request) (any, int, error) {
		f, e := filters(r)
		if e != nil {
			return nil, 0, e
		}
		v, e := a.Product.Categories(r.Context(), user(r), user(r).Moderator(), f.Limit, f.Offset)
		return page(v, f), 200, e
	})
	register("POST /v1/categories", func(r *http.Request) (any, int, error) {
		var x struct {
			Name        string `json:"name"`
			Slug        string `json:"slug"`
			Description string `json:"description"`
		}
		if e := decode(r, &x); e != nil {
			return nil, 0, e
		}
		v, e := a.Product.SaveCategory(r.Context(), user(r), x.Name, x.Slug, x.Description)
		return v, 201, e
	})
	register("PATCH /v1/categories/{id}", func(r *http.Request) (any, int, error) {
		var x struct {
			Active *bool `json:"active"`
		}
		if e := decode(r, &x); e != nil {
			return nil, 0, e
		}
		if x.Active == nil {
			return nil, 0, domain.Invalidf("active is required")
		}
		v, e := a.Product.SetCategory(r.Context(), user(r), r.PathValue("id"), *x.Active)
		return v, 200, e
	})
	register("GET /v1/people", func(r *http.Request) (any, int, error) {
		f, e := filters(r)
		if e != nil {
			return nil, 0, e
		}
		v, e := a.Product.People(r.Context(), user(r), f)
		return page(v, f), 200, e
	})
	register("GET /v1/people/{id}", func(r *http.Request) (any, int, error) {
		v, e := a.Product.Profile(r.Context(), user(r), r.PathValue("id"))
		return v, 200, e
	})
	register("PUT /v1/me/profile", func(r *http.Request) (any, int, error) {
		var x struct {
			DisplayName string `json:"display_name"`
			Bio         string `json:"bio"`
		}
		if e := decode(r, &x); e != nil {
			return nil, 0, e
		}
		v, e := a.Product.UpdateProfile(r.Context(), user(r), x.DisplayName, x.Bio)
		return v, 200, e
	})
	register("GET /v1/me/bookmarks", func(r *http.Request) (any, int, error) {
		f, e := filters(r)
		if e != nil {
			return nil, 0, e
		}
		v, e := a.Product.Bookmarks(r.Context(), user(r), f.Limit, f.Offset)
		return page(v, f), 200, e
	})
	register("PUT /v1/posts/{id}/bookmark", func(r *http.Request) (any, int, error) {
		var x struct {
			Enabled bool `json:"enabled"`
		}
		if e := decode(r, &x); e != nil {
			return nil, 0, e
		}
		e := a.Product.Bookmark(r.Context(), user(r), r.PathValue("id"), x.Enabled)
		return map[string]bool{"bookmarked": x.Enabled}, 200, e
	})
	register("POST /v1/posts/{id}/votes", a.productVote("post"))
	register("POST /v1/answers/{id}/votes", a.productVote("answer"))
	register("GET /v1/posts/{id}/comments", a.productComments("post"))
	register("POST /v1/posts/{id}/comments", a.productComment("post"))
	register("POST /v1/comments/{id}/replies", a.productReply)
	register("GET /v1/answers/{id}/comments", a.productComments("answer"))
	register("POST /v1/answers/{id}/comments", a.productComment("answer"))
	register("GET /v1/feedback", func(r *http.Request) (any, int, error) {
		f, e := filters(r)
		if e != nil {
			return nil, 0, e
		}
		pending := r.URL.Query().Get("pending") == "true"
		v, e := a.Product.CommunitySubmissions(r.Context(), user(r), "feedback", pending, f.Limit, f.Offset)
		return page(v, f), 200, e
	})
	register("POST /v1/feedback", func(r *http.Request) (any, int, error) {
		var x struct {
			Mode    string   `json:"mode"`
			People  []string `json:"people"`
			Message string   `json:"message"`
		}
		if e := decode(r, &x); e != nil {
			return nil, 0, e
		}
		v, e := a.Product.SubmitCommunity(r.Context(), user(r), "feedback", x.Mode, x.People, x.Message)
		return v, 201, e
	})
	register("GET /v1/appreciation", func(r *http.Request) (any, int, error) {
		f, e := filters(r)
		if e != nil {
			return nil, 0, e
		}
		pending := r.URL.Query().Get("pending") == "true"
		v, e := a.Product.CommunitySubmissions(r.Context(), user(r), "appreciation", pending, f.Limit, f.Offset)
		return page(v, f), 200, e
	})
	register("POST /v1/appreciation", func(r *http.Request) (any, int, error) {
		var x struct {
			People  []string `json:"people"`
			Message string   `json:"message"`
		}
		if e := decode(r, &x); e != nil {
			return nil, 0, e
		}
		v, e := a.Product.SubmitCommunity(r.Context(), user(r), "appreciation", "give", x.People, x.Message)
		return v, 201, e
	})
	register("PUT /v1/admin/community-submissions/{id}", func(r *http.Request) (any, int, error) {
		var x struct {
			Decision string `json:"decision"`
		}
		if e := decode(r, &x); e != nil {
			return nil, 0, e
		}
		v, e := a.Product.ReviewCommunity(r.Context(), user(r), r.PathValue("id"), x.Decision)
		return v, 200, e
	})
	register("POST /v1/comments/{id}/report", a.productReport("comment"))
	register("POST /v1/posts/{id}/report", a.productReport("post"))
	register("POST /v1/answers/{id}/report", a.productReport("answer"))
	register("PUT /v1/moderation/comments/{id}", func(r *http.Request) (any, int, error) {
		var x struct {
			Hidden *bool `json:"hidden"`
		}
		if e := decode(r, &x); e != nil {
			return nil, 0, e
		}
		if x.Hidden == nil {
			return nil, 0, domain.Invalidf("hidden is required")
		}
		v, e := a.Product.SetCommentHidden(r.Context(), user(r), r.PathValue("id"), *x.Hidden)
		return v, 200, e
	})
	register("PUT /v1/moderation/answers/{id}", func(r *http.Request) (any, int, error) {
		var x struct {
			Hidden *bool `json:"hidden"`
		}
		if e := decode(r, &x); e != nil {
			return nil, 0, e
		}
		if x.Hidden == nil {
			return nil, 0, domain.Invalidf("hidden is required")
		}
		v, e := a.Product.SetAnswerHidden(r.Context(), user(r), r.PathValue("id"), *x.Hidden)
		return v, 200, e
	})
	register("PUT /v1/tags/{id}/follow", func(r *http.Request) (any, int, error) {
		var x struct {
			Enabled bool `json:"enabled"`
		}
		if e := decode(r, &x); e != nil {
			return nil, 0, e
		}
		e := a.Product.FollowTag(r.Context(), user(r), r.PathValue("id"), x.Enabled)
		return map[string]bool{"following": x.Enabled}, 200, e
	})
	register("GET /v1/me/followed-tags", func(r *http.Request) (any, int, error) {
		f, e := filters(r)
		if e != nil {
			return nil, 0, e
		}
		v, e := a.Product.FollowedTags(r.Context(), user(r), f.Limit, f.Offset)
		return page(v, f), 200, e
	})
	register("GET /v1/me/notifications", func(r *http.Request) (any, int, error) {
		f, e := filters(r)
		if e != nil {
			return nil, 0, e
		}
		v, e := a.Product.Notifications(r.Context(), user(r), f.Limit, f.Offset)
		return page(v, f), 200, e
	})
	register("PUT /v1/me/notifications/read-all", func(r *http.Request) (any, int, error) {
		e := a.Product.MarkAllNotifications(r.Context(), user(r))
		return map[string]bool{"updated": e == nil}, 200, e
	})
	register("PUT /v1/me/notifications/{id}/read", func(r *http.Request) (any, int, error) {
		e := a.Product.MarkNotification(r.Context(), user(r), r.PathValue("id"))
		return map[string]bool{"read": e == nil}, 200, e
	})
	register("GET /v1/me/points", func(r *http.Request) (any, int, error) {
		f, e := filters(r)
		if e != nil {
			return nil, 0, e
		}
		total, v, e := a.Product.Points(r.Context(), user(r), f.Limit, f.Offset)
		return map[string]any{"total": total, "items": v, "limit": f.Limit, "offset": f.Offset}, 200, e
	})
	register("GET /v1/me/credits", func(r *http.Request) (any, int, error) {
		f, e := filters(r)
		if e != nil {
			return nil, 0, e
		}
		total, v, e := a.Product.Credits(r.Context(), user(r), f.Limit, f.Offset)
		return map[string]any{"balance": total, "items": v, "limit": f.Limit, "offset": f.Offset}, 200, e
	})
	register("POST /v1/me/credits/claims", func(r *http.Request) (any, int, error) {
		var x struct {
			Credits     float64 `json:"credits"`
			Description string  `json:"description"`
		}
		if e := decode(r, &x); e != nil {
			return nil, 0, e
		}
		v, e := a.Product.RequestRedemption(r.Context(), user(r), x.Credits, x.Description)
		return v, 201, e
	})
	register("GET /v1/me/credits/claims", func(r *http.Request) (any, int, error) {
		f, e := filters(r)
		if e != nil {
			return nil, 0, e
		}
		v, e := a.Product.Redemptions(r.Context(), user(r), f.Limit, f.Offset)
		return page(v, f), 200, e
	})
	register("GET /v1/leaderboard", func(r *http.Request) (any, int, error) {
		f, e := filters(r)
		if e != nil {
			return nil, 0, e
		}
		period := r.URL.Query().Get("period")
		if period == "" {
			period = "all"
		}
		v, e := a.Product.Leaderboard(r.Context(), user(r), period, f.Limit, f.Offset)
		return page(v, f), 200, e
	})
	register("GET /v1/reward-rules", func(r *http.Request) (any, int, error) {
		v, e := a.Product.RewardRules(r.Context(), user(r))
		return v, 200, e
	})
	register("PUT /v1/admin/reward-rules/{event}", func(r *http.Request) (any, int, error) {
		var x struct {
			Points     int    `json:"points"`
			Enabled    bool   `json:"enabled"`
			DailyCap   int    `json:"daily_cap"`
			RewardKind string `json:"reward_kind"`
		}
		if e := decode(r, &x); e != nil {
			return nil, 0, e
		}
		v, e := a.Product.SetRewardRule(r.Context(), user(r), domain.RewardRule{EventType: r.PathValue("event"), Points: x.Points, Enabled: x.Enabled, DailyCap: x.DailyCap, RewardKind: x.RewardKind})
		return v, 200, e
	})
	register("POST /v1/reports", func(r *http.Request) (any, int, error) {
		var x domain.ReportInput
		if e := decode(r, &x); e != nil {
			return nil, 0, e
		}
		v, e := a.Product.SubmitReport(r.Context(), user(r), x)
		return v, 201, e
	})
	register("GET /v1/moderation/reports", func(r *http.Request) (any, int, error) {
		f, e := filters(r)
		if e != nil {
			return nil, 0, e
		}
		v, e := a.Product.Reports(r.Context(), user(r), r.URL.Query().Get("status"), f.Limit, f.Offset)
		return page(v, f), 200, e
	})
	register("PUT /v1/moderation/reports/{id}", func(r *http.Request) (any, int, error) {
		var x struct {
			Decision string `json:"decision"`
			Note     string `json:"note"`
		}
		if e := decode(r, &x); e != nil {
			return nil, 0, e
		}
		v, e := a.Product.ReviewReport(r.Context(), user(r), r.PathValue("id"), x.Decision, x.Note)
		return v, 200, e
	})
	register("GET /v1/admin/users", func(r *http.Request) (any, int, error) {
		f, e := filters(r)
		if e != nil {
			return nil, 0, e
		}
		v, e := a.Product.AdminUsers(r.Context(), user(r), f.Query, f.Limit, f.Offset)
		return page(v, f), 200, e
	})
	register("GET /v1/admin/analytics", func(r *http.Request) (any, int, error) {
		v, e := a.Product.Analytics(r.Context(), user(r))
		return v, 200, e
	})
	register("GET /v1/admin/redemptions", func(r *http.Request) (any, int, error) {
		f, e := filters(r)
		if e != nil {
			return nil, 0, e
		}
		v, e := a.Product.Redemptions(r.Context(), user(r), f.Limit, f.Offset)
		return page(v, f), 200, e
	})
	register("PUT /v1/admin/redemptions/{id}", func(r *http.Request) (any, int, error) {
		var x struct {
			Decision string `json:"decision"`
		}
		if e := decode(r, &x); e != nil {
			return nil, 0, e
		}
		v, e := a.Product.ReviewRedemption(r.Context(), user(r), r.PathValue("id"), x.Decision)
		return v, 200, e
	})
	register("POST /v1/tags/{id}/aliases", func(r *http.Request) (any, int, error) {
		var x struct {
			Alias string `json:"alias"`
		}
		if e := decode(r, &x); e != nil {
			return nil, 0, e
		}
		v, e := a.Product.SetTagAlias(r.Context(), user(r), r.PathValue("id"), x.Alias)
		return v, 200, e
	})
	register("GET /v1/tags/{id}", func(r *http.Request) (any, int, error) {
		v, e := a.Product.TagDetail(r.Context(), user(r), r.PathValue("id"))
		return v, 200, e
	})
	register("POST /v1/moderation/tags/{id}/merge", func(r *http.Request) (any, int, error) {
		var x struct {
			TargetID string `json:"target_id"`
		}
		if e := decode(r, &x); e != nil {
			return nil, 0, e
		}
		v, e := a.Product.MergeTag(r.Context(), user(r), r.PathValue("id"), x.TargetID)
		return v, 200, e
	})
	register("PUT /v1/moderation/posts/{id}/editorial", func(r *http.Request) (any, int, error) {
		var x struct {
			Action  string `json:"action"`
			Enabled bool   `json:"enabled"`
		}
		if e := decode(r, &x); e != nil {
			return nil, 0, e
		}
		v, e := a.Product.Editorial(r.Context(), user(r), r.PathValue("id"), x.Action, x.Enabled)
		return v, 200, e
	})
	register("POST /v1/questions/{id}/duplicate", func(r *http.Request) (any, int, error) {
		var x struct {
			TargetID string `json:"target_id"`
		}
		if e := decode(r, &x); e != nil {
			return nil, 0, e
		}
		v, e := a.Product.Duplicate(r.Context(), user(r), r.PathValue("id"), x.TargetID)
		return v, 200, e
	})
	register("POST /v1/media", a.uploadMedia)
	// Media is returned through an authenticated same-origin endpoint. The frontend
	// fetches it as a blob, so credentials never appear in image URLs.
	mux.Handle("GET /v1/media/{id}", a.authenticate(http.HandlerFunc(a.getMedia)))
}
func (a *API) productVote(kind string) endpoint {
	return func(r *http.Request) (any, int, error) {
		var x struct {
			Helpful bool `json:"helpful"`
		}
		if e := decode(r, &x); e != nil {
			return nil, 0, e
		}
		v, e := a.Product.Vote(r.Context(), user(r), kind, r.PathValue("id"), x.Helpful)
		return v, 200, e
	}
}
func (a *API) productComments(kind string) endpoint {
	return func(r *http.Request) (any, int, error) {
		f, e := filters(r)
		if e != nil {
			return nil, 0, e
		}
		v, e := a.Product.Comments(r.Context(), user(r), kind, r.PathValue("id"), f.Limit, f.Offset)
		return page(v, f), 200, e
	}
}
func (a *API) productComment(kind string) endpoint {
	return func(r *http.Request) (any, int, error) {
		var x struct {
			Body string `json:"body"`
		}
		if e := decode(r, &x); e != nil {
			return nil, 0, e
		}
		v, e := a.Product.Comment(r.Context(), user(r), kind, r.PathValue("id"), x.Body)
		return v, 201, e
	}
}
func (a *API) productReply(r *http.Request) (any, int, error) {
	var x struct {
		Body string `json:"body"`
	}
	if e := decode(r, &x); e != nil {
		return nil, 0, e
	}
	v, e := a.Product.ReplyComment(r.Context(), user(r), r.PathValue("id"), x.Body)
	return v, 201, e
}
func (a *API) productReport(kind string) endpoint {
	return func(r *http.Request) (any, int, error) {
		var x struct {
			Reason  string `json:"reason"`
			Details string `json:"details"`
		}
		if e := decode(r, &x); e != nil {
			return nil, 0, e
		}
		v, e := a.Product.SubmitReport(r.Context(), user(r), domain.ReportInput{TargetType: kind, TargetID: r.PathValue("id"), Reason: x.Reason, Details: x.Details})
		return v, 201, e
	}
}
func (a *API) uploadMedia(r *http.Request) (any, int, error) {
	r.Body = http.MaxBytesReader(nil, r.Body, 6<<20)
	if e := r.ParseMultipartForm(6 << 20); e != nil {
		return nil, 0, domain.Invalidf("multipart form must contain an image up to 5 MiB")
	}
	postID := r.FormValue("post_id")
	file, header, e := r.FormFile("file")
	if e != nil {
		return nil, 0, domain.Invalidf("file is required")
	}
	defer file.Close()
	data, e := io.ReadAll(io.LimitReader(file, 5<<20+1))
	if e != nil {
		return nil, 0, e
	}
	if len(data) > 5<<20 {
		return nil, 0, domain.Invalidf("image exceeds 5 MiB")
	}
	v, e := a.Product.UploadMedia(r.Context(), user(r), postID, header.Filename, data)
	return v, 201, e
}
func (a *API) getMedia(w http.ResponseWriter, r *http.Request) {
	m, e := a.Product.GetMedia(r.Context(), user(r), r.PathValue("id"))
	if e != nil {
		a.writeError(w, r, e)
		return
	}
	w.Header().Set("Content-Type", m.ContentType)
	w.Header().Set("Content-Disposition", mime.FormatMediaType("inline", map[string]string{"filename": m.Filename}))
	w.Header().Set("Cache-Control", "private, no-store")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(m.Data)
}
