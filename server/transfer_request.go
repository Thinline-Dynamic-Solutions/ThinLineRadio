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
	"database/sql"
	"log"
	"sync"
	"time"
)

type TransferRequest struct {
	Id                     uint64
	UserId                 uint64
	FromGroupId            uint64
	ToGroupId              uint64
	RequestedBy            uint64
	ApprovedBy             uint64
	Status                 string // "pending", "approved", "rejected"
	RequestedAt            int64
	ApprovedAt             int64
	ApprovalToken          string // Secure token for email-based approval
	ApprovalTokenExpiresAt int64  // Unix timestamp, 0 = no expiration
	ApprovalTokenUsed      bool   // Whether the token has been used
}

type TransferRequests struct {
	mutex    sync.RWMutex
	requests map[uint64]*TransferRequest
}

func NewTransferRequests() *TransferRequests {
	return &TransferRequests{
		requests: make(map[uint64]*TransferRequest),
	}
}

func (trs *TransferRequests) Load(db *Database) error {
	trs.mutex.Lock()
	defer trs.mutex.Unlock()

	rows, err := db.Sql.Query(`SELECT "transferRequestId", "userId", "fromGroupId", "toGroupId", "requestedBy", "approvedBy", "status", "requestedAt", "approvedAt", "approvalToken", "approvalTokenExpiresAt", "approvalTokenUsed" FROM "transferRequests" WHERE "status" = 'pending'`)
	if err != nil {
		return err
	}
	defer rows.Close()

	trs.requests = make(map[uint64]*TransferRequest)

	for rows.Next() {
		req := &TransferRequest{}
		var approvedBy sql.NullInt64
		var requestedAt sql.NullInt64
		var approvedAt sql.NullInt64
		var approvalToken sql.NullString
		var approvalTokenExpiresAt sql.NullInt64
		var approvalTokenUsed sql.NullBool

		err := rows.Scan(
			&req.Id,
			&req.UserId,
			&req.FromGroupId,
			&req.ToGroupId,
			&req.RequestedBy,
			&approvedBy,
			&req.Status,
			&requestedAt,
			&approvedAt,
			&approvalToken,
			&approvalTokenExpiresAt,
			&approvalTokenUsed,
		)
		if err != nil {
			log.Printf("Error loading transfer request: %v", err)
			continue
		}

		if approvedBy.Valid {
			req.ApprovedBy = uint64(approvedBy.Int64)
		}
		if requestedAt.Valid {
			req.RequestedAt = requestedAt.Int64
		}
		if approvedAt.Valid {
			req.ApprovedAt = approvedAt.Int64
		}
		if approvalToken.Valid {
			req.ApprovalToken = approvalToken.String
		}
		if approvalTokenExpiresAt.Valid {
			req.ApprovalTokenExpiresAt = approvalTokenExpiresAt.Int64
		}
		if approvalTokenUsed.Valid {
			req.ApprovalTokenUsed = approvalTokenUsed.Bool
		}

		trs.requests[req.Id] = req
	}

	return rows.Err()
}

func (trs *TransferRequests) Get(id uint64) *TransferRequest {
	trs.mutex.RLock()
	defer trs.mutex.RUnlock()
	return trs.requests[id]
}

func (trs *TransferRequests) GetAll() []*TransferRequest {
	trs.mutex.RLock()
	defer trs.mutex.RUnlock()
	requests := make([]*TransferRequest, 0, len(trs.requests))
	for _, req := range trs.requests {
		requests = append(requests, req)
	}
	return requests
}

func (trs *TransferRequests) GetByGroup(groupId uint64) []*TransferRequest {
	trs.mutex.RLock()
	defer trs.mutex.RUnlock()
	requests := []*TransferRequest{}
	for _, req := range trs.requests {
		if req.ToGroupId == groupId || req.FromGroupId == groupId {
			requests = append(requests, req)
		}
	}
	return requests
}

func (trs *TransferRequests) Add(req *TransferRequest, db *Database) error {
	if req.RequestedAt == 0 {
		req.RequestedAt = time.Now().Unix()
	}
	if req.Status == "" {
		req.Status = "pending"
	}

	var id int64
	err := db.Sql.QueryRow(
		`INSERT INTO "transferRequests" ("userId", "fromGroupId", "toGroupId", "requestedBy", "approvedBy", "status", "requestedAt", "approvedAt", "approvalToken", "approvalTokenExpiresAt", "approvalTokenUsed") 
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11) RETURNING "transferRequestId"`,
		req.UserId, req.FromGroupId, req.ToGroupId, req.RequestedBy, req.ApprovedBy, req.Status, req.RequestedAt, req.ApprovedAt, req.ApprovalToken, req.ApprovalTokenExpiresAt, req.ApprovalTokenUsed,
	).Scan(&id)

	if err != nil {
		return err
	}

	req.Id = uint64(id)

	trs.mutex.Lock()
	trs.requests[req.Id] = req
	trs.mutex.Unlock()

	return nil
}

func (trs *TransferRequests) Update(req *TransferRequest, db *Database) error {
	_, err := db.Sql.Exec(
		`UPDATE "transferRequests" SET "status" = $1, "approvedBy" = $2, "approvedAt" = $3, "approvalToken" = $4, "approvalTokenExpiresAt" = $5, "approvalTokenUsed" = $6 WHERE "transferRequestId" = $7`,
		req.Status, req.ApprovedBy, req.ApprovedAt, req.ApprovalToken, req.ApprovalTokenExpiresAt, req.ApprovalTokenUsed, req.Id,
	)

	if err != nil {
		return err
	}

	trs.mutex.Lock()
	trs.requests[req.Id] = req
	trs.mutex.Unlock()

	return nil
}

func (trs *TransferRequests) Delete(id uint64, db *Database) error {
	_, err := db.Sql.Exec(`DELETE FROM "transferRequests" WHERE "transferRequestId" = $1`, id)
	if err != nil {
		return err
	}

	trs.mutex.Lock()
	delete(trs.requests, id)
	trs.mutex.Unlock()

	return nil
}
