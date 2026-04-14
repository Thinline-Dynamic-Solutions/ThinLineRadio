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
	"database/sql"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"
)

const (
	registrationCodeLength = 12
	alphanumericChars      = "ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	specialChars           = "!@#$%^&*()_+-=[]{}|;:,.<>?"
)

type RegistrationCode struct {
	Id          uint64
	Code        string
	Label       string
	UserGroupId uint64
	CreatedBy   uint64
	ExpiresAt   int64
	MaxUses     int
	CurrentUses int
	IsOneTime   bool
	IsActive    bool
	CreatedAt   int64
}

type RegistrationCodes struct {
	mutex sync.RWMutex
	codes map[string]*RegistrationCode
}

func NewRegistrationCodes() *RegistrationCodes {
	return &RegistrationCodes{
		codes: make(map[string]*RegistrationCode),
	}
}

func generateRegistrationCode() (string, error) {
	// Generate a 12-character code with alphanumeric and at least one special character
	buf := make([]byte, registrationCodeLength)

	// First, ensure we have at least one special character
	specialBuf := make([]byte, 1)
	if _, err := rand.Read(specialBuf); err != nil {
		return "", err
	}
	specialPos := int(specialBuf[0]) % registrationCodeLength

	// Fill the rest with alphanumeric or special characters
	allChars := alphanumericChars + specialChars
	for i := 0; i < registrationCodeLength; i++ {
		if i == specialPos {
			// This position must be a special character
			charBuf := make([]byte, 1)
			if _, err := rand.Read(charBuf); err != nil {
				return "", err
			}
			buf[i] = specialChars[int(charBuf[0])%len(specialChars)]
		} else {
			// Other positions can be alphanumeric or special
			charBuf := make([]byte, 1)
			if _, err := rand.Read(charBuf); err != nil {
				return "", err
			}
			buf[i] = allChars[int(charBuf[0])%len(allChars)]
		}
	}

	code := string(buf)

	// Verify we have at least one special character (should always be true, but double-check)
	hasSpecial := false
	for _, char := range code {
		if strings.ContainsRune(specialChars, char) {
			hasSpecial = true
			break
		}
	}

	if !hasSpecial {
		// If somehow we don't have a special char, replace a random position
		replacePos := int(buf[0]) % registrationCodeLength
		specialBuf := make([]byte, 1)
		if _, err := rand.Read(specialBuf); err != nil {
			return "", err
		}
		code = code[:replacePos] + string(specialChars[int(specialBuf[0])%len(specialChars)]) + code[replacePos+1:]
	}

	return code, nil
}

func (rcs *RegistrationCodes) GenerateCode(groupId, createdBy uint64, label, customCode string, expiresAt int64, maxUses int, isOneTime bool) (*RegistrationCode, error) {
	var code string

	if customCode != "" {
		// Use the admin-supplied code after normalising and checking uniqueness
		code = strings.ToUpper(strings.TrimSpace(customCode))
		if rcs.GetByCode(code) != nil {
			return nil, fmt.Errorf("registration code already exists")
		}
	} else {
		// Auto-generate a random code
		var err error
		code, err = generateRegistrationCode()
		if err != nil {
			return nil, err
		}
		for rcs.GetByCode(code) != nil {
			code, err = generateRegistrationCode()
			if err != nil {
				return nil, err
			}
		}
	}

	regCode := &RegistrationCode{
		Code:        code,
		Label:       label,
		UserGroupId: groupId,
		CreatedBy:   createdBy,
		ExpiresAt:   expiresAt,
		MaxUses:     maxUses,
		CurrentUses: 0,
		IsOneTime:   isOneTime,
		IsActive:    true,
		CreatedAt:   time.Now().Unix(),
	}

	return regCode, nil
}

