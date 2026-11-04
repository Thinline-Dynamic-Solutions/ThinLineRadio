// Copyright (C) 2026 Thinline Dynamic Solutions
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.

package main

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ListenLink is a shareable no-sign-in listening link. Visitors who open
// /l/{token} get exactly the link's channels, delay and features.
type ListenLink struct {
	Id           uint64 `json:"id"`
	Token        string `json:"token"`
	Name         string `json:"name"`
	SystemAccess string `json:"systemAccess"`
	Delay        int    `json:"delay"`
	Features     string `json:"-"`
	Enabled      bool   `json:"enabled"`
	ExpiresAt    int64  `json:"expiresAt"`
	CreatedAt    int64  `json:"createdAt"`

	scope *UserGroup
}

func (link *ListenLink) Active() bool {
	if link == nil || !link.Enabled {
		return false
	}
	return link.ExpiresAt == 0 || time.Now().Unix() < link.ExpiresAt
}

// buildScope turns the link into an in-memory free group so the existing
// access, delay and feature checks apply unchanged.
func (link *ListenLink) buildScope() {
	g := &UserGroup{
		Name:         link.Name,
		SystemAccess: link.SystemAccess,
		Delay:        link.Delay,
		Features:     link.Features,
		IsFreeGroup:  true,
	}
	if strings.TrimSpace(g.Features) == "" {
		g.Features = FreeGroupDefaultFeatures().ToJSON()
	}
	g.loadSystemAccess()
	g.loadSystemDelays()
	g.loadTalkgroupDelays()
	g.loadFeatures()
	link.scope = g
}

func (link *ListenLink) GetFeatures() UserGroupFeatures {
	if link == nil || link.scope == nil {
		return UserGroupFeatures{}
	}
	return link.scope.GetFeatures()
}

type ListenLinks struct {
	mutex   sync.RWMutex
	byId    map[uint64]*ListenLink
	byToken map[string]*ListenLink
}

func NewListenLinks() *ListenLinks {
	return &ListenLinks{byId: map[uint64]*ListenLink{}, byToken: map[string]*ListenLink{}}
}

const listenLinkColumns = `"listenLinkId", "token", "name", "systemAccess", "delay", "features", "enabled", "expiresAt", "createdAt"`

func scanListenLink(scan func(dest ...any) error) (*ListenLink, error) {
	link := &ListenLink{}
	if err := scan(&link.Id, &link.Token, &link.Name, &link.SystemAccess, &link.Delay, &link.Features, &link.Enabled, &link.ExpiresAt, &link.CreatedAt); err != nil {
		return nil, err
	}
	link.buildScope()
	return link, nil
}

func (links *ListenLinks) Load(db *Database) error {
	rows, err := db.Sql.Query(`SELECT ` + listenLinkColumns + ` FROM "listenLinks"`)
	if err != nil {
		return err
	}
	defer rows.Close()

	byId := map[uint64]*ListenLink{}
	byToken := map[string]*ListenLink{}
	for rows.Next() {
		link, err := scanListenLink(rows.Scan)
		if err != nil {
			return err
		}
		byId[link.Id] = link
		byToken[link.Token] = link
	}
	if err := rows.Err(); err != nil {
		return err
	}

	links.mutex.Lock()
	links.byId = byId
	links.byToken = byToken
	links.mutex.Unlock()
	return nil
}

// Lookup returns the link for a token only while it is enabled and unexpired.
func (links *ListenLinks) Lookup(token string) *ListenLink {
	if links == nil {
		return nil
	}
	token = strings.TrimSpace(token)
	if token == "" {
		return nil
	}
	links.mutex.RLock()
	link := links.byToken[token]
	links.mutex.RUnlock()
	if !link.Active() {
		return nil
	}
	return link
}

func (links *ListenLinks) Get(id uint64) *ListenLink {
	links.mutex.RLock()
	defer links.mutex.RUnlock()
	return links.byId[id]
}

func (links *ListenLinks) All() []*ListenLink {
	links.mutex.RLock()
	out := make([]*ListenLink, 0, len(links.byId))
	for _, link := range links.byId {
		out = append(out, link)
	}
	links.mutex.RUnlock()
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt > out[j].CreatedAt })
	return out
}

const listenLinkAlphabet = "abcdefghijkmnpqrstuvwxyzABCDEFGHJKLMNPQRSTUVWXYZ23456789"

