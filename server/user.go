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
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base32"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"
)

type User struct {
	Id                   uint64
	Email                string
	Password             string
	Verified             bool
	VerificationToken    string
	CreatedAt            string
	LastLogin            string
	FirstName            string
	LastName             string
	ZipCode              string
	Systems              string
	Talkgroups           string // JSON array of talkgroup IDs or "*" for all
	Delay                int
	SystemDelays         string
	TalkgroupDelays      string
	Settings             string // JSON string for user settings (tag colors, etc.)
	Pin                  string
	PinExpiresAt         uint64
	ConnectionLimit      uint
	StripeCustomerId     string
	StripeSubscriptionId string
	SubscriptionStatus   string
	UserGroupId          uint64
	IsGroupAdmin         bool
	SystemAdmin          bool   // System administrator flag
	ResetCode            string
	ResetCodeExpires     uint64
	EmailChangeCode         string
	EmailChangeCodeExpires  uint64
	PasswordChangeCode      string
	PasswordChangeCodeExpires uint64
	AccountExpiresAt        uint64 // Unix timestamp, 0 = no expiration
	systemsData          any
	systemDelaysMap      map[uint64]uint
	talkgroupDelaysMap   map[string]uint
}

type Users struct {
	mutex sync.RWMutex
	users map[uint64]*User
	pins  map[string]*User
}

func NewUsers() *Users {
	return &Users{
		users: make(map[uint64]*User),
		pins:  make(map[string]*User),
	}
}

const userPinByteLength = 10

func generateUserPin() (string, error) {
	buf := make([]byte, userPinByteLength)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}

	encoder := base32.StdEncoding.WithPadding(base32.NoPadding)
	pin := strings.ToUpper(encoder.EncodeToString(buf))

	// Keep the pin reasonably short for usability.
	if len(pin) > 16 {
		pin = pin[:16]
	}

	return pin, nil
}

func NewUser(email, password string) *User {
	user := &User{
		Email:                email,
		Verified:             false,
		VerificationToken:    "",
		CreatedAt:            fmt.Sprintf("%d", time.Now().Unix()), // Initialize with current timestamp
		LastLogin:            "0",                                   // 0 means never logged in
		Systems:              "",
		Talkgroups:           "",
		Delay:                0,
		SystemDelays:         "",
		TalkgroupDelays:      "",
		StripeCustomerId:     "",
		StripeSubscriptionId: "",
		SubscriptionStatus:   "",
		PinExpiresAt:         0,
		ConnectionLimit:      0,
	}

	// Hash the password
	user.SetPassword(password)

	// Generate verification token
	user.GenerateVerificationToken()

	// Note: CreatedAt is already set above during struct initialization
	// No need to call SetCreatedAt() again

	if pin, err := generateUserPin(); err == nil {
		user.Pin = pin
	} else {
		user.Pin = ""
	}

	return user
}

func (u *User) ensurePinsLoaded() {
	if u.Pin != "" {
		return
	}

	if pin, err := generateUserPin(); err == nil {
		u.Pin = pin
	}
}

func parseUintFromAny(v any) (uint, bool) {
	switch value := v.(type) {
	case float64:
		return uint(value), true
	case int:
		return uint(value), true
	case int32:
		return uint(value), true
	case int64:
		return uint(value), true
	case uint:
		return value, true
	case uint32:
		return uint(value), true
	case uint64:
		return uint(value), true
	case string:
		if parsed, err := strconv.ParseUint(value, 10, 64); err == nil {
			return uint(parsed), true
		}
	}

	return 0, false
}

func (u *User) loadSystemScopes() {
	if strings.TrimSpace(u.Systems) == "" {
		u.systemsData = nil
		return
	}

	if strings.TrimSpace(u.Systems) == "*" {
		u.systemsData = "*"
		return
	}

	var parsed any
	if err := json.Unmarshal([]byte(u.Systems), &parsed); err != nil {
		u.systemsData = "*"
		return
	}

	u.systemsData = parsed
}

