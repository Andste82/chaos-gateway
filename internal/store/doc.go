// Package store persists the configuration as immutable revisions (plan §2.14, §3.6).
//
// Layout below the configuration directory:
//
//	config.json                pointer: active revision, last known good, pending confirm, next id
//	revisions/000042.json      the revision: metadata, SHA-256 and the configuration; never changed
//	revisions/000042.status.json  status and apply times of the revision; rewritten atomically
//
// Every file carries a schema version. Writes are atomic: a temporary file in the same
// directory, fsync, rename, fsync of the directory, so a crash leaves the old or the new
// content, never a mixture. A revision whose content does not match its checksum is reported as
// corrupt instead of being used. There is no database in V1 (plan §3.6).
package store
