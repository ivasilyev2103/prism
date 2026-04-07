// Package cost tracks AI service expenses and subscription quota consumption.
//
// It supports multiple billing models (per_token, per_image, per_request,
// per_second, subscription) and a four-level budget hierarchy
// (global → project → provider → project×provider).
//
// An in-memory write buffer batches records and flushes to SQLite every second
// or every 100 records, reducing write contention under concurrent load.
package cost
