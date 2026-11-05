package main

import (
	"database/sql"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"
)

// How a visit showed up.
const (
	listenerHowPage     = "page"
	listenerHowScanner  = "scanner"
	listenerHowSignedIn = "signed-in"
	listenerHowLogin    = "login"
)

// listenerClaimWindow is how long an anonymous page or socket can be
// attached to an account that signs in from the same browser.
const listenerClaimWindow = 20 * time.Minute

const listenerVisitRetention = 30 * 24 * time.Hour

type listenerVisit struct {
	Id             int64  `json:"id"`
	UserId         uint64 `json:"userId,omitempty"`
	IP             string `json:"ip"`
	UserAgent      string `json:"userAgent"`
	Origin         string `json:"origin"`
	Referer        string `json:"referer"`
	AcceptLanguage string `json:"acceptLanguage"`
	ClientKind     string `json:"clientKind"`
	ListenLink     string `json:"listenLink"`
	How            string `json:"how"`
	AuthNote       string `json:"authNote"`
	Detail         string `json:"detail"`
	Hits           int    `json:"hits"`
	BadPins        int    `json:"badPins"`
	OpenedAt       int64  `json:"openedAt"`
	LastSeenAt     int64  `json:"lastSeenAt"`
	ClosedAt       int64  `json:"closedAt"`
}

type listenerPresenceSummary struct {
	IP         string
	ClientKind string
	How        string
	AuthNote   string
	LastSeenAt int64
}

type listenerRequestMeta struct {
	IP             string
	UserAgent      string
	Origin         string
	Referer        string
	AcceptLanguage string
	ClientKind     string
	ListenLink     string
}

var listenerPrune struct {
	mu sync.Mutex
	n  int
}

func listenerClientKind(userAgent string) string {
	ua := strings.ToLower(userAgent)
	switch {
	case ua == "":
		return "unknown"
	case strings.Contains(ua, "dart"),
		strings.Contains(ua, "thinline"),
		strings.Contains(ua, "okhttp"),
		strings.Contains(ua, "cfnetwork"):
		return "mobile"
	default:
		return "web"
	}
}

func clipListenerText(s string, n int) string {
	s = strings.TrimSpace(s)
	if n <= 0 || len(s) <= n {
		return s
	}
	return s[:n]
}

func listenerMeta(r *http.Request, link *ListenLink) listenerRequestMeta {
	meta := listenerRequestMeta{}
	if r == nil {
		return meta
	}
	meta.IP = clipListenerText(GetRemoteAddr(r), 80)
	meta.UserAgent = clipListenerText(r.UserAgent(), 400)
	meta.Origin = clipListenerText(r.Header.Get("Origin"), 300)
	meta.Referer = clipListenerText(r.Referer(), 300)
	meta.AcceptLanguage = clipListenerText(r.Header.Get("Accept-Language"), 120)
	meta.ClientKind = listenerClientKind(meta.UserAgent)
	if link != nil {
		name := strings.TrimSpace(link.Name)
		if name == "" {
			name = "share link"
		}
		meta.ListenLink = clipListenerText(name, 120)
	}
	return meta
}

func (controller *Controller) listenerDB() *sql.DB {
	if controller == nil || controller.Database == nil || controller.Database.Sql == nil {
		return nil
	}
	return controller.Database.Sql
}

func (controller *Controller) maybePruneListenerVisits(db *sql.DB) {
	listenerPrune.mu.Lock()
	listenerPrune.n++
	run := listenerPrune.n%40 == 0
	listenerPrune.mu.Unlock()
	if !run {
		return
	}
	cutoff := time.Now().Add(-listenerVisitRetention).Unix()
	if _, err := db.Exec(`DELETE FROM "listenerVisits" WHERE "lastSeenAt" > 0 AND "lastSeenAt" < $1`, cutoff); err != nil {
		log.Printf("listener visits prune: %v", err)
	}
}