func newListenLinkToken() (string, error) {
	b := make([]byte, 10)
	max := big.NewInt(int64(len(listenLinkAlphabet)))
	for i := range b {
		n, err := rand.Int(rand.Reader, max)
		if err != nil {
			return "", err
		}
		b[i] = listenLinkAlphabet[n.Int64()]
	}
	return string(b), nil
}

func (links *ListenLinks) Add(link *ListenLink, db *Database) error {
	for attempt := 0; attempt < 5; attempt++ {
		token, err := newListenLinkToken()
		if err != nil {
			return err
		}
		link.Token = token
		link.CreatedAt = time.Now().Unix()
		err = db.Sql.QueryRow(
			`INSERT INTO "listenLinks" ("token", "name", "systemAccess", "delay", "features", "enabled", "expiresAt", "createdAt")
			 VALUES ($1, $2, $3, $4, $5, $6, $7, $8) RETURNING "listenLinkId"`,
			link.Token, link.Name, link.SystemAccess, link.Delay, link.Features, link.Enabled, link.ExpiresAt, link.CreatedAt,
		).Scan(&link.Id)
		if err == nil {
			link.buildScope()
			links.mutex.Lock()
			links.byId[link.Id] = link
			links.byToken[link.Token] = link
			links.mutex.Unlock()
			return nil
		}
		if !strings.Contains(strings.ToLower(err.Error()), "unique") {
			return err
		}
	}
	return errors.New("could not generate a unique link token")
}

func (links *ListenLinks) Update(link *ListenLink, db *Database) error {
	if _, err := db.Sql.Exec(
		`UPDATE "listenLinks" SET "name" = $1, "systemAccess" = $2, "delay" = $3, "features" = $4, "enabled" = $5, "expiresAt" = $6 WHERE "listenLinkId" = $7`,
		link.Name, link.SystemAccess, link.Delay, link.Features, link.Enabled, link.ExpiresAt, link.Id,
	); err != nil {
		return err
	}
	link.buildScope()
	links.mutex.Lock()
	if old := links.byId[link.Id]; old != nil {
		delete(links.byToken, old.Token)
	}
	links.byId[link.Id] = link
	links.byToken[link.Token] = link
	links.mutex.Unlock()
	return nil
}

func (links *ListenLinks) Delete(id uint64, db *Database) error {
	if _, err := db.Sql.Exec(`DELETE FROM "listenLinks" WHERE "listenLinkId" = $1`, id); err != nil {
		return err
	}
	links.mutex.Lock()
	if old := links.byId[id]; old != nil {
		delete(links.byToken, old.Token)
		delete(links.byId, id)
	}
	links.mutex.Unlock()
	return nil
}

func migrateListenLinks(db *Database) error {
	_, err := db.Sql.Exec(`CREATE TABLE IF NOT EXISTS "listenLinks" (
		"listenLinkId" bigserial NOT NULL PRIMARY KEY,
		"token" text NOT NULL UNIQUE,
		"name" text NOT NULL DEFAULT '',
		"systemAccess" text NOT NULL DEFAULT '',
		"delay" integer NOT NULL DEFAULT 0,
		"features" text NOT NULL DEFAULT '',
		"enabled" boolean NOT NULL DEFAULT true,
		"expiresAt" bigint NOT NULL DEFAULT 0,
		"createdAt" bigint NOT NULL DEFAULT 0
	)`)
	if err != nil {
		return fmt.Errorf("migrateListenLinks: %w", err)
	}
	return nil
}

// listenLinkListeners counts connected listeners per link id.
func (controller *Controller) listenLinkListeners() map[uint64]int {
	counts := map[uint64]int{}
	controller.Clients.mutex.Lock()
	for c := range controller.Clients.Map {
		if c.User == nil && c.ListenLink != nil {
			counts[c.ListenLink.Id]++
		}
	}
	controller.Clients.mutex.Unlock()
	return counts
}

// refreshListenLinkClients drops listeners of a link that was turned off,
// deleted, or changed, so they reconnect under the new rules (or not at all).
func (controller *Controller) refreshListenLinkClients(id uint64) {
	controller.Clients.mutex.Lock()
	var targets []*Client
	for c := range controller.Clients.Map {
		if c.User == nil && c.ListenLink != nil && c.ListenLink.Id == id {
			targets = append(targets, c)
		}
	}
	controller.Clients.mutex.Unlock()
	for _, c := range targets {
		if c.Conn != nil {
			c.Conn.Close()
		}
	}
}

