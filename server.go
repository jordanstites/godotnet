// Package godotnet is a networking library for Godot 4 multiplayer games.
// It owns transport (TLS-TCP and UDP), framing, session lifecycle, message
// dispatch, and the tick loop. It deliberately stays out of game logic.
//
// This file is the package entry point. The Server type, Config, NewServer,
// and Run land in later commits as the skeleton is filled in.
package godotnet
