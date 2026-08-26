// Package logging points the shared logrus logger at rotating files under the
// provider's repo, replacing the SDK default of unrotated ./logs/*.log files
// relative to the process's working directory.
package logging

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/filswan/go-mcs-sdk/mcs/api/common/logs"
	"github.com/rifflock/lfshook"
	"github.com/sirupsen/logrus"
	"github.com/swanchain/computing-provider-v2/conf"
	lumberjack "gopkg.in/natefinch/lumberjack.v2"
)

// Setup configures the shared logger from the [Log] config section. It is safe
// to call before any config is loaded — the zero Log value yields the defaults.
func Setup(cfg conf.Log) error {
	if cfg.Dir == "" {
		return fmt.Errorf("log dir is empty")
	}
	if err := os.MkdirAll(cfg.Dir, 0755); err != nil {
		return fmt.Errorf("failed to create log dir %s: %w", cfg.Dir, err)
	}

	logger := logs.GetLogger()

	level, err := logrus.ParseLevel(cfg.Level)
	if err != nil {
		return fmt.Errorf("invalid log level %q: %w", cfg.Level, err)
	}
	logger.SetLevel(level)

	rotator := func(name string) io.Writer {
		return &lumberjack.Logger{
			Filename:   filepath.Join(cfg.Dir, name),
			MaxSize:    cfg.MaxSizeMB,
			MaxBackups: cfg.MaxBackups,
			MaxAge:     cfg.MaxAge(),
			Compress:   cfg.CompressEnabled(),
		}
	}
	info, warn, errw := rotator("info.log"), rotator("warn.log"), rotator("error.log")

	if cfg.StdoutEnabled() {
		logger.SetOutput(os.Stdout)
	} else {
		logger.SetOutput(io.Discard)
	}

	// Drop the SDK's hooks, which write unrotated files under ./logs relative to cwd.
	logger.Hooks = make(logrus.LevelHooks)
	logger.Hooks.Add(lfshook.NewHook(lfshook.WriterMap{
		logrus.TraceLevel: info,
		logrus.DebugLevel: info,
		logrus.InfoLevel:  info,
		logrus.WarnLevel:  warn,
		logrus.ErrorLevel: errw,
		logrus.FatalLevel: errw,
		logrus.PanicLevel: errw,
	}, logger.Formatter))

	return nil
}