type listenLinkRequest struct {
	Name         string             `json:"name"`
	SystemAccess string             `json:"systemAccess"`
	Delay        int                `json:"delay"`
	Features     *UserGroupFeatures `json:"features"`
	Enabled      *bool              `json:"enabled"`
	ExpiresAt    int64              `json:"expiresAt"`
}

func listenLinkJSON(link *ListenLink, listeners int) map[string]any {
	return map[string]any{
		"id":           link.Id,
		"token":        link.Token,
		"name":         link.Name,
		"systemAccess": link.SystemAccess,
		"delay":        link.Delay,
		"features":     link.GetFeatures(),
		"enabled":      link.Enabled,
		"expiresAt":    link.ExpiresAt,
		"createdAt":    link.CreatedAt,
		"active":       link.Active(),
		"listeners":    listeners,
	}
}

// AdminListenLinksHandler serves /api/admin/listen-links and /api/admin/listen-links/{id}.
func (api *Api) AdminListenLinksHandler(w http.ResponseWriter, r *http.Request) {
	client := api.getClient(r)
	if client == nil || !api.isAdmin(client) {
		api.exitWithError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}
	links := api.Controller.ListenLinks
	db := api.Controller.Database

	var id uint64
	if rest := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/admin/listen-links"), "/"); rest != "" {
		parsed, err := strconv.ParseUint(rest, 10, 64)
		if err != nil {
			api.exitWithError(w, http.StatusNotFound, "not found")
			return
		}
		id = parsed
	}

	writeJSON := func(status int, body any) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(body)
	}

	switch {
	case r.Method == http.MethodGet && id == 0:
		counts := api.Controller.listenLinkListeners()
		out := []map[string]any{}
		for _, link := range links.All() {
			out = append(out, listenLinkJSON(link, counts[link.Id]))
		}
		writeJSON(http.StatusOK, map[string]any{"links": out})

	case (r.Method == http.MethodPost && id == 0) || (r.Method == http.MethodPut && id != 0):
		var req listenLinkRequest
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
			api.exitWithError(w, http.StatusBadRequest, "invalid request")
			return
		}
		req.Name = strings.TrimSpace(req.Name)
		if req.Name == "" {
			api.exitWithError(w, http.StatusBadRequest, "Name is required")
			return
		}
		if strings.TrimSpace(req.SystemAccess) == "" || strings.TrimSpace(req.SystemAccess) == "[]" {
			api.exitWithError(w, http.StatusBadRequest, "Pick at least one channel")
			return
		}
		if req.Delay < 0 {
			req.Delay = 0
		}

		var link *ListenLink
		if id == 0 {
			link = &ListenLink{Enabled: true, Features: FreeGroupDefaultFeatures().ToJSON()}
		} else {
			existing := links.Get(id)
			if existing == nil {
				api.exitWithError(w, http.StatusNotFound, "not found")
				return
			}
			copied := *existing
			link = &copied
		}
		link.Name = req.Name
		link.SystemAccess = req.SystemAccess
		link.Delay = req.Delay
		link.ExpiresAt = req.ExpiresAt
		if req.Features != nil {
			link.Features = req.Features.ToJSON()
		}
		if req.Enabled != nil {
			link.Enabled = *req.Enabled
		}

		var err error
		if id == 0 {
			err = links.Add(link, db)
		} else {
			err = links.Update(link, db)
		}
		if err != nil {
			api.exitWithError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if id != 0 {
			api.Controller.refreshListenLinkClients(id)
		}
		writeJSON(http.StatusOK, listenLinkJSON(link, 0))

	case r.Method == http.MethodDelete && id != 0:
		if links.Get(id) == nil {
			api.exitWithError(w, http.StatusNotFound, "not found")
			return
		}
		if err := links.Delete(id, db); err != nil {
			api.exitWithError(w, http.StatusInternalServerError, err.Error())
			return
		}
		api.Controller.refreshListenLinkClients(id)
		writeJSON(http.StatusOK, map[string]any{"ok": true})

	default:
		api.exitWithError(w, http.StatusMethodNotAllowed, "Method not allowed")
	}
}