func (rcs *RegistrationCodes) Load(db *Database) error {
	rcs.mutex.Lock()
	defer rcs.mutex.Unlock()

	rows, err := db.Sql.Query(`SELECT "registrationCodeId", "code", "label", "userGroupId", "createdBy", "expiresAt", "maxUses", "currentUses", "isOneTime", "isActive", "createdAt" FROM "registrationCodes"`)
	if err != nil {
		return err
	}
	defer rows.Close()

	rcs.codes = make(map[string]*RegistrationCode)

	for rows.Next() {
		code := &RegistrationCode{}
		var expiresAt sql.NullInt64
		var createdAt sql.NullInt64
		var createdBy sql.NullInt64

		err := rows.Scan(
			&code.Id,
			&code.Code,
			&code.Label,
			&code.UserGroupId,
			&createdBy,
			&expiresAt,
			&code.MaxUses,
			&code.CurrentUses,
			&code.IsOneTime,
			&code.IsActive,
			&createdAt,
		)
		if err != nil {
			log.Printf("Error loading registration code: %v", err)
			continue
		}

		if createdBy.Valid {
			code.CreatedBy = uint64(createdBy.Int64)
		} else {
			code.CreatedBy = 0 // System admin created
		}

		if expiresAt.Valid {
			code.ExpiresAt = expiresAt.Int64
		}
		if createdAt.Valid {
			code.CreatedAt = createdAt.Int64
		} else {
			code.CreatedAt = time.Now().Unix()
		}

		rcs.codes[strings.ToUpper(code.Code)] = code
	}

	return rows.Err()
}

func (rcs *RegistrationCodes) GetByCode(code string) *RegistrationCode {
	rcs.mutex.RLock()
	defer rcs.mutex.RUnlock()
	return rcs.codes[strings.ToUpper(code)]
}

func (rcs *RegistrationCodes) Validate(code string) (*RegistrationCode, error) {
	regCode := rcs.GetByCode(code)
	if regCode == nil {
		return nil, fmt.Errorf("invalid registration code")
	}

	if !regCode.IsActive {
		return nil, fmt.Errorf("registration code is not active")
	}

	if regCode.ExpiresAt > 0 && time.Now().Unix() > regCode.ExpiresAt {
		return nil, fmt.Errorf("registration code has expired")
	}

	if regCode.MaxUses > 0 && regCode.CurrentUses >= regCode.MaxUses {
		return nil, fmt.Errorf("registration code has reached maximum uses")
	}

	return regCode, nil
}

func (rcs *RegistrationCodes) Use(code string, db *Database) error {
	regCode := rcs.GetByCode(code)
	if regCode == nil {
		return fmt.Errorf("invalid registration code")
	}

	regCode.CurrentUses++

	if regCode.IsOneTime {
		regCode.IsActive = false
	}

	_, err := db.Sql.Exec(
		`UPDATE "registrationCodes" SET "currentUses" = $1, "isActive" = $2 WHERE "registrationCodeId" = $3`,
		regCode.CurrentUses, regCode.IsActive, regCode.Id,
	)

	if err != nil {
		return err
	}

	rcs.mutex.Lock()
	rcs.codes[strings.ToUpper(code)] = regCode
	rcs.mutex.Unlock()

	return nil
}

func (rcs *RegistrationCodes) Add(code *RegistrationCode, db *Database) error {
	var id int64
	var createdBy interface{}

	// Use NULL if createdBy is 0 (system admin), otherwise use the user ID
	if code.CreatedBy == 0 {
		createdBy = nil
	} else {
		createdBy = code.CreatedBy
	}

	err := db.Sql.QueryRow(
		`INSERT INTO "registrationCodes" ("code", "label", "userGroupId", "createdBy", "expiresAt", "maxUses", "currentUses", "isOneTime", "isActive", "createdAt") 
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10) RETURNING "registrationCodeId"`,
		code.Code, code.Label, code.UserGroupId, createdBy, code.ExpiresAt, code.MaxUses, code.CurrentUses, code.IsOneTime, code.IsActive, code.CreatedAt,
	).Scan(&id)

	if err != nil {
		return err
	}

	code.Id = uint64(id)

	rcs.mutex.Lock()
	rcs.codes[strings.ToUpper(code.Code)] = code
	rcs.mutex.Unlock()

	return nil
}

func (rcs *RegistrationCodes) Delete(id uint64, db *Database) error {
	rcs.mutex.Lock()
	defer rcs.mutex.Unlock()

	// Find the code to remove from map
	var codeToDelete string
	for code, regCode := range rcs.codes {
		if regCode.Id == id {
			codeToDelete = code
			break
		}
	}

	_, err := db.Sql.Exec(`DELETE FROM "registrationCodes" WHERE "registrationCodeId" = $1`, id)
	if err != nil {
		return err
	}

	if codeToDelete != "" {
		delete(rcs.codes, codeToDelete)
	}

	return nil
}

func (rcs *RegistrationCodes) GetAll() []*RegistrationCode {
	rcs.mutex.RLock()
	defer rcs.mutex.RUnlock()
	codes := make([]*RegistrationCode, 0, len(rcs.codes))
	for _, code := range rcs.codes {
		codes = append(codes, code)
	}
	return codes
}