func (u *User) loadDelayMaps() {
	u.systemDelaysMap = map[uint64]uint{}
	u.talkgroupDelaysMap = map[string]uint{}

	if strings.TrimSpace(u.SystemDelays) != "" {
		var raw map[string]any
		if err := json.Unmarshal([]byte(u.SystemDelays), &raw); err == nil {
			for key, val := range raw {
				if delay, ok := parseUintFromAny(val); ok {
					if parsedKey, err := strconv.ParseUint(key, 10, 64); err == nil {
						u.systemDelaysMap[parsedKey] = delay
					}
				}
			}
		}
	}

	if strings.TrimSpace(u.TalkgroupDelays) != "" {
		var raw map[string]any
		if err := json.Unmarshal([]byte(u.TalkgroupDelays), &raw); err == nil {
			for key, val := range raw {
				if delay, ok := parseUintFromAny(val); ok {
					u.talkgroupDelaysMap[key] = delay
				}
			}
		}
	}
}

func (users *Users) IsPinAvailable(pin string, excludeID uint64) bool {
	pin = strings.TrimSpace(pin)
	if pin == "" {
		return true
	}

	users.mutex.RLock()
	defer users.mutex.RUnlock()

	existing, ok := users.pins[pin]
	if !ok || existing == nil {
		return true
	}

	return existing.Id == excludeID
}

func (users *Users) GenerateUniquePin(excludeID uint64) (string, error) {
	const maxAttempts = 1000

	for attempts := 0; attempts < maxAttempts; attempts++ {
		pin, err := generateUserPin()
		if err != nil {
			return "", err
		}

		if users.IsPinAvailable(pin, excludeID) {
			return pin, nil
		}
	}

	return "", fmt.Errorf("unable to generate unique pin after %d attempts", maxAttempts)
}

func (u *User) HasAccess(call *Call) bool {
	if u == nil || call == nil || call.System == nil || call.Talkgroup == nil {
		return true
	}

	// If user has a group, use group's system access
	// Note: This requires access to Controller, which we'll handle in the caller
	// For now, fall back to user-level access if group is not available
	if u.UserGroupId > 0 {
		// Group access will be checked in controller where we have access to UserGroups
		// This method signature will need to be updated or we check group in the caller
		// For backward compatibility, we'll check both
	}

	// Fall back to user-level access check
	if u.systemsData == nil {
		return true
	}

	switch v := u.systemsData.(type) {
	case string:
		return strings.TrimSpace(v) == "" || v == "*"
	case []any:
		for _, scope := range v {
			scopeMap, ok := scope.(map[string]any)
			if !ok {
				continue
			}

			idVal, ok := scopeMap["id"]
			if !ok {
				continue
			}

			var systemRef uint
			switch id := idVal.(type) {
			case float64:
				systemRef = uint(id)
			case string:
				if parsed, err := strconv.ParseUint(id, 10, 32); err == nil {
					systemRef = uint(parsed)
				}
			}
			if systemRef != uint(call.System.SystemRef) {
				continue
			}

			if tg, ok := scopeMap["talkgroups"]; ok {
				switch talkgroups := tg.(type) {
				case string:
					if talkgroups == "*" {
						return true
					}
				case []any:
					for _, entry := range talkgroups {
						switch talkgroupRef := entry.(type) {
						case float64:
							if uint(talkgroupRef) == uint(call.Talkgroup.TalkgroupRef) {
								return true
							}
						case string:
							if parsed, err := strconv.ParseUint(talkgroupRef, 10, 32); err == nil && uint(parsed) == uint(call.Talkgroup.TalkgroupRef) {
								return true
							}
						}
					}
				}
			} else {
				// No talkgroups restriction means whole system allowed
				return true
			}
		}
	default:
		return true
	}

	return false
}

func (u *User) PinExpired() bool {
	if u == nil || u.PinExpiresAt == 0 {
		return false
	}
	return uint64(time.Now().Unix()) > u.PinExpiresAt
}

