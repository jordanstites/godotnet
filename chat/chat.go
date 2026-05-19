// Package chat provides channel-based messaging, direct messages, and
// scrollback history layered on top of the core godotnet primitives.
//
// TODO(v0.2): implement. The package is a placeholder so consumers can
// import the path today.
package chat

// Planned exported types and functions for v0.2:
//
//   type Channel struct { /* unexported */ }
//   type Manager struct { /* unexported */ }
//
//   func New(server *godotnet.Server) *Manager
//   func (m *Manager) CreateChannel(name string) *Channel
//   func (m *Manager) Join(playerID godotnet.PlayerID, channel string) error
//   func (m *Manager) Leave(playerID godotnet.PlayerID, channel string) error
//   func (m *Manager) Send(playerID godotnet.PlayerID, channel string, body string) error
//   func (m *Manager) DM(from, to godotnet.PlayerID, body string) error
//   func (m *Manager) History(channel string, limit int) []Message