// noteListenerPage records someone opening the scanner page. Repeat loads from
// the same browser within the claim window bump the same anonymous row.
func (controller *Controller) noteListenerPage(r *http.Request) {
	if r == nil || strings.HasPrefix(r.URL.Path, "/admin") {
		return
	}
	db := controller.listenerDB()
	if db == nil {
		return
	}
	meta := listenerMeta(r, nil)
	if meta.IP == "" && meta.UserAgent == "" {
		return
	}
	now := time.Now().Unix()
	cutoff := time.Now().Add(-listenerClaimWindow).Unix()

	var id int64
	err := db.QueryRow(`
		SELECT "visitId" FROM "listenerVisits"
		WHERE "ip" = $1 AND "userAgent" = $2 AND "userId" IS NULL AND "closedAt" = 0 AND "how" = $3 AND "lastSeenAt" >= $4
		ORDER BY "lastSeenAt" DESC LIMIT 1`,
		meta.IP, meta.UserAgent, listenerHowPage, cutoff,
	).Scan(&id)
	if err == nil && id > 0 {
		_, err = db.Exec(`UPDATE "listenerVisits" SET "hits" = "hits" + 1, "lastSeenAt" = $1, "origin" = CASE WHEN "origin" = '' THEN $2 ELSE "origin" END, "referer" = CASE WHEN "referer" = '' THEN $3 ELSE "referer" END WHERE "visitId" = $4`,
			now, meta.Origin, meta.Referer, id)
		if err != nil {
			log.Printf("listener page touch: %v", err)
		}
		return
	}
	if err != nil && err != sql.ErrNoRows {
		log.Printf("listener page lookup: %v", err)
		return
	}

	if _, err = db.Exec(`
		INSERT INTO "listenerVisits"
			("ip", "userAgent", "origin", "referer", "acceptLanguage", "clientKind", "how", "hits", "openedAt", "lastSeenAt")
		VALUES ($1, $2, $3, $4, $5, $6, $7, 1, $8, $8)`,
		meta.IP, meta.UserAgent, meta.Origin, meta.Referer, meta.AcceptLanguage, meta.ClientKind, listenerHowPage, now,
	); err != nil {
		log.Printf("listener page insert: %v", err)
		return
	}
	// An older open page row for this browser is no longer the current visit.
	_, _ = db.Exec(`
		UPDATE "listenerVisits" SET "closedAt" = $1
		WHERE "ip" = $2 AND "userAgent" = $3 AND "userId" IS NULL AND "how" = $4 AND "closedAt" = 0 AND "lastSeenAt" < $5`,
		now, meta.IP, meta.UserAgent, listenerHowPage, now)
	controller.maybePruneListenerVisits(db)
}

// noteListenerConnect records the WebSocket. A recent anonymous page view from
// the same browser is reused so the page load and the socket are one visit.
func (controller *Controller) noteListenerConnect(client *Client) {
	if client == nil || client.request == nil {
		return
	}
	if strings.HasPrefix(client.request.URL.Path, "/admin") {
		return
	}
	db := controller.listenerDB()
	if db == nil {
		return
	}
	meta := listenerMeta(client.request, client.ListenLink)
	now := time.Now().Unix()
	cutoff := time.Now().Add(-listenerClaimWindow).Unix()

	var id int64
	err := db.QueryRow(`
		SELECT "visitId" FROM "listenerVisits"
		WHERE "ip" = $1 AND "userAgent" = $2 AND "userId" IS NULL AND "closedAt" = 0 AND "how" = $3 AND "lastSeenAt" >= $4
		ORDER BY "lastSeenAt" DESC LIMIT 1`,
		meta.IP, meta.UserAgent, listenerHowPage, cutoff,
	).Scan(&id)
	if err == nil && id > 0 {
		_, err = db.Exec(`
			UPDATE "listenerVisits"
			SET "how" = $1, "clientKind" = $2, "listenLink" = CASE WHEN $3 <> '' THEN $3 ELSE "listenLink" END,
				"lastSeenAt" = $4, "origin" = CASE WHEN "origin" = '' THEN $5 ELSE "origin" END
			WHERE "visitId" = $6`,
			listenerHowScanner, meta.ClientKind, meta.ListenLink, now, meta.Origin, id)
		if err != nil {
			log.Printf("listener connect claim: %v", err)
			return
		}
		client.visitId = id
		return
	}
	if err != nil && err != sql.ErrNoRows {
		log.Printf("listener connect lookup: %v", err)
	}

	err = db.QueryRow(`
		INSERT INTO "listenerVisits"
			("ip", "userAgent", "origin", "referer", "acceptLanguage", "clientKind", "listenLink", "how", "hits", "openedAt", "lastSeenAt")
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, 1, $9, $9)
		RETURNING "visitId"`,
		meta.IP, meta.UserAgent, meta.Origin, meta.Referer, meta.AcceptLanguage, meta.ClientKind, meta.ListenLink, listenerHowScanner, now,
	).Scan(&id)
	if err != nil {
		log.Printf("listener connect insert: %v", err)
		return
	}
	client.visitId = id
	controller.maybePruneListenerVisits(db)
}