func (u *User) EffectiveDelay(call *Call, defaultDelay uint) uint {
	if u == nil || call == nil || call.System == nil || call.Talkgroup == nil {
		return defaultDelay
	}

	// If user has a group, use group's delay settings
	// Note: This requires access to Controller, which we'll handle in the caller
	// For now, fall back to user-level delays if group is not available
	if u.UserGroupId > 0 {
		// Group delays will be checked in controller where we have access to UserGroups
		// This method signature will need to be updated or we check group in the caller
		// For backward compatibility, we'll check both
	}

	// Fall back to user-level delays
	if len(u.talkgroupDelaysMap) > 0 {
		key := fmt.Sprintf("%d:%d", call.System.SystemRef, call.Talkgroup.TalkgroupRef)
		if delay, ok := u.talkgroupDelaysMap[key]; ok && delay > 0 {
			return delay
		}
	}

	if len(u.systemDelaysMap) > 0 {
		if delay, ok := u.systemDelaysMap[uint64(call.System.SystemRef)]; ok && delay > 0 {
			return delay
		}
	}

	if u.Delay > 0 {
		return uint(u.Delay)
	}

	return defaultDelay
}

func (u *User) HashPassword(password string) error {
	hash := sha256.Sum256([]byte(password))
	u.Password = hex.EncodeToString(hash[:])
	return nil
}

func (u *User) VerifyPassword(password string) bool {
	hash := sha256.Sum256([]byte(password))
	return u.Password == hex.EncodeToString(hash[:])
}

func (u *User) SetPassword(password string) error {
	return u.HashPassword(password)
}

func (u *User) CheckPassword(password string) bool {
	return u.VerifyPassword(password)
}

func (u *User) SetCreatedAt() {
	u.CreatedAt = fmt.Sprintf("%d", time.Now().Unix())
}

func (u *User) UpdateLastLogin() {
	u.LastLogin = fmt.Sprintf("%d", time.Now().Unix())
}

func (u *User) GenerateVerificationToken() error {
	bytes := make([]byte, 32)
	if _, err := rand.Read(bytes); err != nil {
		return err
	}
	u.VerificationToken = hex.EncodeToString(bytes)
	return nil
}

// GenerateResetCode generates a 6-digit numeric reset code
func (u *User) GenerateResetCode() (string, error) {
	// Generate a random number between 0 and 999999
	bytes := make([]byte, 4)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	// Convert bytes to a uint32 and modulo to get 0-999999
	randomNum := uint32(bytes[0])<<24 | uint32(bytes[1])<<16 | uint32(bytes[2])<<8 | uint32(bytes[3])
	codeNum := randomNum % 1000000
	// Format as 6-digit string with leading zeros
	code := fmt.Sprintf("%06d", codeNum)
	u.ResetCode = code
	// Set expiration to 15 minutes from now
	u.ResetCodeExpires = uint64(time.Now().Add(15 * time.Minute).Unix())
	return code, nil
}

// VerifyResetCode checks if the provided code matches and hasn't expired
func (u *User) VerifyResetCode(code string) bool {
	if u.ResetCode == "" || code == "" {
		return false
	}
	if u.ResetCode != code {
		return false
	}
	// Check if code has expired
	if u.ResetCodeExpires == 0 || time.Now().Unix() > int64(u.ResetCodeExpires) {
		return false
	}
	return true
}

// GenerateEmailChangeCode generates a 6-digit numeric code for email change verification
func (u *User) GenerateEmailChangeCode() (string, error) {
	// Generate a random number between 0 and 999999
	bytes := make([]byte, 4)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	// Convert bytes to a uint32 and modulo to get 0-999999
	randomNum := uint32(bytes[0])<<24 | uint32(bytes[1])<<16 | uint32(bytes[2])<<8 | uint32(bytes[3])
	codeNum := randomNum % 1000000
	// Format as 6-digit string with leading zeros
	code := fmt.Sprintf("%06d", codeNum)
	u.EmailChangeCode = code
	// Set expiration to 15 minutes from now
	u.EmailChangeCodeExpires = uint64(time.Now().Add(15 * time.Minute).Unix())
	return code, nil
}

