// Copyright (C) 2025 Thinline Dynamic Solutions
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// This program is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
// GNU General Public License for more details.
//
// You should have received a copy of the GNU General Public License
// along with this program.  If not, see <http://www.gnu.org/licenses/>

package main

import (
	"fmt"
	"log"
	"sync"
	"time"
)

type DeviceToken struct {
	Id        uint64
	UserId    uint64
	Token     string // Legacy field; kept for DB compatibility. No longer used for push delivery.
	FCMToken  string // Firebase Cloud Messaging token — the active push token.
	PushType  string // "fcm"
	Platform  string // "ios" or "android"
	Sound     string // Notification sound preference
	CreatedAt int64
	LastUsed  int64
}

type DeviceTokens struct {
	mutex      sync.RWMutex
	tokens     map[uint64]*DeviceToken   // by device token ID
	userTokens map[uint64][]*DeviceToken // by user ID
	// tokenIndex provides O(1) lookup by FCM token string.
	// Used to efficiently clean up invalid tokens reported by the relay server
	// without scanning all users.
	tokenIndex map[string]*DeviceToken
}

func NewDeviceTokens() *DeviceTokens {
	return &DeviceTokens{
		tokens:     make(map[uint64]*DeviceToken),
		userTokens: make(map[uint64][]*DeviceToken),
		tokenIndex: make(map[string]*DeviceToken),
	}
}

// GetByToken returns the DeviceToken whose token value matches the given string, or nil.
func (dt *DeviceTokens) GetByToken(tokenValue string) *DeviceToken {
	dt.mutex.RLock()
	defer dt.mutex.RUnlock()
	return dt.tokenIndex[tokenValue]
}

func (dt *DeviceTokens) Load(db *Database) error {
	dt.mutex.Lock()
	defer dt.mutex.Unlock()

	rows, err := db.Sql.Query(`SELECT "deviceTokenId", "userId", "token", "fcmToken", "pushType", "platform", "sound", "createdAt", "lastUsed" FROM "deviceTokens"`)
	if err != nil {
		return err
	}
	defer rows.Close()

	dt.tokens = make(map[uint64]*DeviceToken)
	dt.userTokens = make(map[uint64][]*DeviceToken)
	dt.tokenIndex = make(map[string]*DeviceToken)

	tokenCount := 0
	userTokenCounts := make(map[uint64]int)
	uniqueUsers := make(map[uint64]bool)

	for rows.Next() {
		token := &DeviceToken{}
		var fcmToken, pushType *string
		err := rows.Scan(
			&token.Id,
			&token.UserId,
			&token.Token,
			&fcmToken,
			&pushType,
			&token.Platform,
			&token.Sound,
			&token.CreatedAt,
			&token.LastUsed,
		)
		if err != nil {
			continue
		}

		// Handle nullable fields
		if fcmToken != nil {
			token.FCMToken = *fcmToken
		}
		if pushType != nil {
			token.PushType = *pushType
		} else {
			token.PushType = "onesignal"
		}

		dt.tokens[token.Id] = token
		dt.userTokens[token.UserId] = append(dt.userTokens[token.UserId], token)
		// Index by FCMToken (the value we send to the relay server) so that
		// invalid-token responses can be matched back to the right record.
		if token.FCMToken != "" {
			dt.tokenIndex[token.FCMToken] = token
		} else if token.Token != "" {
			// Legacy OneSignal record — still index it so it can be cleaned up.
			dt.tokenIndex[token.Token] = token
		}
		tokenCount++
		userTokenCounts[token.UserId]++
		uniqueUsers[token.UserId] = true
	}

	// Log token loading summary with more detail
	fmt.Printf("DeviceTokens.Load: loaded %d total device tokens for %d users\n", tokenCount, len(uniqueUsers))
	if tokenCount == 0 {
		fmt.Printf("DeviceTokens.Load: WARNING - No device tokens found in database. This is normal for new installations or if all users have unregistered their devices.\n")
	}
	for userId, count := range userTokenCounts {
		if count > 1 {
			fmt.Printf("DeviceTokens.Load: user %d has %d device tokens\n", userId, count)
		}
	}

	return nil
}

func (dt *DeviceTokens) Add(token *DeviceToken, db *Database) error {
	dt.mutex.Lock()
	defer dt.mutex.Unlock()

	if token.CreatedAt == 0 {
		token.CreatedAt = time.Now().Unix()
	}
	if token.LastUsed == 0 {
		token.LastUsed = time.Now().Unix()
	}

	// Convert empty strings to nil for database
	var fcmToken *string
	if token.FCMToken != "" {
		fcmToken = &token.FCMToken
	}
	var pushType *string
	if token.PushType != "" {
		pushType = &token.PushType
	}

	var tokenId int64
	err := db.Sql.QueryRow(
		`INSERT INTO "deviceTokens" ("userId", "token", "fcmToken", "pushType", "platform", "sound", "createdAt", "lastUsed") 
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8) RETURNING "deviceTokenId"`,
		token.UserId, token.Token, fcmToken, pushType, token.Platform, token.Sound, token.CreatedAt, token.LastUsed,
	).Scan(&tokenId)
	if err != nil {
		return err
	}

	token.Id = uint64(tokenId)
	dt.tokens[token.Id] = token
	dt.userTokens[token.UserId] = append(dt.userTokens[token.UserId], token)
	if token.FCMToken != "" {
		dt.tokenIndex[token.FCMToken] = token
	} else if token.Token != "" {
		dt.tokenIndex[token.Token] = token
	}

	return nil
}

