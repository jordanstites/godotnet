// Package sub provides server-pushed event subscriptions: a player
// subscribes to a named topic, the server publishes events on that
// topic, and the library fans them out only to subscribed sessions.
//
// TODO(v0.2): implement. The package is a placeholder so consumers can
// import the path today.
package sub

// Planned exported types and functions for v0.2:
//
//   type Topic string
//
//   type Manager struct { /* unexported */ }
//   func New(server *godotnet.Server) *Manager
//   func (m *Manager) Subscribe(playerID godotnet.PlayerID, topic Topic) error
//   func (m *Manager) Unsubscribe(playerID godotnet.PlayerID, topic Topic) error
//   func (m *Manager) Publish(topic Topic, msg proto.Message)
//   func (m *Manager) PublishOver(topic Topic, msg proto.Message, transport Transport)
//
//   type Transport int
//   const (
//       TransportUDP Transport = iota
//       TransportTCP
//   )
