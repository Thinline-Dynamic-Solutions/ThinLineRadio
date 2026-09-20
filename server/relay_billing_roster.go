// Copyright (C) 2025 Thinline Dynamic Solutions
//
// relay_billing_roster.go — roster the relay pulls to bill ad-free seats.
// Relay owns the count; this endpoint only reports account flags.

package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

type billingRosterUser struct {
	ID                 string `json:"id"`
	SubscriptionStatus string `json:"subscription_status"`
	Suspended          bool   `json:"suspended"`
	AccountExpired     bool   `json:"account_expired"`
	BillingRequired    bool   `json:"billing_required"`
}

func billingRosterOpaqueID(userID uint64) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("tlr-user:%d", userID)))
	return hex.EncodeToString(sum[:])
}

func userBillingRequired(user *User, groups *UserGroups, paywallEnabled bool) bool {
	if user == nil || groups == nil || !paywallEnabled || user.UserGroupId == 0 {
		return false
	}
	group := groups.Get(user.UserGroupId)
	if group == nil || !group.BillingEnabled {
		return false
	}
	if group.BillingMode == "group_admin" {
		return user.IsGroupAdmin
	}
	return true
}

func buildBillingRoster(users []*User, groups *UserGroups, paywallEnabled bool) []billingRosterUser {
	out := make([]billingRosterUser, 0, len(users))
	for _, user := range users {
		if user == nil {
			continue
		}
		out = append(out, billingRosterUser{
			ID:                 billingRosterOpaqueID(user.Id),
			SubscriptionStatus: strings.TrimSpace(user.SubscriptionStatus),
			Suspended:          user.IsSuspended(),
			AccountExpired:     user.AccountExpired(),
			BillingRequired:    userBillingRequired(user, groups, paywallEnabled),
		})
	}
	return out
}

// RelayBillingRosterHandler is GET /api/relay/billing-roster.
// Authenticated with the configured relay API key (same as billing webhooks).
func (api *Api) RelayBillingRosterHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		api.exitWithError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}
	key := r.Header.Get("X-API-Key")
	if key == "" {
		if auth := r.Header.Get("Authorization"); strings.HasPrefix(strings.ToLower(auth), "bearer ") {
			key = strings.TrimSpace(auth[7:])
		}
	}
	if key == "" || key != api.Controller.Options.RelayServerAPIKey {
		api.exitWithError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}
	roster := buildBillingRoster(
		api.Controller.Users.GetAllUsers(),
		api.Controller.UserGroups,
		api.Controller.Options.StripePaywallEnabled,
	)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"users": roster,
	})
}
