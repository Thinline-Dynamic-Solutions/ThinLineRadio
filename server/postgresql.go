// Copyright (C) 2019-2024 Chrystian Huot <chrystian@huot.qc.ca>
// Modified by Thinline Dynamic Solutions
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

var PostgresqlSchema = []string{
	`CREATE TABLE IF NOT EXISTS "apikeys" (
    "apikeyId" bigserial NOT NULL PRIMARY KEY,
    "disabled" boolean NOT NULL DEFAULT false,
    "ident" text NOT NULL,
    "key" text NOT NULL,
    "order" integer NOT NULL DEFAULT 0,
    "systems" text NOT NULL DEFAULT ''
  );`,

	`CREATE TABLE IF NOT EXISTS "downstreams" (
    "downstreamId" bigserial NOT NULL PRIMARY KEY,
    "apikey" text NOT NULL,
    "disabled" boolean NOT NULL DEFAULT false,
    "name" text NOT NULL DEFAULT '',
    "order" integer NOT NULL DEFAULT 0,
    "systems" text NOT NULL DEFAULT '',
    "url" text NOT NULL
  );`,

	`CREATE TABLE IF NOT EXISTS "groups" (
    "groupId" bigserial NOT NULL PRIMARY KEY,
    "label" text NOT NULL,
    "order" integer NOT NULL DEFAULT 0
  );`,

	`CREATE TABLE IF NOT EXISTS "tags" (
    "tagId" bigserial NOT NULL PRIMARY KEY,
    "label" text NOT NULL,
    "order" integer NOT NULL DEFAULT 0
  );`,

	`CREATE TABLE IF NOT EXISTS "systems" (
    "systemId" bigserial NOT NULL PRIMARY KEY,
    "autoPopulate" boolean NOT NULL DEFAULT false,
    "blacklists" text NOT NULL DEFAULT '',
    "delay" integer NOT NULL DEFAULT 0,
    "label" text NOT NULL,
    "order" integer NOT NULL DEFAULT 0,
    "systemRef" integer NOT NULL,
    "type" text NOT NULL DEFAULT '',
    "noAudioAlertsEnabled" boolean NOT NULL DEFAULT true,
    "noAudioThresholdMinutes" integer NOT NULL DEFAULT 30
  );`,

	`CREATE TABLE IF NOT EXISTS "sites" (
    "siteId" bigserial NOT NULL PRIMARY KEY,
    "label" text NOT NULL,
    "order" integer NOT NULL DEFAULT 0,
    "siteRef" integer NOT NULL,
    "systemId" bigint NOT NULL DEFAULT 0,
    CONSTRAINT "sites_systemId" FOREIGN KEY ("systemId") REFERENCES "systems" ("systemId") ON DELETE CASCADE ON UPDATE CASCADE
  );`,

	`CREATE TABLE IF NOT EXISTS "talkgroups" (
    "talkgroupId" bigserial NOT NULL PRIMARY KEY,
    "delay" integer NOT NULL DEFAULT 0,
    "frequency" integer NOT NULL DEFAULT 0,
    "label" text NOT NULL,
    "name" text NOT NULL,
    "order" integer NOT NULL DEFAULT 0,
    "systemId" bigint NOT NULL,
    "tagId" bigint NOT NULL,
    "talkgroupRef" integer NOT NULL,
    "type" TEXT NOT NULL DEFAULT '',
    "toneDetectionEnabled" boolean NOT NULL DEFAULT false,
    "toneSets" text NOT NULL DEFAULT '[]',
    "excludeFromPreferredSite" boolean NOT NULL DEFAULT false,
    CONSTRAINT "talkgroups_systemId_fkey" FOREIGN KEY ("systemId") REFERENCES "systems" ("systemId") ON DELETE CASCADE ON UPDATE CASCADE,
    CONSTRAINT "talkgroups_tagId_fkey" FOREIGN KEY ("tagId") REFERENCES "tags" ("tagId") ON DELETE CASCADE ON UPDATE CASCADE
  );`,

	`CREATE TABLE IF NOT EXISTS "talkgroupGroups" (
    "talkgroupGroupId" bigserial NOT NULL PRIMARY KEY,
    "groupId" bigint NOT NULL,
    "talkgroupId" bigint NOT NULL,
    CONSTRAINT "talkgroupGroups_groupId" FOREIGN KEY ("groupId") REFERENCES "groups" ("groupId") ON DELETE CASCADE ON UPDATE CASCADE,
    CONSTRAINT "talkgroupGroups_talkgroupId" FOREIGN KEY ("talkgroupId") REFERENCES "talkgroups" ("talkgroupId") ON DELETE CASCADE ON UPDATE CASCADE
  );`,

	`CREATE TABLE IF NOT EXISTS "calls" (
    "callId" bigserial NOT NULL PRIMARY KEY,
    "audio" bytea NOT NULL,
    "audioFilename" text NOT NULL,
    "audioMime" text NOT NULL,
    "siteRef" integer NOT NULL DEFAULT 0,
    "systemId" bigint NOT NULL,
    "talkgroupId" bigint NOT NULL,
    "timestamp" bigint NOT NULL,
    "frequency" integer NOT NULL DEFAULT 0,
    CONSTRAINT "calls_systemId" FOREIGN KEY ("systemId") REFERENCES "systems" ("systemId") ON DELETE CASCADE ON UPDATE CASCADE,
    CONSTRAINT "calls_talkgroupId" FOREIGN KEY ("talkgroupId") REFERENCES "talkgroups" ("talkgroupId") ON DELETE CASCADE ON UPDATE CASCADE
  );`,

	`CREATE INDEX IF NOT EXISTS "calls_idx" ON "calls" ("systemId","siteRef","talkgroupId","timestamp");`,
	`ALTER TABLE "calls" ADD COLUMN IF NOT EXISTS "frequency" integer NOT NULL DEFAULT 0;`,
	`ALTER TABLE "calls" ADD COLUMN IF NOT EXISTS "systemRef" integer NOT NULL DEFAULT 0;`,
	`ALTER TABLE "calls" ADD COLUMN IF NOT EXISTS "talkgroupRef" integer NOT NULL DEFAULT 0;`,
	`ALTER TABLE "calls" ADD COLUMN IF NOT EXISTS "toneSequence" text NOT NULL DEFAULT '';`,
	`ALTER TABLE "calls" ADD COLUMN IF NOT EXISTS "hasTones" boolean NOT NULL DEFAULT false;`,
	`ALTER TABLE "calls" ADD COLUMN IF NOT EXISTS "transcript" text NOT NULL DEFAULT '';`,
	`ALTER TABLE "calls" ADD COLUMN IF NOT EXISTS "transcriptConfidence" real NOT NULL DEFAULT 0;`,
	`ALTER TABLE "calls" ADD COLUMN IF NOT EXISTS "transcriptionStatus" text NOT NULL DEFAULT 'pending';`,
	`ALTER TABLE "calls" ADD COLUMN IF NOT EXISTS "transcriptionFailureReason" text NOT NULL DEFAULT '';`,
	`CREATE INDEX IF NOT EXISTS "calls_refs_idx" ON "calls" ("systemRef","talkgroupRef","timestamp");`,
	`CREATE INDEX IF NOT EXISTS "calls_tones_idx" ON "calls" ("hasTones","timestamp");`,
	`CREATE INDEX IF NOT EXISTS "calls_transcript_idx" ON "calls" ("transcriptionStatus","timestamp");`,
	// Standalone timestamp index for sorting/filtering without system/talkgroup filters
	`CREATE INDEX IF NOT EXISTS "calls_timestamp_idx" ON "calls" ("timestamp" DESC);`,
	`DROP TABLE IF EXISTS "callFrequencies";`,

	`CREATE TABLE IF NOT EXISTS "callPatches" (
    "callPatchId" bigserial NOT NULL PRIMARY KEY,
    "callId" bigint NOT NULL,
    "talkgroupId" bigint NOT NULL,
    CONSTRAINT "callPatches_callId" FOREIGN KEY ("callId") REFERENCES "calls" ("callId") ON DELETE CASCADE ON UPDATE CASCADE,
    CONSTRAINT "callPatches_talkgroupId" FOREIGN KEY ("talkgroupId") REFERENCES "talkgroups" ("talkgroupId") ON DELETE CASCADE ON UPDATE CASCADE
  );`,

	`CREATE TABLE IF NOT EXISTS "callUnits" (
    "callUnitId" bigserial NOT NULL PRIMARY KEY,
    "callId" bigint NOT NULL,
    "offset" float NOT NULL,
    "unitRef" bigint NOT NULL,
    CONSTRAINT "callUnits_callId" FOREIGN KEY ("callId") REFERENCES "calls" ("callId") ON DELETE CASCADE ON UPDATE CASCADE
  );`,
	
	// Migration: Change unitRef from integer to bigint for large radio unit IDs
	`ALTER TABLE "callUnits" ALTER COLUMN "unitRef" TYPE bigint;`,
	
	// Index for fast lookup of units by callId (critical for search performance)
	`CREATE INDEX IF NOT EXISTS "callUnits_callId_idx" ON "callUnits" ("callId", "offset");`,

	`CREATE TABLE IF NOT EXISTS "delayed" (
    "delayedId" bigserial NOT NULL PRIMARY KEY,
    "callId" bigint NOT NULL,
    "timestamp" bigint NOT NULL,
    CONSTRAINT "delayed_callId" FOREIGN KEY ("callId") REFERENCES "calls" ("callId") ON DELETE CASCADE ON UPDATE CASCADE
  );`,

	// Index for fast lookup of delayed calls (critical for search performance with LEFT JOIN)
	`CREATE INDEX IF NOT EXISTS "delayed_callId_idx" ON "delayed" ("callId");`,

	`CREATE TABLE IF NOT EXISTS "dirwatches" (
    "dirwatchId" bigserial NOT NULL PRIMARY KEY,
    "delay" integer NOT NULL DEFAULT 0,
    "deleteAfter" boolean NOT NULL DEFAULT false,
    "directory" text NOT NULL,
    "disabled" boolean NOT NULL DEFAULT false,
    "extension" text NOT NULL DEFAULT '',
    "frequency" integer NOT NULL DEFAULT 0,
    "mask" text NOT NULL DEFAULT '',
    "order" integer NOT NULL DEFAULT 0,
    "siteId" bigint NOT NULL DEFAULT 0,
    "systemId" bigint NOT NULL DEFAULT 0,
    "talkgroupId" bigint NOT NULL DEFAULT 0,
    "type" text NOT NULL DEFAULT ''
  );`,

	`CREATE TABLE IF NOT EXISTS "logs" (
    "logId" bigserial NOT NULL PRIMARY KEY,
    "level" text NOT NULL,
    "message" text NOT NULL,
    "timestamp" bigint NOT NULL
  );`,

	// Index for fast timestamp-based sorting and filtering on the logs table
	`CREATE INDEX IF NOT EXISTS "logs_timestamp_idx" ON "logs" ("timestamp" DESC);`,

	`CREATE TABLE IF NOT EXISTS "options" (
    "optionId" bigserial NOT NULL PRIMARY KEY,
    "key" text NOT NULL,
    "value" text NOT NULL
  );`,

	`CREATE TABLE IF NOT EXISTS "suspectedHallucinations" (
    "id" bigserial NOT NULL PRIMARY KEY,
    "phrase" text NOT NULL UNIQUE,
    "rejectedCount" integer NOT NULL DEFAULT 0,
    "acceptedCount" integer NOT NULL DEFAULT 0,
    "firstSeenAt" bigint NOT NULL DEFAULT 0,
    "lastSeenAt" bigint NOT NULL DEFAULT 0,
    "systemIds" text NOT NULL DEFAULT '',
    "status" text NOT NULL DEFAULT 'pending',
    "autoAdded" boolean NOT NULL DEFAULT false,
    "createdAt" bigint NOT NULL DEFAULT 0,
    "updatedAt" bigint NOT NULL DEFAULT 0
  );`,

	`CREATE TABLE IF NOT EXISTS "units" (
    "unitId" bigserial NOT NULL PRIMARY KEY,
    "label" text NOT NULL,
    "order" integer NOT NULL DEFAULT 0,
    "systemId" bigint NOT NULL,
    "unitRef" integer NOT NULL DEFAULT 0,
    "unitFrom" integer NOT NULL DEFAULT 0,
    "unitTo" integer NOT NULL DEFAULT 0,
    CONSTRAINT "units_systemId_fkey" FOREIGN KEY ("systemId") REFERENCES "systems" ("systemId") ON DELETE CASCADE ON UPDATE CASCADE
  );`,

	`CREATE TABLE IF NOT EXISTS "users" (
    "userId" bigserial NOT NULL PRIMARY KEY,
    "email" text NOT NULL UNIQUE,
    "password" text NOT NULL,
    "pin" text NOT NULL UNIQUE,
    "pinExpiresAt" bigint NOT NULL DEFAULT 0,
    "connectionLimit" integer NOT NULL DEFAULT 0,
    "verified" boolean NOT NULL DEFAULT false,
    "verificationToken" text NOT NULL DEFAULT '',
    "createdAt" text NOT NULL DEFAULT '',
    "lastLogin" text NOT NULL DEFAULT '',
    "firstName" text NOT NULL DEFAULT '',
    "lastName" text NOT NULL DEFAULT '',
    "zipCode" text NOT NULL DEFAULT '',
    "systems" text NOT NULL DEFAULT '',
    "delay" integer NOT NULL DEFAULT 0,
    "systemDelays" text NOT NULL DEFAULT '',
    "talkgroupDelays" text NOT NULL DEFAULT '',
    "stripeCustomerId" text NOT NULL DEFAULT '',
    "stripeSubscriptionId" text NOT NULL DEFAULT '',
    "subscriptionStatus" text NOT NULL DEFAULT '',
    "accountExpiresAt" bigint NOT NULL DEFAULT 0,
    "settings" text NOT NULL DEFAULT ''
  );`,

	`CREATE TABLE IF NOT EXISTS "userAlertPreferences" (
    "userAlertPreferenceId" bigserial NOT NULL PRIMARY KEY,
    "userId" bigint NOT NULL,
    "systemId" bigint NOT NULL,
    "talkgroupId" bigint NOT NULL,
    "alertEnabled" boolean NOT NULL DEFAULT false,
    "toneAlerts" boolean NOT NULL DEFAULT true,
    "keywordAlerts" boolean NOT NULL DEFAULT true,
    "keywords" text NOT NULL DEFAULT '[]',
    "keywordListIds" text NOT NULL DEFAULT '[]',
    "toneSetIds" text NOT NULL DEFAULT '[]',
    CONSTRAINT "userAlertPreferences_userId_fkey" FOREIGN KEY ("userId") REFERENCES "users" ("userId") ON DELETE CASCADE ON UPDATE CASCADE,
    UNIQUE ("userId", "systemId", "talkgroupId")
  );`,

	`CREATE TABLE IF NOT EXISTS "keywordLists" (
    "keywordListId" bigserial NOT NULL PRIMARY KEY,
    "label" text NOT NULL,
    "description" text NOT NULL DEFAULT '',
    "keywords" text NOT NULL DEFAULT '[]',
    "order" integer NOT NULL DEFAULT 0,
    "createdAt" bigint NOT NULL DEFAULT 0
  );`,

	`CREATE TABLE IF NOT EXISTS "alerts" (
    "alertId" bigserial NOT NULL PRIMARY KEY,
    "callId" bigint NOT NULL,
    "systemId" bigint NOT NULL,
    "talkgroupId" bigint NOT NULL,
    "alertType" text NOT NULL DEFAULT '',
    "toneDetected" boolean NOT NULL DEFAULT false,
    "toneSetId" text NOT NULL DEFAULT '',
    "keywordsMatched" text NOT NULL DEFAULT '[]',
    "transcriptSnippet" text NOT NULL DEFAULT '',
    "createdAt" bigint NOT NULL,
    CONSTRAINT "alerts_callId_fkey" FOREIGN KEY ("callId") REFERENCES "calls" ("callId") ON DELETE CASCADE ON UPDATE CASCADE,
    CONSTRAINT "alerts_systemId_fkey" FOREIGN KEY ("systemId") REFERENCES "systems" ("systemId") ON DELETE CASCADE ON UPDATE CASCADE,
    CONSTRAINT "alerts_talkgroupId_fkey" FOREIGN KEY ("talkgroupId") REFERENCES "talkgroups" ("talkgroupId") ON DELETE CASCADE ON UPDATE CASCADE
  );`,

	`CREATE INDEX IF NOT EXISTS "alerts_created_idx" ON "alerts" ("createdAt");`,
	`CREATE INDEX IF NOT EXISTS "alerts_call_idx" ON "alerts" ("callId");`,

	`CREATE TABLE IF NOT EXISTS "transcriptions" (
    "transcriptionId" bigserial NOT NULL PRIMARY KEY,
    "callId" bigint NOT NULL,
    "transcript" text NOT NULL,
    "confidence" real NOT NULL DEFAULT 0,
    "language" text NOT NULL DEFAULT 'en',
    "userId" bigint,
    "createdAt" bigint NOT NULL,
    CONSTRAINT "transcriptions_callId_fkey" FOREIGN KEY ("callId") REFERENCES "calls" ("callId") ON DELETE CASCADE ON UPDATE CASCADE,
    CONSTRAINT "transcriptions_userId_fkey" FOREIGN KEY ("userId") REFERENCES "users" ("userId") ON DELETE SET NULL ON UPDATE CASCADE
  );`,

	`CREATE INDEX IF NOT EXISTS "transcriptions_call_idx" ON "transcriptions" ("callId");`,

	`CREATE TABLE IF NOT EXISTS "keywordMatches" (
    "keywordMatchId" bigserial NOT NULL PRIMARY KEY,
    "callId" bigint NOT NULL,
    "userId" bigint NOT NULL,
    "keyword" text NOT NULL,
    "context" text NOT NULL DEFAULT '',
    "position" integer NOT NULL DEFAULT 0,
    "alerted" boolean NOT NULL DEFAULT false,
    CONSTRAINT "keywordMatches_callId_fkey" FOREIGN KEY ("callId") REFERENCES "calls" ("callId") ON DELETE CASCADE ON UPDATE CASCADE,
    CONSTRAINT "keywordMatches_userId_fkey" FOREIGN KEY ("userId") REFERENCES "users" ("userId") ON DELETE CASCADE ON UPDATE CASCADE
  );`,

	`CREATE INDEX IF NOT EXISTS "keywordMatches_user_idx" ON "keywordMatches" ("userId","callId");`,

	`CREATE TABLE IF NOT EXISTS "userGroups" (
    "userGroupId" bigserial NOT NULL PRIMARY KEY,
    "name" text NOT NULL,
    "description" text NOT NULL DEFAULT '',
    "systemAccess" text NOT NULL DEFAULT '',
    "delay" integer NOT NULL DEFAULT 0,
    "systemDelays" text NOT NULL DEFAULT '',
    "talkgroupDelays" text NOT NULL DEFAULT '',
    "connectionLimit" integer NOT NULL DEFAULT 0,
    "maxUsers" integer NOT NULL DEFAULT 0,
    "billingEnabled" boolean NOT NULL DEFAULT true,
    "stripePriceId" text NOT NULL DEFAULT '',
    "pricingOptions" text NOT NULL DEFAULT '',
    "billingMode" text NOT NULL DEFAULT 'all_users',
    "collectSalesTax" boolean NOT NULL DEFAULT false,
    "isPublicRegistration" boolean NOT NULL DEFAULT false,
    "allowAddExistingUsers" boolean NOT NULL DEFAULT false,
    "createdAt" bigint NOT NULL DEFAULT 0
  );`,

	`CREATE TABLE IF NOT EXISTS "registrationCodes" (
    "registrationCodeId" bigserial NOT NULL PRIMARY KEY,
    "code" text NOT NULL UNIQUE,
    "userGroupId" bigint NOT NULL,
    "createdBy" bigint NOT NULL,
    "expiresAt" bigint NOT NULL DEFAULT 0,
    "maxUses" integer NOT NULL DEFAULT 0,
    "currentUses" integer NOT NULL DEFAULT 0,
    "isOneTime" boolean NOT NULL DEFAULT false,
    "isActive" boolean NOT NULL DEFAULT true,
    "createdAt" bigint NOT NULL DEFAULT 0,
    CONSTRAINT "registrationCodes_userGroupId_fkey" FOREIGN KEY ("userGroupId") REFERENCES "userGroups" ("userGroupId") ON DELETE CASCADE ON UPDATE CASCADE,
    CONSTRAINT "registrationCodes_createdBy_fkey" FOREIGN KEY ("createdBy") REFERENCES "users" ("userId") ON DELETE CASCADE ON UPDATE CASCADE
  );`,

	`CREATE TABLE IF NOT EXISTS "userInvitations" (
    "userInvitationId" bigserial NOT NULL PRIMARY KEY,
    "email" text NOT NULL,
    "code" text NOT NULL UNIQUE,
    "userGroupId" bigint NOT NULL,
    "invitedBy" bigint NOT NULL,
    "invitedAt" bigint NOT NULL DEFAULT 0,
    "usedAt" bigint NOT NULL DEFAULT 0,
    "expiresAt" bigint NOT NULL DEFAULT 0,
    "status" text NOT NULL DEFAULT 'pending',
    CONSTRAINT "userInvitations_userGroupId_fkey" FOREIGN KEY ("userGroupId") REFERENCES "userGroups" ("userGroupId") ON DELETE CASCADE ON UPDATE CASCADE,
    CONSTRAINT "userInvitations_invitedBy_fkey" FOREIGN KEY ("invitedBy") REFERENCES "users" ("userId") ON DELETE CASCADE ON UPDATE CASCADE
  );`,

	`CREATE TABLE IF NOT EXISTS "transferRequests" (
    "transferRequestId" bigserial NOT NULL PRIMARY KEY,
    "userId" bigint NOT NULL,
    "fromGroupId" bigint NOT NULL,
    "toGroupId" bigint NOT NULL,
    "requestedBy" bigint NOT NULL,
    "approvedBy" bigint NOT NULL DEFAULT 0,
    "status" text NOT NULL DEFAULT 'pending',
    "requestedAt" bigint NOT NULL DEFAULT 0,
    "approvedAt" bigint NOT NULL DEFAULT 0,
    "approvalToken" text NOT NULL DEFAULT '',
    "approvalTokenExpiresAt" bigint NOT NULL DEFAULT 0,
    "approvalTokenUsed" boolean NOT NULL DEFAULT false,
    CONSTRAINT "transferRequests_userId_fkey" FOREIGN KEY ("userId") REFERENCES "users" ("userId") ON DELETE CASCADE ON UPDATE CASCADE,
    CONSTRAINT "transferRequests_fromGroupId_fkey" FOREIGN KEY ("fromGroupId") REFERENCES "userGroups" ("userGroupId") ON DELETE CASCADE ON UPDATE CASCADE,
    CONSTRAINT "transferRequests_toGroupId_fkey" FOREIGN KEY ("toGroupId") REFERENCES "userGroups" ("userGroupId") ON DELETE CASCADE ON UPDATE CASCADE,
    CONSTRAINT "transferRequests_requestedBy_fkey" FOREIGN KEY ("requestedBy") REFERENCES "users" ("userId") ON DELETE CASCADE ON UPDATE CASCADE
  );`,

	`CREATE TABLE IF NOT EXISTS "deviceTokens" (
    "deviceTokenId" bigserial NOT NULL PRIMARY KEY,
    "userId" bigint NOT NULL,
    "token" text NOT NULL,
    "fcmToken" text,
    "pushType" text NOT NULL DEFAULT 'onesignal',
    "platform" text NOT NULL DEFAULT 'android',
    "sound" text NOT NULL DEFAULT 'startup.wav',
    "createdAt" bigint NOT NULL DEFAULT 0,
    "lastUsed" bigint NOT NULL DEFAULT 0,
    CONSTRAINT "deviceTokens_userId_fkey" FOREIGN KEY ("userId") REFERENCES "users" ("userId") ON DELETE CASCADE ON UPDATE CASCADE,
    UNIQUE ("userId", "token")
  );`,
	// Add fcmToken and pushType columns if they don't exist (migration for existing databases)
	`ALTER TABLE "deviceTokens" ADD COLUMN IF NOT EXISTS "fcmToken" text;`,
	`ALTER TABLE "deviceTokens" ADD COLUMN IF NOT EXISTS "pushType" text NOT NULL DEFAULT 'onesignal';`,
}
