// Package interest provides spatial interest management: each player
// has a position and an interest radius, and the library filters
// outbound broadcasts so that only players whose interest regions
// overlap the source receive the message.
//
// TODO(v0.2): implement. The package is a placeholder so consumers can
// import the path today.
package interest

// Planned exported types and functions for v0.2:
//
//   type Vec2 struct{ X, Y float32 }
//
//   type Manager struct { /* unexported */ }
//   func New(server *godotnet.Server, cellSize float32) *Manager
//   func (m *Manager) SetPosition(playerID godotnet.PlayerID, pos Vec2)
//   func (m *Manager) SetRadius(playerID godotnet.PlayerID, radius float32)
//   func (m *Manager) PlayersNear(pos Vec2, radius float32) []godotnet.PlayerID
//   func (m *Manager) Broadcast(tc godotnet.TickCtx, origin Vec2, radius float32, msg proto.Message)
