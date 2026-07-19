package trafficlogger

import "github.com/apernet/hysteria/core/v2/server"

var _ server.TrafficLogger = (*MultiTrafficLogger)(nil)

// MultiTrafficLogger fans traffic and connection events out to multiple loggers.
// LogTraffic always calls every logger and returns false when at least one logger
// requests that the client be disconnected.
type MultiTrafficLogger struct {
	loggers []server.TrafficLogger
}

func NewMultiTrafficLogger(loggers ...server.TrafficLogger) *MultiTrafficLogger {
	filtered := make([]server.TrafficLogger, 0, len(loggers))
	for _, logger := range loggers {
		if logger != nil {
			filtered = append(filtered, logger)
		}
	}
	return &MultiTrafficLogger{loggers: filtered}
}

func (m *MultiTrafficLogger) LogTraffic(id string, tx, rx uint64) bool {
	allow := true
	for _, logger := range m.loggers {
		if !logger.LogTraffic(id, tx, rx) {
			allow = false
		}
	}
	return allow
}

func (m *MultiTrafficLogger) LogOnlineState(id string, online bool) {
	for _, logger := range m.loggers {
		logger.LogOnlineState(id, online)
	}
}

func (m *MultiTrafficLogger) TraceStream(stream server.HyStream, stats *server.StreamStats) {
	for _, logger := range m.loggers {
		logger.TraceStream(stream, stats)
	}
}

func (m *MultiTrafficLogger) UntraceStream(stream server.HyStream) {
	for _, logger := range m.loggers {
		logger.UntraceStream(stream)
	}
}
