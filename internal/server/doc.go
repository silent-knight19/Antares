// Package server hosts the TCP listener, per-connection I/O goroutines, and
// the single-executor command loop owning the keyspace (ADR-002).
package server
