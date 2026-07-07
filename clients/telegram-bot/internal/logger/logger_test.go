package logger

import (
	"context"
	"log/slog"
	"testing"
)

func TestNew_LevelFiltering(t *testing.T) {
	tests := []struct {
		configLevel  string
		checkedLevel slog.Level
		wantEnabled  bool
	}{
		{configLevel: "debug", checkedLevel: slog.LevelDebug, wantEnabled: true},
		{configLevel: "info", checkedLevel: slog.LevelDebug, wantEnabled: false},
		{configLevel: "info", checkedLevel: slog.LevelInfo, wantEnabled: true},
		{configLevel: "warn", checkedLevel: slog.LevelInfo, wantEnabled: false},
		{configLevel: "warning", checkedLevel: slog.LevelWarn, wantEnabled: true},
		{configLevel: "error", checkedLevel: slog.LevelWarn, wantEnabled: false},
		{configLevel: "error", checkedLevel: slog.LevelError, wantEnabled: true},
		{configLevel: "", checkedLevel: slog.LevelInfo, wantEnabled: true},
		{configLevel: "not-a-level", checkedLevel: slog.LevelInfo, wantEnabled: true},
	}

	for _, tt := range tests {
		t.Run(tt.configLevel+"/"+tt.checkedLevel.String(), func(t *testing.T) {
			l := New(tt.configLevel)
			if got := l.Enabled(context.Background(), tt.checkedLevel); got != tt.wantEnabled {
				t.Errorf("Enabled(%s) with config level %q = %v, want %v", tt.checkedLevel, tt.configLevel, got, tt.wantEnabled)
			}
		})
	}
}

func TestNew_ReturnsNonNilLogger(t *testing.T) {
	if l := New("info"); l == nil {
		t.Fatal("New returned nil logger")
	}
}
