package audit

import "log/slog"

type Service struct {
	logger *slog.Logger
}

func NewService(logger *slog.Logger) *Service {
	return &Service{logger: logger.With("component", "audit")}
}