// VerifyEmailChangeCode checks if the provided code matches and hasn't expired
func (u *User) VerifyEmailChangeCode(code string) bool {
	if u.EmailChangeCode == "" || code == "" {
		return false
	}
	if u.EmailChangeCode != code {
		return false
	}
	// Check if code has expired
	if u.EmailChangeCodeExpires == 0 || time.Now().Unix() > int64(u.EmailChangeCodeExpires) {
		return false
	}
	return true
}

// ClearEmailChangeCode clears the email change verification code
func (u *User) ClearEmailChangeCode() {
	u.EmailChangeCode = ""
	u.EmailChangeCodeExpires = 0
}

// GeneratePasswordChangeCode generates a 6-digit numeric code for password change verification
func (u *User) GeneratePasswordChangeCode() (string, error) {
	// Generate a random number between 0 and 999999
	bytes := make([]byte, 4)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	// Convert bytes to a uint32 and modulo to get 0-999999
	randomNum := uint32(bytes[0])<<24 | uint32(bytes[1])<<16 | uint32(bytes[2])<<8 | uint32(bytes[3])
	codeNum := randomNum % 1000000
	// Format as 6-digit string with leading zeros
	code := fmt.Sprintf("%06d", codeNum)
	u.PasswordChangeCode = code
	// Set expiration to 15 minutes from now
	u.PasswordChangeCodeExpires = uint64(time.Now().Add(15 * time.Minute).Unix())
	return code, nil
}

// VerifyPasswordChangeCode checks if the provided code matches and hasn't expired
func (u *User) VerifyPasswordChangeCode(code string) bool {
	if u.PasswordChangeCode == "" || code == "" {
		return false
	}
	if u.PasswordChangeCode != code {
		return false
	}
	// Check if code has expired
	if u.PasswordChangeCodeExpires == 0 || time.Now().Unix() > int64(u.PasswordChangeCodeExpires) {
		return false
	}
	return true
}

// ClearPasswordChangeCode clears the password change verification code
func (u *User) ClearPasswordChangeCode() {
	u.PasswordChangeCode = ""
	u.PasswordChangeCodeExpires = 0
}

func (users *Users) Add(user *User) error {
	users.mutex.Lock()
	defer users.mutex.Unlock()

	user.ensurePinsLoaded()
	user.loadSystemScopes()
	user.loadDelayMaps()

	// For new users, don't store in memory until after database save
	// The database will assign the real ID
	if user.Id == 0 {
		// Don't store in memory yet - will be stored after database save
		return nil
	}

	users.users[user.Id] = user
	if user.Pin != "" {
		user.Pin = strings.TrimSpace(user.Pin)
		users.pins[user.Pin] = user
	}
	return nil
}

func (users *Users) Update(user *User) error {
	users.mutex.Lock()
	defer users.mutex.Unlock()

	user.ensurePinsLoaded()
	user.loadSystemScopes()
	user.loadDelayMaps()

	if existing, ok := users.users[user.Id]; ok && existing.Pin != "" && existing.Pin != user.Pin {
		delete(users.pins, existing.Pin)
	}

	users.users[user.Id] = user
	if user.Pin != "" {
		user.Pin = strings.TrimSpace(user.Pin)
		users.pins[user.Pin] = user
	}
	return nil
}

func (users *Users) Remove(id uint64) error {
	users.mutex.Lock()
	defer users.mutex.Unlock()

	if user, ok := users.users[id]; ok {
		if user.Pin != "" {
			delete(users.pins, user.Pin)
		}
		delete(users.users, id)
	}
	return nil
}

