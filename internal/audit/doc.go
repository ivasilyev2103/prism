// Package audit provides an append-only request metadata log with tamper detection.
//
// Each record contains an HMAC-SHA256 hash chained to the previous record.
// WORM semantics are enforced via SQLite triggers (UPDATE/DELETE are forbidden).
// Request and response bodies are never stored.
//
// The HMAC key is derived from the master password via HKDF with a distinct
// info string, separate from the vault encryption key.
package audit
