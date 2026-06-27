// Copyright the olareg contributors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

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