func (users *Users) Read(db *Database) error {
	formatError := errorFormatter("users", "read")

	users.mutex.Lock()
	defer users.mutex.Unlock()

	users.users = make(map[uint64]*User)
	users.pins = make(map[string]*User)

	rows, err := db.Sql.Query(`SELECT "userId", "email", "password", "pin", "pinExpiresAt", "connectionLimit", "verified", "verificationToken", "createdAt", "lastLogin", "firstName", "lastName", "zipCode", "systems", "talkgroups", "delay", "systemDelays", "talkgroupDelays", "settings", "stripeCustomerId", "stripeSubscriptionId", "subscriptionStatus", "userGroupId", "isGroupAdmin", COALESCE("systemAdmin", false), "resetCode", "resetCodeExpires", "accountExpiresAt" FROM "users"`)
	if err != nil {
		return formatError(err, "")
	}
	defer rows.Close()

	for rows.Next() {
		user := &User{}
		var systems, talkgroups, systemDelays, talkgroupDelays sql.NullString
		var pin sql.NullString
		var pinExpiresAt sql.NullInt64
		var connectionLimit sql.NullInt64
		var settings sql.NullString
		var stripeCustomerId, stripeSubscriptionId, subscriptionStatus sql.NullString
		var userGroupId sql.NullInt64
		var isGroupAdmin sql.NullBool
		var systemAdmin sql.NullBool
		var resetCode sql.NullString
		var resetCodeExpires sql.NullInt64
		var accountExpiresAt sql.NullInt64

		err := rows.Scan(&user.Id, &user.Email, &user.Password, &pin, &pinExpiresAt, &connectionLimit, &user.Verified, &user.VerificationToken, &user.CreatedAt, &user.LastLogin, &user.FirstName, &user.LastName, &user.ZipCode, &systems, &talkgroups, &user.Delay, &systemDelays, &talkgroupDelays, &settings, &stripeCustomerId, &stripeSubscriptionId, &subscriptionStatus, &userGroupId, &isGroupAdmin, &systemAdmin, &resetCode, &resetCodeExpires, &accountExpiresAt)
		if err != nil {
			return formatError(err, "")
		}

		if pin.Valid {
			user.Pin = strings.TrimSpace(pin.String)
		}
		if pinExpiresAt.Valid {
			user.PinExpiresAt = uint64(pinExpiresAt.Int64)
		}
		if connectionLimit.Valid && connectionLimit.Int64 > 0 {
			user.ConnectionLimit = uint(connectionLimit.Int64)
		}

		if systems.Valid {
			user.Systems = systems.String
		}
		if talkgroups.Valid {
			user.Talkgroups = talkgroups.String
		}
		if systemDelays.Valid {
			user.SystemDelays = systemDelays.String
		}
		if talkgroupDelays.Valid {
			user.TalkgroupDelays = talkgroupDelays.String
		}
		if settings.Valid {
			user.Settings = settings.String
		}
		if stripeCustomerId.Valid {
			user.StripeCustomerId = stripeCustomerId.String
		}
		if stripeSubscriptionId.Valid {
			user.StripeSubscriptionId = stripeSubscriptionId.String
		}
		if subscriptionStatus.Valid {
			user.SubscriptionStatus = subscriptionStatus.String
		}
		if userGroupId.Valid {
			user.UserGroupId = uint64(userGroupId.Int64)
		}
		if isGroupAdmin.Valid {
			user.IsGroupAdmin = isGroupAdmin.Bool
		}
		if systemAdmin.Valid {
			user.SystemAdmin = systemAdmin.Bool
		}
		if resetCode.Valid {
			user.ResetCode = resetCode.String
		}
		if resetCodeExpires.Valid {
			user.ResetCodeExpires = uint64(resetCodeExpires.Int64)
		}
		if accountExpiresAt.Valid {
			user.AccountExpiresAt = uint64(accountExpiresAt.Int64)
		}

		if settings.Valid {
			user.Settings = settings.String
		}
		
		user.ensurePinsLoaded()
		user.loadSystemScopes()
		user.loadDelayMaps()

	users.users[user.Id] = user
	if user.Pin != "" {
		user.Pin = strings.TrimSpace(user.Pin)
		users.pins[user.Pin] = user
	}
	}

	return nil
}

