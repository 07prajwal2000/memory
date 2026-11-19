package store

// migrations are applied in order; PRAGMA user_version tracks how many ran.
//
// The memories table deliberately stores no body: the markdown file on disk is
// the source of truth and the index can be rebuilt with Reconcile.
var migrations = []string{
	`
CREATE TABLE contexts(
	slug TEXT PRIMARY KEY,
	created_at INTEGER NOT NULL,
	expires_at INTEGER
);
CREATE TABLE memories(
	id TEXT PRIMARY KEY,
	context TEXT NOT NULL REFERENCES contexts(slug) ON DELETE CASCADE,
	name TEXT NOT NULL,
	has_name INTEGER NOT NULL,
	hash TEXT NOT NULL,
	version INTEGER NOT NULL,
	updated_at INTEGER NOT NULL,
	updated_by TEXT NOT NULL DEFAULT '',
	tags TEXT NOT NULL DEFAULT '[]',
	UNIQUE(context, name)
);
CREATE INDEX memories_context ON memories(context);
`,
	`ALTER TABLE memories ADD COLUMN size INTEGER NOT NULL DEFAULT 0;`,
}
