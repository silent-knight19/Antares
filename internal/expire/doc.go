// Package expire implements Redis-style active expiration: sample 20 volatile
// keys, repeat while more than 25% are expired, bounded by a per-cycle time
// budget (ADR-005). Lazy expiry lives in the store.
package expire
