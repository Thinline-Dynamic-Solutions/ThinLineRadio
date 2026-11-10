// Copyright (C) 2025 Thinline Dynamic Solutions

package main

import (
	"fmt"
	"log"
)

func migrateIncidentMapping(db *Database) error {
	// Column adds first. Index builds are best-effort afterward so a long
	// CREATE INDEX on a huge calls table cannot abort the whole migration chain.
	queries := []string{
		`ALTER TABLE "systems" ADD COLUMN IF NOT EXISTS "incidentMappingConfig" text NOT NULL DEFAULT '{}'`,
		`ALTER TABLE "talkgroups" ADD COLUMN IF NOT EXISTS "incidentMappingConfig" text NOT NULL DEFAULT '{}'`,

		`ALTER TABLE "calls" ADD COLUMN IF NOT EXISTS "extractedAddress" text NOT NULL DEFAULT ''`,
		`ALTER TABLE "calls" ADD COLUMN IF NOT EXISTS "incidentAddress" text NOT NULL DEFAULT ''`,
		`ALTER TABLE "calls" ADD COLUMN IF NOT EXISTS "incidentCrossStreet1" text NOT NULL DEFAULT ''`,
		`ALTER TABLE "calls" ADD COLUMN IF NOT EXISTS "incidentCrossStreet2" text NOT NULL DEFAULT ''`,
		`ALTER TABLE "calls" ADD COLUMN IF NOT EXISTS "incidentNature" text NOT NULL DEFAULT ''`,
		`ALTER TABLE "calls" ADD COLUMN IF NOT EXISTS "incidentCommonName" text NOT NULL DEFAULT ''`,
		`ALTER TABLE "calls" ADD COLUMN IF NOT EXISTS "incidentAptUnit" text NOT NULL DEFAULT ''`,
		`ALTER TABLE "calls" ADD COLUMN IF NOT EXISTS "incidentLat" double precision NOT NULL DEFAULT 0`,
		`ALTER TABLE "calls" ADD COLUMN IF NOT EXISTS "incidentLon" double precision NOT NULL DEFAULT 0`,
		`ALTER TABLE "calls" ADD COLUMN IF NOT EXISTS "incidentGeocodeStatus" text NOT NULL DEFAULT ''`,
		`ALTER TABLE "calls" ADD COLUMN IF NOT EXISTS "incidentGeocodeSource" text NOT NULL DEFAULT ''`,
		`ALTER TABLE "calls" ADD COLUMN IF NOT EXISTS "incidentGeocodeQuery" text NOT NULL DEFAULT ''`,
		`ALTER TABLE "calls" ADD COLUMN IF NOT EXISTS "incidentMappingProcessedAt" bigint NOT NULL DEFAULT 0`,
		`ALTER TABLE "calls" ADD COLUMN IF NOT EXISTS "incidentNatureReviewedAt" bigint NOT NULL DEFAULT 0`,
		`ALTER TABLE "calls" ADD COLUMN IF NOT EXISTS "incidentAdditional" text NOT NULL DEFAULT '[]'`,

		`CREATE TABLE IF NOT EXISTS "mappingKnownStreets" (
			"mappingKnownStreetId" bigserial NOT NULL PRIMARY KEY,
			"systemId" bigint NOT NULL,
			"talkgroupId" bigint,
			"streetName" text NOT NULL,
			"createdAt" bigint NOT NULL DEFAULT 0,
			CONSTRAINT "mappingKnownStreets_systemId_fkey" FOREIGN KEY ("systemId") REFERENCES "systems" ("systemId") ON DELETE CASCADE,
			CONSTRAINT "mappingKnownStreets_talkgroupId_fkey" FOREIGN KEY ("talkgroupId") REFERENCES "talkgroups" ("talkgroupId") ON DELETE CASCADE
		)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS "mappingKnownStreets_scope_name_idx" ON "mappingKnownStreets" ("systemId", COALESCE("talkgroupId", 0), "streetName")`,

		`CREATE TABLE IF NOT EXISTS "mappingStreetCorrections" (
			"mappingStreetCorrectionId" bigserial NOT NULL PRIMARY KEY,
			"systemId" bigint NOT NULL,
			"talkgroupId" bigint,
			"badName" text NOT NULL,
			"correctName" text NOT NULL,
			"createdAt" bigint NOT NULL DEFAULT 0,
			CONSTRAINT "mappingStreetCorrections_systemId_fkey" FOREIGN KEY ("systemId") REFERENCES "systems" ("systemId") ON DELETE CASCADE,
			CONSTRAINT "mappingStreetCorrections_talkgroupId_fkey" FOREIGN KEY ("talkgroupId") REFERENCES "talkgroups" ("talkgroupId") ON DELETE CASCADE
		)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS "mappingStreetCorrections_scope_bad_idx" ON "mappingStreetCorrections" ("systemId", COALESCE("talkgroupId", 0), "badName")`,

		`CREATE TABLE IF NOT EXISTS "mappingKnownPlaces" (
			"mappingKnownPlaceId" bigserial NOT NULL PRIMARY KEY,
			"systemId" bigint NOT NULL,
			"talkgroupId" bigint,
			"placeKey" text NOT NULL,
			"displayName" text NOT NULL DEFAULT '',
			"lat" double precision NOT NULL DEFAULT 0,
			"lon" double precision NOT NULL DEFAULT 0,
			"addressHint" text NOT NULL DEFAULT '',
			"source" text NOT NULL DEFAULT 'manual',
			"updatedAt" bigint NOT NULL DEFAULT 0,
			CONSTRAINT "mappingKnownPlaces_systemId_fkey" FOREIGN KEY ("systemId") REFERENCES "systems" ("systemId") ON DELETE CASCADE,
			CONSTRAINT "mappingKnownPlaces_talkgroupId_fkey" FOREIGN KEY ("talkgroupId") REFERENCES "talkgroups" ("talkgroupId") ON DELETE CASCADE
		)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS "mappingKnownPlaces_scope_key_idx" ON "mappingKnownPlaces" ("systemId", COALESCE("talkgroupId", 0), "placeKey")`,

		`CREATE TABLE IF NOT EXISTS "mappingGeocodeCache" (
			"mappingGeocodeCacheId" bigserial NOT NULL PRIMARY KEY,
			"systemId" bigint NOT NULL,
			"queryNormalized" text NOT NULL,
			"lat" double precision NOT NULL DEFAULT 0,
			"lon" double precision NOT NULL DEFAULT 0,
			"formattedAddress" text NOT NULL DEFAULT '',
			"hitCount" integer NOT NULL DEFAULT 1,
			"lastUsedAt" bigint NOT NULL DEFAULT 0,
			CONSTRAINT "mappingGeocodeCache_systemId_fkey" FOREIGN KEY ("systemId") REFERENCES "systems" ("systemId") ON DELETE CASCADE
		)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS "mappingGeocodeCache_system_query_idx" ON "mappingGeocodeCache" ("systemId", "queryNormalized")`,

		// Negative cache: remember queries that already failed a provider lookup.
		`CREATE TABLE IF NOT EXISTS "mappingGeocodeMissCache" (
			"mappingGeocodeMissCacheId" bigserial NOT NULL PRIMARY KEY,
			"systemId" bigint NOT NULL,
			"queryNormalized" text NOT NULL DEFAULT '',
			"provider" text NOT NULL DEFAULT '',
			"reason" text NOT NULL DEFAULT '',
			"hitCount" integer NOT NULL DEFAULT 1,
			"lastUsedAt" bigint NOT NULL DEFAULT 0,
			CONSTRAINT "mappingGeocodeMissCache_systemId_fkey" FOREIGN KEY ("systemId") REFERENCES "systems" ("systemId") ON DELETE CASCADE
		)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS "mappingGeocodeMissCache_lookup_idx" ON "mappingGeocodeMissCache" ("systemId", "queryNormalized", "provider")`,

		// Audit trail for outbound external geocode HTTP (one row per send).
		`CREATE TABLE IF NOT EXISTS "mappingGeocodeExternalLog" (
			"mappingGeocodeExternalLogId" bigserial NOT NULL PRIMARY KEY,
			"systemId" bigint NOT NULL DEFAULT 0,
			"provider" text NOT NULL DEFAULT '',
			"queryNormalized" text NOT NULL DEFAULT '',
			"ok" boolean NOT NULL DEFAULT false,
			"lat" double precision NOT NULL DEFAULT 0,
			"lon" double precision NOT NULL DEFAULT 0,
			"matchedAddress" text NOT NULL DEFAULT '',
			"detail" text NOT NULL DEFAULT '',
			"sentAt" bigint NOT NULL DEFAULT 0,
			CONSTRAINT "mappingGeocodeExternalLog_systemId_fkey" FOREIGN KEY ("systemId") REFERENCES "systems" ("systemId") ON DELETE CASCADE
		)`,
		`CREATE INDEX IF NOT EXISTS "mappingGeocodeExternalLog_sent_idx" ON "mappingGeocodeExternalLog" ("sentAt" DESC)`,
		`CREATE INDEX IF NOT EXISTS "mappingGeocodeExternalLog_provider_sent_idx" ON "mappingGeocodeExternalLog" ("provider", "sentAt" DESC)`,

		// mappingImportBatches / mappingStreets / mappingAddressPoints /
		// mappingTigerSegments used to hold per-system OSM/TIGER data imported
		// by the built-in Overpass + TIGER ADDRFEAT importers. Geocoding no
		// longer maintains any local copy of OSM/TIGER data — every deployment
		// now defers to the relay's self-hosted Nominatim add-on instead.
		// Drop the tables (and whatever data they still hold) outright rather
		// than leaving unused schema + orphaned rows behind.
		`DROP TABLE IF EXISTS "mappingStreets" CASCADE`,
		`DROP TABLE IF EXISTS "mappingAddressPoints" CASCADE`,
		`DROP TABLE IF EXISTS "mappingTigerSegments" CASCADE`,
		`DROP TABLE IF EXISTS "mappingImportBatches" CASCADE`,

		// US-wide Census cartographic boundaries for incident map overlays.
		`CREATE TABLE IF NOT EXISTS "mappingBoundaries" (
			"mappingBoundaryId" bigserial NOT NULL PRIMARY KEY,
			"geoid" text NOT NULL,
			"name" text NOT NULL DEFAULT '',
			"stateFips" text NOT NULL DEFAULT '',
			"layerType" text NOT NULL DEFAULT '',
			"geometry" text NOT NULL DEFAULT '{}',
			"minLat" double precision NOT NULL DEFAULT 0,
			"minLon" double precision NOT NULL DEFAULT 0,
			"maxLat" double precision NOT NULL DEFAULT 0,
			"maxLon" double precision NOT NULL DEFAULT 0,
			"centroidLat" double precision NOT NULL DEFAULT 0,
			"centroidLon" double precision NOT NULL DEFAULT 0,
			"colorIndex" integer NOT NULL DEFAULT 0,
			"updatedAt" bigint NOT NULL DEFAULT 0
		)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS "mappingBoundaries_layer_geoid_idx" ON "mappingBoundaries" ("layerType", "geoid")`,
		`CREATE INDEX IF NOT EXISTS "mappingBoundaries_bbox_idx" ON "mappingBoundaries" ("layerType", "minLat", "maxLat", "minLon", "maxLon")`,
	}
	for _, q := range queries {
		if _, err := db.Sql.Exec(q); err != nil {
			return err
		}
	}

	// Do NOT CREATE INDEX on "calls" here. Production calls tables can be hundreds
	// of GB; building calls_incident_status_idx has segfaulted Postgres parallel
	// workers (signal 11) and put the cluster into recovery, which then hangs TLR
	// startup. Operators can build these offline later with:
	//   SET max_parallel_maintenance_workers = 0;
	//   CREATE INDEX CONCURRENTLY ...
	log.Println("incident mapping: skipping calls incident indexes at startup (build offline on large DBs)")
	return nil
}

// migrateIncidentMappingReason adds a durable skip/fail reason on calls so
// admins can see why a pin was not plotted without grepping event logs.
func migrateIncidentMappingReason(db *Database) error {
	_, err := db.Sql.Exec(`ALTER TABLE "calls" ADD COLUMN IF NOT EXISTS "incidentMappingReason" text NOT NULL DEFAULT ''`)
	if err != nil {
		return fmt.Errorf("migrateIncidentMappingReason: %w", err)
	}
	return nil
}
