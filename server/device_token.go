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
	Token     string // OneSignal player ID (legacy) or unique device identifier
	FCMToken  string // Firebase Cloud Messaging token
	PushType  string // "onesignal" or "fcm"
	Platform  string // "ios" or "android"
	Sound     string // Notification sound preference
	CreatedAt int64
	LastUsed  int64
}

type DeviceTokens struct {
	mutex      sync.RWMutex
	tokens     map[uint64]*DeviceToken // Map by device token ID
	userTokens map[uint64][]*DeviceToken // Map user ID to their devices
}

func NewDeviceTokens() *DeviceTokens {
	return &DeviceTokens{
		tokens:     make(map[uint64]*DeviceToken),
		userTokens: make(map[uint64][]*DeviceToken),
	}
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
			token.PushType = "onesignal" // Default to OneSignal for existing tokens
		}

		dt.tokens[token.Id] = token
		dt.userTokens[token.UserId] = append(dt.userTokens[token.UserId], token)
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
		if t.Token == token {
			return t
		}
	}
	return nil
}

// RemoveAllOneSignalTokensForUser removes all OneSignal tokens for a user
// This should be called when a user registers an FCM token
func (dt *DeviceTokens) RemoveAllOneSignalTokensForUser(userId uint64, db *Database) error {
	dt.mutex.Lock()
	defer dt.mutex.Unlock()

	userTokens := dt.userTokens[userId]
	if userTokens == nil {
		return nil // No tokens for this user
	}

	// Collect OneSignal token IDs to delete
	var toDelete []uint64
	for _, t := range userTokens {
		if t.PushType == "onesignal" || t.PushType == "" {
			toDelete = append(toDelete, t.Id)
		}
	}

	if len(toDelete) == 0 {
		return nil // No OneSignal tokens to delete
	}

	log.Printf("DeviceTokens.RemoveAllOneSignalTokensForUser: removing %d OneSignal tokens for user %d", len(toDelete), userId)

	// Delete from database
	for _, id := range toDelete {
		_, err := db.Sql.Exec(`DELETE FROM "deviceTokens" WHERE "deviceTokenId" = $1`, id)
		if err != nil {
			log.Printf("DeviceTokens.RemoveAllOneSignalTokensForUser: error deleting token %d: %v", id, err)
			continue
		}

		// Remove from memory
		token := dt.tokens[id]
		if token != nil {
			delete(dt.tokens, id)

			// Remove from userTokens map
			updatedUserTokens := []*DeviceToken{}
			for _, t := range dt.userTokens[userId] {
				if t.Id != id {
					updatedUserTokens = append(updatedUserTokens, t)
				}
			}
			dt.userTokens[userId] = updatedUserTokens

			// Log deletion with truncated token
			truncatedToken := token.Token
			if len(truncatedToken) > 10 {
				truncatedToken = truncatedToken[:10] + "..."
			}
			log.Printf("DeviceTokens.RemoveAllOneSignalTokensForUser: removed OneSignal token ID %d (token: %s, platform: %s)",
				id, truncatedToken, token.Platform)
		}
	}

	return nil
}

