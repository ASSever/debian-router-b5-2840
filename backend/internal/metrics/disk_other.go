//go:build !linux

package metrics

import "github.com/ASSever/debian-router-b5-2840/backend/internal/hostmetrics"

func diskSpace(string) *hostmetrics.Space { return nil }
