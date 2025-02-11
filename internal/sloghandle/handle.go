// Package sloghandle includes handlers for slog.
package sloghandle

import (
	"context"
	"log/slog"
)

// TODO(bmitch): this package is temporary.
// Switch to a built-in with a future Go release:
// <https://github.com/golang/go/issues/62005>

// DiscardHandler is used to automatically discard everything.
type DiscardHandler struct{}

var Discard DiscardHandler

func (d DiscardHandler) Enabled(context.Context, slog.Level) bool {
	return false
}

func (d DiscardHandler) Handle(context.Context, slog.Record) error {
	return nil
}

func (d DiscardHandler) WithAttrs([]slog.Attr) slog.Handler {
	return Discard
}

func (d DiscardHandler) WithGroup(string) slog.Handler {
	return Discard
}