func (dt *DeviceTokens) Update(token *DeviceToken, db *Database) error {
	dt.mutex.Lock()
	defer dt.mutex.Unlock()

	token.LastUsed = time.Now().Unix()

	// Convert empty strings to nil for database
	var fcmToken *string
	if token.FCMToken != "" {
		fcmToken = &token.FCMToken
	}
	var pushType *string
	if token.PushType != "" {
		pushType = &token.PushType
	}

	_, err := db.Sql.Exec(
		`UPDATE "deviceTokens" SET "token" = $1, "fcmToken" = $2, "pushType" = $3, "platform" = $4, "sound" = $5, "lastUsed" = $6 WHERE "deviceTokenId" = $7`,
		token.Token, fcmToken, pushType, token.Platform, token.Sound, token.LastUsed, token.Id,
	)
	if err != nil {
		return err
	}

	dt.tokens[token.Id] = token
	if token.FCMToken != "" {
		dt.tokenIndex[token.FCMToken] = token
	} else if token.Token != "" {
		dt.tokenIndex[token.Token] = token
	}
	return nil
}

func (dt *DeviceTokens) Delete(id uint64, db *Database) error {
	dt.mutex.Lock()
	defer dt.mutex.Unlock()

	token, exists := dt.tokens[id]
	if !exists {
		return fmt.Errorf("device token not found")
	}

	// Log deletion with truncated token for security
	truncatedToken := token.Token
	if len(truncatedToken) > 10 {
		truncatedToken = truncatedToken[:10] + "..."
	}
	log.Printf("DeviceTokens.Delete: removing device token ID %d for user %d (token: %s, platform: %s)",
		id, token.UserId, truncatedToken, token.Platform)

	_, err := db.Sql.Exec(`DELETE FROM "deviceTokens" WHERE "deviceTokenId" = $1`, id)
	if err != nil {
		return err
	}

	delete(dt.tokens, id)
	if token.FCMToken != "" {
		delete(dt.tokenIndex, token.FCMToken)
	}
	if token.Token != "" {
		delete(dt.tokenIndex, token.Token)
	}

	// Remove from userTokens map
	userTokens := dt.userTokens[token.UserId]
	for i, t := range userTokens {
		if t.Id == id {
			dt.userTokens[token.UserId] = append(userTokens[:i], userTokens[i+1:]...)
			break
		}
	}

	return nil
}

func (dt *DeviceTokens) GetByUser(userId uint64) []*DeviceToken {
	dt.mutex.RLock()
	defer dt.mutex.RUnlock()

	tokens := dt.userTokens[userId]
	if tokens == nil {
		return []*DeviceToken{} // Return empty slice instead of nil
	}

	// Return a copy to prevent external modification
	result := make([]*DeviceToken, len(tokens))
	copy(result, tokens)
	return result
}

func (dt *DeviceTokens) FindByUserAndToken(userId uint64, token string) *DeviceToken {
	dt.mutex.RLock()
	defer dt.mutex.RUnlock()

	for _, t := range dt.userTokens[userId] {
		if t.Token == token || t.FCMToken == token {
			return t
		}
	}
	return nil
}

// RemoveAllLegacyTokensForUser removes all device tokens that do not have an FCM token
// (i.e. old OneSignal registrations). Called when a user successfully registers via FCM
// so stale tokens are not left in the database.
func (dt *DeviceTokens) RemoveAllLegacyTokensForUser(userId uint64, db *Database) error {
	dt.mutex.Lock()
	defer dt.mutex.Unlock()

	userTokens := dt.userTokens[userId]
	if len(userTokens) == 0 {
		return nil
	}

	var toDelete []uint64
	for _, t := range userTokens {
		if t.FCMToken == "" || t.PushType == "onesignal" {
			toDelete = append(toDelete, t.Id)
		}
	}
	if len(toDelete) == 0 {
		return nil
	}

	log.Printf("DeviceTokens.RemoveAllLegacyTokensForUser: removing %d legacy token(s) for user %d", len(toDelete), userId)

	for _, id := range toDelete {
		if _, err := db.Sql.Exec(`DELETE FROM "deviceTokens" WHERE "deviceTokenId" = $1`, id); err != nil {
			log.Printf("DeviceTokens.RemoveAllLegacyTokensForUser: error deleting token %d: %v", id, err)
			continue
		}

		if token := dt.tokens[id]; token != nil {
			delete(dt.tokens, id)
			if token.Token != "" {
				delete(dt.tokenIndex, token.Token)
			}
			if token.FCMToken != "" {
				delete(dt.tokenIndex, token.FCMToken)
			}
			updated := dt.userTokens[userId][:0]
			for _, t := range dt.userTokens[userId] {
				if t.Id != id {
					updated = append(updated, t)
				}
			}
			dt.userTokens[userId] = updated
			log.Printf("DeviceTokens.RemoveAllLegacyTokensForUser: removed token ID %d (platform: %s)", id, token.Platform)
		}
	}

	return nil
}
