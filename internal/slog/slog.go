// Package slog is a temporary interface to support older versions of Go.
package slog

type Logger interface {
	Debug(msg string, args ...any)
	Error(msg string, args ...any)
	Info(msg string, args ...any)
	Warn(msg string, args ...any)
}

// Null is a logger that does nothing.
type Null struct{}

func (n Null) Debug(msg string, args ...any) {}
func (n Null) Error(msg string, args ...any) {}
func (n Null) Info(msg string, args ...any)  {}
func (n Null) Warn(msg string, args ...any)  {}