// noteListenerSignedIn attaches the open socket's visit to the account.
func (controller *Controller) noteListenerSignedIn(client *Client, user *User, authNote string) {
	if controller == nil || user == nil || user.Id == 0 {
		return
	}
	db := controller.listenerDB()
	if db == nil {
		return
	}
	now := time.Now().Unix()
	if client != nil && client.visitId > 0 {
		kind := ""
		if client.FCMToken != "" {
			kind = "mobile"
		}
		_, err := db.Exec(`
			UPDATE "listenerVisits"
			SET "userId" = $1, "how" = $2, "authNote" = $3, "lastSeenAt" = $4,
				"clientKind" = CASE WHEN $5 <> '' THEN $5 ELSE "clientKind" END
			WHERE "visitId" = $6`,
			user.Id, listenerHowSignedIn, authNote, now, kind, client.visitId)
		if err != nil {
			log.Printf("listener sign-in claim: %v", err)
		}
		return
	}
	meta := listenerRequestMeta{}
	var link *ListenLink
	if client != nil {
		link = client.ListenLink
		meta = listenerMeta(client.request, link)
	}
	controller.insertListenerVisit(db, meta, user.Id, listenerHowSignedIn, authNote, "", now)
}

// noteListenerLogin records a password sign-in. A successful sign-in claims a
// recent anonymous visit from the same browser. A failed attempt against a
// known account is stored on that account and does not claim the anonymous visit.
func (controller *Controller) noteListenerLogin(r *http.Request, user *User, authNote, detail string) {
	db := controller.listenerDB()
	if db == nil || r == nil {
		return
	}
	meta := listenerMeta(r, nil)
	now := time.Now().Unix()
	detail = clipListenerText(detail, 200)
	var userId uint64
	if user != nil {
		userId = user.Id
	}

	if userId > 0 && (authNote == "password" || authNote == "group-admin") {
		cutoff := time.Now().Add(-listenerClaimWindow).Unix()
		var id int64
		err := db.QueryRow(`
			SELECT "visitId" FROM "listenerVisits"
			WHERE "ip" = $1 AND "userAgent" = $2 AND "userId" IS NULL AND "lastSeenAt" >= $3
			ORDER BY "lastSeenAt" DESC LIMIT 1`,
			meta.IP, meta.UserAgent, cutoff,
		).Scan(&id)
		if err == nil && id > 0 {
			if _, err = db.Exec(`
				UPDATE "listenerVisits"
				SET "userId" = $1, "how" = $2, "authNote" = $3, "lastSeenAt" = $4, "closedAt" = 0
				WHERE "visitId" = $5`,
				userId, listenerHowSignedIn, authNote, now, id); err != nil {
				log.Printf("listener login claim: %v", err)
			}
			return
		}
	}

	how := listenerHowLogin
	if userId > 0 && (authNote == "password" || authNote == "group-admin") {
		how = listenerHowSignedIn
	}
	controller.insertListenerVisit(db, meta, userId, how, authNote, detail, now)
}

func (controller *Controller) noteListenerBadPin(client *Client) {
	if client == nil || client.visitId == 0 {
		return
	}
	db := controller.listenerDB()
	if db == nil {
		return
	}
	now := time.Now().Unix()
	if _, err := db.Exec(`UPDATE "listenerVisits" SET "badPins" = "badPins" + 1, "lastSeenAt" = $1 WHERE "visitId" = $2`, now, client.visitId); err != nil {
		log.Printf("listener bad pin: %v", err)
	}
}

func (controller *Controller) noteListenerMobile(client *Client) {
	if client == nil || client.visitId == 0 {
		return
	}
	db := controller.listenerDB()
	if db == nil {
		return
	}
	if _, err := db.Exec(`UPDATE "listenerVisits" SET "clientKind" = 'mobile', "lastSeenAt" = $1 WHERE "visitId" = $2`, time.Now().Unix(), client.visitId); err != nil {
		log.Printf("listener mobile: %v", err)
	}
}

