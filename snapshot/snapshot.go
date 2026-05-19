// Package snapshot provides a ring-buffered history of recent world
// snapshots, supporting client-side interpolation and lag compensation
// queries from the server.
//
// TODO(v0.2): implement. The package is a placeholder so consumers can
// import the path today.
package snapshot

// Planned exported types and functions for v0.2:
//
//   type Snapshot[T any] struct {
//       Tick uint64
//       At   time.Time
//       Data T
//   }
//
//   type History[T any] struct { /* unexported */ }
//   func New[T any](capacity int) *History[T]
//   func (h *History[T]) Push(tick uint64, at time.Time, data T)
//   func (h *History[T]) At(tick uint64) (Snapshot[T], bool)
//   func (h *History[T]) AtTime(t time.Time) (a, b Snapshot[T], lerp float32, ok bool)
//   func (h *History[T]) Latest() (Snapshot[T], bool)
