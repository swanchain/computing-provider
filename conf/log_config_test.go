package conf

import (
	"path/filepath"
	"testing"
)

func TestApplyLogDefaults(t *testing.T) {
	repo := "/repo"

	t.Run("empty section gets defaults under the repo", func(t *testing.T) {
		var l Log
		applyLogDefaults(&l, repo)
		if want := filepath.Join(repo, "logs"); l.Dir != want {
			t.Errorf("Dir = %q, want %q", l.Dir, want)
		}
		if l.Level != "info" || l.MaxSizeMB != 100 || l.MaxBackups != 5 || l.MaxAgeDays != 30 {
			t.Errorf("unexpected defaults: %+v", l)
		}
		if !l.CompressEnabled() || !l.StdoutEnabled() {
			t.Error("Compress and Stdout should default to true")
		}
	})

	t.Run("relative Dir resolves against the repo", func(t *testing.T) {
		l := Log{Dir: "var/log"}
		applyLogDefaults(&l, repo)
		if want := filepath.Join(repo, "var/log"); l.Dir != want {
			t.Errorf("Dir = %q, want %q", l.Dir, want)
		}
	})

	t.Run("absolute Dir is kept, so logs can live on another disk", func(t *testing.T) {
		l := Log{Dir: "/mnt/data/logs"}
		applyLogDefaults(&l, repo)
		if l.Dir != "/mnt/data/logs" {
			t.Errorf("Dir = %q, want /mnt/data/logs", l.Dir)
		}
	})

	t.Run("explicit values survive", func(t *testing.T) {
		no := false
		l := Log{Level: "warn", MaxSizeMB: 10, MaxBackups: 2, MaxAgeDays: 7, Compress: &no, Stdout: &no}
		applyLogDefaults(&l, repo)
		if l.Level != "warn" || l.MaxSizeMB != 10 || l.MaxBackups != 2 || l.MaxAgeDays != 7 {
			t.Errorf("defaults overwrote explicit values: %+v", l)
		}
		if l.CompressEnabled() || l.StdoutEnabled() {
			t.Error("explicit false should not be treated as unset")
		}
	})

	t.Run("negative MaxAgeDays disables the age limit", func(t *testing.T) {
		l := Log{MaxAgeDays: -1}
		applyLogDefaults(&l, repo)
		if got := l.MaxAge(); got != 0 {
			t.Errorf("MaxAge() = %d, want 0 (no limit)", got)
		}
	})
}