func (controller *Controller) noteListenerDisconnect(client *Client) {
	if client == nil || client.visitId == 0 {
		return
	}
	db := controller.listenerDB()
	if db == nil {
		return
	}
	now := time.Now().Unix()
	if _, err := db.Exec(`UPDATE "listenerVisits" SET "closedAt" = $1, "lastSeenAt" = $1 WHERE "visitId" = $2 AND "closedAt" = 0`, now, client.visitId); err != nil {
		log.Printf("listener disconnect: %v", err)
	}
}

func (controller *Controller) insertListenerVisit(db *sql.DB, meta listenerRequestMeta, userId uint64, how, authNote, detail string, now int64) {
	var uid any
	if userId > 0 {
		uid = userId
	}
	if _, err := db.Exec(`
		INSERT INTO "listenerVisits"
			("userId", "ip", "userAgent", "origin", "referer", "acceptLanguage", "clientKind", "listenLink", "how", "authNote", "detail", "hits", "openedAt", "lastSeenAt")
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, 1, $12, $12)`,
		uid, meta.IP, meta.UserAgent, meta.Origin, meta.Referer, meta.AcceptLanguage, meta.ClientKind, meta.ListenLink, how, authNote, detail, now,
	); err != nil {
		log.Printf("listener visit insert: %v", err)
		return
	}
	controller.maybePruneListenerVisits(db)
}

func (controller *Controller) recentAnonymousVisits(limit int) []listenerVisit {
	db := controller.listenerDB()
	if db == nil {
		return nil
	}
	if limit <= 0 || limit > 100 {
		limit = 40
	}
	rows, err := db.Query(`
		SELECT "visitId", COALESCE("userId", 0), "ip", "userAgent", "origin", "referer", "acceptLanguage",
			"clientKind", "listenLink", "how", "authNote", "detail", "hits", "badPins", "openedAt", "lastSeenAt", "closedAt"
		FROM "listenerVisits"
		WHERE "userId" IS NULL
		ORDER BY "lastSeenAt" DESC
		LIMIT $1`, limit)
	if err != nil {
		log.Printf("listener anonymous list: %v", err)
		return nil
	}
	defer rows.Close()
	return scanListenerVisits(rows)
}

func (controller *Controller) userListenerVisits(userId uint64, limit int) []listenerVisit {
	db := controller.listenerDB()
	if db == nil || userId == 0 {
		return nil
	}
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	rows, err := db.Query(`
		SELECT "visitId", COALESCE("userId", 0), "ip", "userAgent", "origin", "referer", "acceptLanguage",
			"clientKind", "listenLink", "how", "authNote", "detail", "hits", "badPins", "openedAt", "lastSeenAt", "closedAt"
		FROM "listenerVisits"
		WHERE "userId" = $1
		ORDER BY "lastSeenAt" DESC
		LIMIT $2`, userId, limit)
	if err != nil {
		log.Printf("listener user list: %v", err)
		return nil
	}
	defer rows.Close()
	return scanListenerVisits(rows)
}

func (controller *Controller) listenerPresenceByUser() map[uint64]listenerPresenceSummary {
	out := map[uint64]listenerPresenceSummary{}
	db := controller.listenerDB()
	if db == nil {
		return out
	}
	rows, err := db.Query(`
		SELECT DISTINCT ON ("userId") "userId", "ip", "clientKind", "how", "authNote", "lastSeenAt"
		FROM "listenerVisits"
		WHERE "userId" IS NOT NULL
		ORDER BY "userId", "lastSeenAt" DESC`)
	if err != nil {
		log.Printf("listener presence summary: %v", err)
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var userId uint64
		var summary listenerPresenceSummary
		if err := rows.Scan(&userId, &summary.IP, &summary.ClientKind, &summary.How, &summary.AuthNote, &summary.LastSeenAt); err != nil {
			continue
		}
		out[userId] = summary
	}
	return out
}

func scanListenerVisits(rows *sql.Rows) []listenerVisit {
	var list []listenerVisit
	for rows.Next() {
		var v listenerVisit
		if err := rows.Scan(&v.Id, &v.UserId, &v.IP, &v.UserAgent, &v.Origin, &v.Referer, &v.AcceptLanguage,
			&v.ClientKind, &v.ListenLink, &v.How, &v.AuthNote, &v.Detail, &v.Hits, &v.BadPins, &v.OpenedAt, &v.LastSeenAt, &v.ClosedAt); err != nil {
			continue
		}
		list = append(list, v)
	}
	if list == nil {
		list = []listenerVisit{}
	}
	return list
}
