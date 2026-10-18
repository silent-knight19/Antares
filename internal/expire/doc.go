// Package expire implements lazy expiry on access plus the active sampling
// cycle (hz=10, sample 20 keys, repeat while >25% expired) (ADR-005).
package expire
