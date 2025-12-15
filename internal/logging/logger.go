package logging

import (
	"github.com/sakthiram/kiro-slack-agent/internal/config"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// NewLogger creates a new zap logger based on configuration.
func NewLogger(cfg *config.LoggingConfig) (*zap.Logger, error) {
	var zapCfg zap.Config

	if cfg.Format == "console" {
		zapCfg = zap.NewDevelopmentConfig()
		zapCfg.EncoderConfig.EncodeLevel = zapcore.CapitalColorLevelEncoder
	} else {
		zapCfg = zap.NewProductionConfig()
		zapCfg.EncoderConfig.TimeKey = "ts"
		zapCfg.EncoderConfig.EncodeTime = zapcore.ISO8601TimeEncoder
	}

	// Set log level
	level, err := zapcore.ParseLevel(cfg.Level)
	if err != nil {
		level = zapcore.InfoLevel
	}
	zapCfg.Level = zap.NewAtomicLevelAt(level)

	return zapCfg.Build()
}

// WithSessionID returns a logger with session_id field.
func WithSessionID(logger *zap.Logger, sessionID string) *zap.Logger {
	return logger.With(zap.String("session_id", sessionID))
}

// WithUserID returns a logger with user_id field.
func WithUserID(logger *zap.Logger, userID string) *zap.Logger {
	return logger.With(zap.String("user_id", userID))
}

// WithChannelID returns a logger with channel_id field.
func WithChannelID(logger *zap.Logger, channelID string) *zap.Logger {
	return logger.With(zap.String("channel_id", channelID))
}

// WithContext returns a logger with common context fields.
func WithContext(logger *zap.Logger, sessionID, userID, channelID string) *zap.Logger {
	return logger.With(
		zap.String("session_id", sessionID),
		zap.String("user_id", userID),
		zap.String("channel_id", channelID),
	)
}