func (users *Users) Write(db *Database) error {
	formatError := errorFormatter("users", "write")

	users.mutex.RLock()
	defer users.mutex.RUnlock()

	// If no users in memory, this might be a new user registration
	// We need to handle this case differently
	if len(users.users) == 0 {
		return nil
	}

	for _, user := range users.users {
		// Use empty strings instead of NULL for NOT NULL columns
		systems := user.Systems
		talkgroups := user.Talkgroups
		systemDelays := user.SystemDelays
		talkgroupDelays := user.TalkgroupDelays
		settings := user.Settings
		stripeCustomerId := user.StripeCustomerId
		stripeSubscriptionId := user.StripeSubscriptionId
		subscriptionStatus := user.SubscriptionStatus
		user.ensurePinsLoaded()
		pin := user.Pin
		pinExpiresAt := int64(user.PinExpiresAt)
		connectionLimit := int64(user.ConnectionLimit)

		var err error
		if user.Id == 0 {
			// New user - let database auto-generate ID
			// Handle timestamp fields properly - keep as strings since columns are text type
			var createdAtStr, lastLoginStr string

			// Parse createdAt timestamp
			if user.CreatedAt != "" {
				// Verify it's a valid timestamp format, otherwise use current time
				if _, err := strconv.ParseInt(user.CreatedAt, 10, 64); err == nil {
					createdAtStr = user.CreatedAt
				} else {
					createdAtStr = strconv.FormatInt(time.Now().Unix(), 10)
				}
			} else {
				createdAtStr = strconv.FormatInt(time.Now().Unix(), 10)
			}

			// Parse lastLogin timestamp
			if user.LastLogin != "" {
				// Verify it's a valid timestamp format, otherwise use 0
				if _, err := strconv.ParseInt(user.LastLogin, 10, 64); err == nil {
					lastLoginStr = user.LastLogin
				} else {
					lastLoginStr = "0"
				}
			} else {
				lastLoginStr = "0"
			}

			var resetCodeVal interface{}
			var resetCodeExpiresVal interface{}
			if user.ResetCode != "" {
				resetCodeVal = user.ResetCode
			} else {
				resetCodeVal = ""
			}
			if user.ResetCodeExpires > 0 {
				resetCodeExpiresVal = int64(user.ResetCodeExpires)
			} else {
				resetCodeExpiresVal = int64(0)
			}

			var accountExpiresAtVal interface{}
			if user.AccountExpiresAt > 0 {
				accountExpiresAtVal = int64(user.AccountExpiresAt)
			} else {
				accountExpiresAtVal = int64(0)
			}

		result, err := db.Sql.Exec(`INSERT INTO "users" ("email", "password", "pin", "pinExpiresAt", "connectionLimit", "verified", "verificationToken", "createdAt", "lastLogin", "firstName", "lastName", "zipCode", "systems", "talkgroups", "delay", "systemDelays", "talkgroupDelays", "settings", "stripeCustomerId", "stripeSubscriptionId", "subscriptionStatus", "userGroupId", "isGroupAdmin", "systemAdmin", "resetCode", "resetCodeExpires", "accountExpiresAt") VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20, $21, $22, $23, $24, $25, $26, $27)`,
			user.Email, user.Password, pin, pinExpiresAt, connectionLimit, user.Verified, user.VerificationToken, createdAtStr, lastLoginStr, user.FirstName, user.LastName, user.ZipCode, systems, talkgroups, user.Delay, systemDelays, talkgroupDelays, settings, stripeCustomerId, stripeSubscriptionId, subscriptionStatus, user.UserGroupId, user.IsGroupAdmin, user.SystemAdmin, resetCodeVal, resetCodeExpiresVal, accountExpiresAtVal)
			if err != nil {
				return formatError(err, "")
			}
			// Get the auto-generated ID
			id, err := result.LastInsertId()
			if err != nil {
				return formatError(err, "")
			}
			user.Id = uint64(id)
		} else {
			// Existing user - update
			// Handle timestamp fields properly - keep as strings since columns are text type
			var createdAtStr, lastLoginStr string

			// Parse createdAt timestamp
			if user.CreatedAt != "" {
				// Verify it's a valid timestamp format, otherwise use current time
				if _, err := strconv.ParseInt(user.CreatedAt, 10, 64); err == nil {
					createdAtStr = user.CreatedAt
				} else {
					createdAtStr = strconv.FormatInt(time.Now().Unix(), 10)
				}
			} else {
				createdAtStr = strconv.FormatInt(time.Now().Unix(), 10)
			}

			// Parse lastLogin timestamp
			if user.LastLogin != "" {
				// Verify it's a valid timestamp format, otherwise use 0
				if _, err := strconv.ParseInt(user.LastLogin, 10, 64); err == nil {
					lastLoginStr = user.LastLogin
				} else {
					lastLoginStr = "0"
				}
			} else {
				lastLoginStr = "0"
			}

			var resetCodeVal interface{}
			var resetCodeExpiresVal interface{}
			if user.ResetCode != "" {
				resetCodeVal = user.ResetCode
			} else {
				resetCodeVal = ""
			}
			if user.ResetCodeExpires > 0 {
				resetCodeExpiresVal = int64(user.ResetCodeExpires)
			} else {
				resetCodeExpiresVal = int64(0)
			}

			var accountExpiresAtVal interface{}
			if user.AccountExpiresAt > 0 {
				accountExpiresAtVal = int64(user.AccountExpiresAt)
			} else {
				accountExpiresAtVal = int64(0)
			}

		_, err = db.Sql.Exec(`UPDATE "users" SET "email"=$1, "password"=$2, "pin"=$3, "pinExpiresAt"=$4, "connectionLimit"=$5, "verified"=$6, "verificationToken"=$7, "createdAt"=$8, "lastLogin"=$9, "firstName"=$10, "lastName"=$11, "zipCode"=$12, "systems"=$13, "talkgroups"=$14, "delay"=$15, "systemDelays"=$16, "talkgroupDelays"=$17, "settings"=$18, "stripeCustomerId"=$19, "stripeSubscriptionId"=$20, "subscriptionStatus"=$21, "userGroupId"=$22, "isGroupAdmin"=$23, "systemAdmin"=$24, "resetCode"=$25, "resetCodeExpires"=$26, "accountExpiresAt"=$27 WHERE "userId"=$28`,
			user.Email, user.Password, pin, pinExpiresAt, connectionLimit, user.Verified, user.VerificationToken, createdAtStr, lastLoginStr, user.FirstName, user.LastName, user.ZipCode, systems, talkgroups, user.Delay, systemDelays, talkgroupDelays, settings, stripeCustomerId, stripeSubscriptionId, subscriptionStatus, user.UserGroupId, user.IsGroupAdmin, user.SystemAdmin, resetCodeVal, resetCodeExpiresVal, accountExpiresAtVal, user.Id)
			if err != nil {
				return formatError(err, "")
			}
		}
	}

	return nil
}

