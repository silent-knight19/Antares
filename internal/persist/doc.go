// Package persist implements the framed AOF log [u32 len][u32 CRC32][RESP payload]
// with group commit, torn-tail recovery, and rewrite via the shared snapshot
// format (ADR-004, ADR-007, ADR-010).
package persist
