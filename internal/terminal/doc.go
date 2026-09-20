// Package terminal owns the server-authoritative terminal emulator.
//
// A Core serializes every emulator mutation through one actor. Bytes emitted by
// the emulator, whether they encode user input or replies to terminal queries,
// leave through one bounded output stream. Callers must continuously drain that
// stream into the managed PTY; viewers never participate in query handling.
package terminal