func (users *Users) GetUserByEmail(email string) *User {
	users.mutex.RLock()
	defer users.mutex.RUnlock()

	// Normalize email to lowercase for case-insensitive comparison
	normalizedEmail := NormalizeEmail(email)
	
	for _, user := range users.users {
		if NormalizeEmail(user.Email) == normalizedEmail {
			return user
		}
	}
	return nil
}

func (users *Users) GetUserByPin(pin string) *User {
	users.mutex.RLock()
	defer users.mutex.RUnlock()

	if pin == "" {
		return nil
	}

	pin = strings.TrimSpace(pin)
	return users.pins[pin]
}

func (users *Users) GetUserById(id uint64) *User {
	users.mutex.RLock()
	defer users.mutex.RUnlock()

	return users.users[id]
}

func (users *Users) GetUserByStripeCustomerId(customerId string) *User {
	users.mutex.RLock()
	defer users.mutex.RUnlock()

	for _, user := range users.users {
		if user.StripeCustomerId == customerId {
			return user
		}
	}
	return nil
}

func (users *Users) GetAllUsers() []*User {
	users.mutex.RLock()
	defer users.mutex.RUnlock()

	var userList []*User
	for _, user := range users.users {
		userList = append(userList, user)
	}
	return userList
}

// CheckDuplicateEmails finds users with duplicate emails (case-insensitive)
// Returns a map of normalized email -> list of users with that email
func (users *Users) CheckDuplicateEmails() map[string][]*User {
	users.mutex.RLock()
	defer users.mutex.RUnlock()

	emailMap := make(map[string][]*User)
	
	// Group users by normalized email
	for _, user := range users.users {
		normalizedEmail := NormalizeEmail(user.Email)
		emailMap[normalizedEmail] = append(emailMap[normalizedEmail], user)
	}
	
	// Filter to only duplicates
	duplicates := make(map[string][]*User)
	for email, userList := range emailMap {
		if len(userList) > 1 {
			duplicates[email] = userList
		}
	}
	
	return duplicates
}

