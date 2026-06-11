package main

import "testing"

func TestSplitPostgresqlSchema(t *testing.T) {
	schema := []string{
		`CREATE TABLE IF NOT EXISTS "calls" ("callId" bigserial PRIMARY KEY);`,
		`CREATE INDEX IF NOT EXISTS "calls_idx" ON "calls" ("systemId","timestamp");`,
		`ALTER TABLE "calls" ADD COLUMN IF NOT EXISTS "frequency" integer NOT NULL DEFAULT 0;`,
	}
	ddl, indexes := splitPostgresqlSchema(schema)
	if len(ddl) != 2 {
		t.Fatalf("ddl len = %d, want 2", len(ddl))
	}
	if len(indexes) != 1 {
		t.Fatalf("indexes len = %d, want 1", len(indexes))
	}
	if got := indexNameFromDDL(indexes[0]); got != "calls_idx" {
		t.Fatalf("index name = %q, want calls_idx", got)
	}
}