func (users *Users) SaveNewUser(user *User, db *Database) error {
	formatError := errorFormatter("users", "saveNewUser")

	user.ensurePinsLoaded()
	
	// All these columns are NOT NULL, so use empty string instead of NULL
	systems := user.Systems
	systemDelays := user.SystemDelays
	talkgroupDelays := user.TalkgroupDelays
	settings := user.Settings
	stripeCustomerId := user.StripeCustomerId
	stripeSubscriptionId := user.StripeSubscriptionId
	subscriptionStatus := user.SubscriptionStatus

	// Insert new user - let database auto-generate ID
	var userId int64

	// Handle timestamp fields properly - keep as strings since columns are text type
	var createdAtStr, lastLoginStr string

	// Parse createdAt timestamp
	if user.CreatedAt != "" {
		// Verify it's a valid timestamp format, otherwise use current time
		if _, err := strconv.ParseInt(user.CreatedAt, 10, 64); err == nil {
			createdAtStr = user.CreatedAt
		} else {
			createdAtStr = strconv.FormatInt(time.Now().Unix(), 10)
		}
	} else {
		createdAtStr = strconv.FormatInt(time.Now().Unix(), 10)
	}

	// Parse lastLogin timestamp
	if user.LastLogin != "" {
		// Verify it's a valid timestamp format, otherwise use 0
		if _, err := strconv.ParseInt(user.LastLogin, 10, 64); err == nil {
			lastLoginStr = user.LastLogin
		} else {
			lastLoginStr = "0"
		}
	} else {
		lastLoginStr = "0"
	}

	// Insert user with all fields including systems, delays, settings, and Stripe data
	err := db.Sql.QueryRow(`INSERT INTO "users" ("email", "password", "pin", "pinExpiresAt", "connectionLimit", "verified", "verificationToken", "createdAt", "lastLogin", "firstName", "lastName", "zipCode", "systems", "talkgroups", "delay", "systemDelays", "talkgroupDelays", "settings", "stripeCustomerId", "stripeSubscriptionId", "subscriptionStatus", "accountExpiresAt", "userGroupId", "isGroupAdmin", "systemAdmin") VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20, $21, $22, $23, $24, $25) RETURNING "userId"`,
		user.Email, user.Password, user.Pin, user.PinExpiresAt, user.ConnectionLimit, user.Verified, user.VerificationToken, createdAtStr, lastLoginStr, user.FirstName, user.LastName, user.ZipCode, systems, user.Talkgroups, user.Delay, systemDelays, talkgroupDelays, settings, stripeCustomerId, stripeSubscriptionId, subscriptionStatus, user.AccountExpiresAt, user.UserGroupId, user.IsGroupAdmin, user.SystemAdmin).Scan(&userId)
	if err != nil {
		return formatError(err, "")
	}
	user.Id = uint64(userId)
	user.loadSystemScopes()
	user.loadDelayMaps()

	// Now add to memory with the real ID
	users.mutex.Lock()
	users.users[user.Id] = user
	if user.Pin != "" {
		user.Pin = strings.TrimSpace(user.Pin)
		users.pins[user.Pin] = user
	}
	users.mutex.Unlock()

	return nil
}

func (users *Users) HasPins() bool {
	users.mutex.RLock()
	defer users.mutex.RUnlock()
	return len(users.pins) > 0
}
